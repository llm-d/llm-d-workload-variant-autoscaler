package collector

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/capacity"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CapacityMetricsCollector collects vLLM capacity metrics from Prometheus
type CapacityMetricsCollector struct {
	promAPI   promv1.API
	k8sClient client.Client
}

// NewCapacityMetricsCollector creates a new capacity metrics collector
func NewCapacityMetricsCollector(promAPI promv1.API) *CapacityMetricsCollector {
	return &CapacityMetricsCollector{
		promAPI:   promAPI,
		k8sClient: nil, // Will be set when available
	}
}

// SetK8sClient sets the Kubernetes client for pod ownership lookups
func (cmc *CapacityMetricsCollector) SetK8sClient(k8sClient client.Client) {
	cmc.k8sClient = k8sClient
}

// escapePrometheusLabelValue escapes a label value for safe use in Prometheus queries.
// This prevents PromQL injection attacks by escaping quotes and backslashes.
// Prometheus label values can contain any characters, including forward slashes,
// but must be properly escaped when embedded in query strings.
func escapePrometheusLabelValue(value string) string {
	// Escape backslashes first (must be done before escaping quotes)
	value = regexp.MustCompile(`\\`).ReplaceAllString(value, `\\`)
	// Escape double quotes
	value = regexp.MustCompile(`"`).ReplaceAllString(value, `\"`)
	return value
}

// validatePrometheusLabel validates that a label value is not empty.
// Returns error if validation fails.
func validatePrometheusLabel(value, name string) error {
	if value == "" {
		return fmt.Errorf("%s cannot be empty", name)
	}
	return nil
}

// contextWithRespectedDeadline creates a timeout context that respects the parent context deadline.
// If the parent has a deadline shorter than the desired timeout, uses the parent's remaining time minus a buffer.
// Returns the context and cancel function.
func contextWithRespectedDeadline(parent context.Context, desiredTimeout time.Duration) (context.Context, context.CancelFunc) {
	deadline, hasDeadline := parent.Deadline()
	if !hasDeadline {
		// No parent deadline, use desired timeout
		return context.WithTimeout(parent, desiredTimeout)
	}

	// Calculate remaining time from parent deadline
	remaining := time.Until(deadline)
	if remaining <= 0 {
		// Parent already expired, use minimal timeout
		return context.WithTimeout(parent, time.Millisecond)
	}

	// If remaining time is less than desired, use remaining minus buffer
	const deadlineBuffer = 100 * time.Millisecond
	if remaining < desiredTimeout {
		timeout := remaining - deadlineBuffer
		if timeout < time.Millisecond {
			timeout = time.Millisecond
		}
		return context.WithTimeout(parent, timeout)
	}

	// Parent deadline is generous, use desired timeout
	return context.WithTimeout(parent, desiredTimeout)
}

// CollectReplicaMetrics collects KV cache and queue metrics for all replicas of a model.
// It queries Prometheus for:
// - constants.VLLMKvCacheUsagePerc (KV cache utilization 0.0-1.0)
// - constants.VLLMNumRequestsWaiting (queue length)
//
// Uses max_over_time[1m] to capture peak values in the last minute for safety-first
// capacity guardrails. This prevents missing saturation events that could occur between
// instant queries and provides more conservative capacity analysis.
//
// Uses deployment-to-pod mapping for accurate attribution.
// Each deployment corresponds to a VA, and we get
// the actual pods for each deployment using the pod lists.
func (cmc *CapacityMetricsCollector) CollectReplicaMetrics(
	ctx context.Context,
	modelID string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
	variantAutoscalings map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	variantCosts map[string]float64,
) ([]interfaces.ReplicaMetrics, error) {

	// Validate input to prevent injection and ensure valid queries
	if err := validatePrometheusLabel(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := validatePrometheusLabel(modelID, "modelID"); err != nil {
		return nil, err
	}

	// Query KV cache and queue metrics in parallel for better performance
	// Use result struct to avoid race conditions on error variables
	type queryResult struct {
		kvMetrics    map[string]float64
		queueMetrics map[string]int
		kvErr        error
		queueErr     error
	}
	result := &queryResult{}
	var resultMutex sync.Mutex
	var wg sync.WaitGroup

	wg.Add(2)

	// Query KV cache metrics in parallel
	go func() {
		defer wg.Done()
		kv, err := cmc.queryKvCacheMetrics(ctx, modelID, namespace)
		resultMutex.Lock()
		result.kvMetrics = kv
		result.kvErr = err
		resultMutex.Unlock()
	}()

	// Query queue metrics in parallel
	go func() {
		defer wg.Done()
		queue, err := cmc.queryQueueMetrics(ctx, modelID, namespace)
		resultMutex.Lock()
		result.queueMetrics = queue
		result.queueErr = err
		resultMutex.Unlock()
	}()

	wg.Wait()

	// Check for errors after both queries complete
	if result.kvErr != nil {
		return nil, fmt.Errorf("failed to query KV cache metrics: %w", result.kvErr)
	}
	if result.queueErr != nil {
		return nil, fmt.Errorf("failed to query queue metrics: %w", result.queueErr)
	}

	// Use results from struct
	kvMetricsMap := result.kvMetrics
	queueMetricsMap := result.queueMetrics

	// Merge metrics by pod and assign to variants using deployment-to-pod mapping
	replicaMetrics := cmc.mergeMetrics(ctx, kvMetricsMap, queueMetricsMap, modelID, namespace, deployments, variantAutoscalings, variantCosts)

	logger.Log.Debugf("Collected replica metrics: modelID=%s, namespace=%s, replicaCount=%d",
		modelID, namespace, len(replicaMetrics))

	return replicaMetrics, nil
}

// queryKvCacheMetrics queries constants.VLLMKvCacheUsagePerc metric with max_over_time[1m]
// to capture peak KV cache usage in the last minute for conservative capacity analysis.
func (cmc *CapacityMetricsCollector) queryKvCacheMetrics(
	ctx context.Context,
	modelID string,
	namespace string,
) (map[string]float64, error) {

	// Query for peak KV cache usage over last minute across all pods of this model (all variants)
	// Using max_over_time ensures we don't miss saturation events between queries
	// The outer 'max by (pod)' aggregates multiple scrape samples per pod into one value
	// vLLM uses 'model_name' label for the model identifier
	// Escape label values to prevent PromQL injection
	query := fmt.Sprintf(`max by (pod) (max_over_time(%s{namespace="%s",model_name="%s"}[1m]))`,
		constants.VLLMKvCacheUsagePerc, escapePrometheusLabelValue(namespace), escapePrometheusLabelValue(modelID))

	// Add timeout to prevent hanging on Prometheus issues (respects parent deadline)
	queryCtx, cancel := contextWithRespectedDeadline(ctx, 5*time.Second)
	defer cancel()

	result, warnings, err := cmc.promAPI.Query(queryCtx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	if len(warnings) > 0 {
		logger.Log.Warnf("Prometheus query returned warnings: query=%s, warnings=%v",
			query, warnings)
	}

	metricsMap := make(map[string]float64)

	if result.Type() == model.ValVector {
		vector := result.(model.Vector)
		for _, sample := range vector {
			podName := string(sample.Metric["pod"])
			if podName == "" {
				// Try alternative label names
				podName = string(sample.Metric["pod_name"])
			}

			if podName != "" {
				kvValue := float64(sample.Value)
				metricsMap[podName] = kvValue
				logger.Log.Infof("KV cache metric: pod=%s, usage=%.3f (%.1f%%)",
					podName, kvValue, kvValue*100)
			}
		}
	}

	logger.Log.Debugf("KV cache metrics collected (max over 1m): modelID=%s, namespace=%s, podCount=%d",
		modelID, namespace, len(metricsMap))

	return metricsMap, nil
}

// queryQueueMetrics queries constants.VLLMNumRequestsWaiting metric with max_over_time[1m]
// to capture peak queue length in the last minute for conservative capacity analysis.
func (cmc *CapacityMetricsCollector) queryQueueMetrics(
	ctx context.Context,
	modelID string,
	namespace string,
) (map[string]int, error) {

	// Query for peak queue length over last minute
	// Using max_over_time ensures we catch burst traffic that could saturate the system
	// The outer 'max by (pod)' aggregates multiple scrape samples per pod into one value
	// vLLM uses 'model_name' label for the model identifier
	// Escape label values to prevent PromQL injection
	query := fmt.Sprintf(`max by (pod) (max_over_time(%s{namespace="%s",model_name="%s"}[1m]))`,
		constants.VLLMNumRequestsWaiting, escapePrometheusLabelValue(namespace), escapePrometheusLabelValue(modelID))

	// Add timeout to prevent hanging on Prometheus issues (respects parent deadline)
	queryCtx, cancel := contextWithRespectedDeadline(ctx, 5*time.Second)
	defer cancel()

	result, warnings, err := cmc.promAPI.Query(queryCtx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	if len(warnings) > 0 {
		logger.Log.Warnf("Prometheus query returned warnings: query=%s, warnings=%v",
			query, warnings)
	}

	metricsMap := make(map[string]int)

	if result.Type() == model.ValVector {
		vector := result.(model.Vector)
		for _, sample := range vector {
			podName := string(sample.Metric["pod"])
			if podName == "" {
				podName = string(sample.Metric["pod_name"])
			}

			if podName != "" {
				queueLen := int(sample.Value)
				metricsMap[podName] = queueLen
				logger.Log.Infof("Queue metric: pod=%s, queueLength=%d", podName, queueLen)
			}
		}
	}

	logger.Log.Debugf("Queue metrics collected (max over 1m): modelID=%s, namespace=%s, podCount=%d",
		modelID, namespace, len(metricsMap))

	return metricsMap, nil
}

// mergeMetrics combines KV cache and queue metrics into ReplicaMetrics structs.
// Uses deployment label selectors to match pods to variants.
//
// Matching strategy:
// 1. If k8sClient is available: Query pods using deployment label selectors (proper Kubernetes way)
// 2. Fallback: Use naming convention (deployment-name prefix matching)
//
// This approach is more robust than pure name-based matching and aligns with
// Kubernetes best practices for pod-to-controller attribution.
func (cmc *CapacityMetricsCollector) mergeMetrics(
	ctx context.Context,
	kvMetrics map[string]float64,
	queueMetrics map[string]int,
	modelID string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
	variantAutoscalings map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	variantCosts map[string]float64,
) []interfaces.ReplicaMetrics {

	// Use union of pod names from both metric sets
	podSet := make(map[string]bool)
	for pod := range kvMetrics {
		podSet[pod] = true
	}
	for pod := range queueMetrics {
		podSet[pod] = true
	}

	replicaMetrics := make([]interfaces.ReplicaMetrics, 0, len(podSet))

	// Track variant matching statistics for logging
	variantPodCounts := make(map[string]int)
	unmatchedPods := 0

	for podName := range podSet {
		// Check for missing metrics and warn (prevents silent data loss)
		kvUsage, hasKv := kvMetrics[podName]
		queueLen, hasQueue := queueMetrics[podName]

		if !hasKv {
			logger.Log.Warnf("Pod missing KV cache metrics, using 0 (may cause incorrect capacity analysis): pod=%s, model=%s, namespace=%s",
				podName, modelID, namespace)
			kvUsage = 0
		}
		if !hasQueue {
			logger.Log.Warnf("Pod missing queue metrics, using 0 (may cause incorrect capacity analysis): pod=%s, model=%s, namespace=%s",
				podName, modelID, namespace)
			queueLen = 0
		}

		// Match pod to variant using deployment label selectors or owner references
		variantName := cmc.findDeploymentForPod(ctx, podName, namespace, deployments)

		// Skip pods that don't match any known deployment
		if variantName == "" {
			logger.Log.Warnf("Skipping pod that doesn't match any deployment: pod=%s, deployments=%v",
				podName, getDeploymentNames(deployments))
			unmatchedPods++
			continue
		}

		variantPodCounts[variantName]++

		// Get accelerator name from VariantAutoscaling label
		acceleratorName := ""
		if va, ok := variantAutoscalings[variantName]; ok && va != nil {
			if va.Labels != nil {
				if accName, exists := va.Labels["inference.optimization/acceleratorName"]; exists {
					acceleratorName = accName
				}
			}
		}

		if acceleratorName == "" {
			logger.Log.Warnf("Missing acceleratorName label on VariantAutoscaling: variant=%s, pod=%s", variantName, podName)
		}

		// Look up cost by variant name, default to DefaultVariantCost if not found
		cost := capacity.DefaultVariantCost
		if variantCosts != nil {
			if c, ok := variantCosts[variantName]; ok {
				cost = c
			}
		}

		metric := interfaces.ReplicaMetrics{
			PodName:         podName,
			ModelID:         modelID,
			Namespace:       namespace,
			VariantName:     variantName,
			AcceleratorName: acceleratorName,
			KvCacheUsage:    kvUsage,
			QueueLength:     queueLen,
			Cost:            cost,
		}

		replicaMetrics = append(replicaMetrics, metric)
	}

	// Log pod-to-variant matching summary
	if unmatchedPods > 0 {
		logger.Log.Warnf("Pod-to-variant matching summary: totalPods=%d, unmatchedPods=%d, variantCounts=%v",
			len(podSet), unmatchedPods, variantPodCounts)
	} else {
		logger.Log.Debugf("Pod-to-variant matching successful: totalPods=%d, variantCounts=%v",
			len(podSet), variantPodCounts)
	}

	return replicaMetrics
}

// getDeploymentNames extracts deployment names from the deployments map for logging
func getDeploymentNames(deployments map[string]*appsv1.Deployment) []string {
	names := make([]string, 0, len(deployments))
	for name := range deployments {
		names = append(names, name)
	}
	return names
}

// findDeploymentForPod finds which deployment owns a pod using Kubernetes API.
//
// Matching strategies (in order of preference):
// 1. If k8sClient is available: Query pods for each deployment using label selectors
// 2. Fallback: Use Kubernetes naming convention (deployment-name prefix matching)
//
// The label selector approach is the proper Kubernetes way as it uses the deployment's
// spec.selector to find matching pods, which is how Deployments actually manage pods.
//
// Returns the deployment name if found, empty string otherwise.
func (cmc *CapacityMetricsCollector) findDeploymentForPod(
	ctx context.Context,
	podName string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
) string {
	// Strategy 1: Use Kubernetes API with label selectors (preferred)
	if cmc.k8sClient != nil {
		for deploymentName, deployment := range deployments {
			// Use deployment's label selector to check if pod belongs to it
			selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
			if err != nil {
				logger.Log.Warnf("Invalid label selector for deployment: deployment=%s, error=%v", deploymentName, err)
				continue
			}

			// List pods matching this deployment's selector
			podList := &corev1.PodList{}
			listOpts := &client.ListOptions{
				Namespace:     namespace,
				LabelSelector: selector,
			}

			if err := cmc.k8sClient.List(ctx, podList, listOpts); err != nil {
				logger.Log.Warnf("Failed to list pods for deployment: deployment=%s, error=%v", deploymentName, err)
				continue
			}

			// Check if our pod is in the list
			for _, pod := range podList.Items {
				if pod.Name == podName {
					return deploymentName
				}
			}
		}
	}

	// Strategy 2: Fallback to naming convention
	var matchedDeployment string
	maxPrefixLen := 0

	for deploymentName := range deployments {
		prefix := deploymentName + "-"
		if strings.HasPrefix(podName, prefix) {
			// Use longest matching prefix to handle nested deployment names
			if len(prefix) > maxPrefixLen {
				matchedDeployment = deploymentName
				maxPrefixLen = len(prefix)
			}
		}
	}

	return matchedDeployment
}
