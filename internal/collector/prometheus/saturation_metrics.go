package prometheus

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/saturation"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
)

// SaturationMetricsCollector collects vLLM metrics from Prometheus
type SaturationMetricsCollector struct {
	promAPI   promv1.API
	k8sClient client.Client
}

// NewSaturationMetricsCollector creates a new metrics collector
func NewSaturationMetricsCollector(promAPI promv1.API) *SaturationMetricsCollector {
	return &SaturationMetricsCollector{
		promAPI:   promAPI,
		k8sClient: nil, // Will be set when available
	}
}

// SetK8sClient sets the Kubernetes client for pod ownership lookups
func (cmc *SaturationMetricsCollector) SetK8sClient(k8sClient client.Client) {
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

// CollectReplicaMetrics collects KV cache and queue metrics for all replicas of a model.
// It queries Prometheus for:
// - constants.VLLMKvCacheUsagePerc (KV cache utilization 0.0-1.0)
// - constants.VLLMNumRequestsWaiting (queue length)
//
// Uses max_over_time[1m] to capture peak values in the last minute for safety-first
// guardrails. This prevents missing saturation events that could occur between
// instant queries and provides more conservative analysis.
//
// Uses deployment-to-pod mapping for accurate attribution.
// Each deployment corresponds to a VA, and we get
// the actual pods for each deployment using the pod lists.
func (cmc *SaturationMetricsCollector) CollectReplicaMetrics(
	ctx context.Context,
	modelID string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
	variantAutoscalings map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	variantCosts map[string]float64,
) ([]interfaces.ReplicaMetrics, error) {
	logger := ctrl.LoggerFrom(ctx)
	// Validate input to prevent injection and ensure valid queries
	// if err := validatePrometheusLabel(namespace, "namespace"); err != nil {
	// 	return nil, err
	// }
	// if err := validatePrometheusLabel(modelID, "modelID"); err != nil {
	// 	return nil, err
	// }

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

	logger.V(logging.DEBUG).Info("Collected replica metrics", "modelID", modelID, "namespace", namespace, "replicaCount", len(replicaMetrics))

	return replicaMetrics, nil
}

// queryKvCacheMetrics queries constants.VLLMKvCacheUsagePerc metric with max_over_time[1m]
// to capture peak KV cache usage in the last minute for conservative analysis.
func (cmc *SaturationMetricsCollector) queryKvCacheMetrics(
	ctx context.Context,
	modelID string,
	namespace string,
) (map[string]float64, error) {
	logger := ctrl.LoggerFrom(ctx)
	// Query for peak KV cache usage over last minute across all pods of this model (all variants)
	// Using max_over_time ensures we don't miss saturation events between queries
	// The outer 'max by (pod)' aggregates multiple scrape samples per pod into one value
	// vLLM uses 'model_name' label for the model identifier
	// Escape label values to prevent PromQL injection
	query := fmt.Sprintf(`max by (pod) (max_over_time(%s{namespace="%s",model_name="%s"}[1m]))`,
		constants.VLLMKvCacheUsagePerc, escapePrometheusLabelValue(namespace), escapePrometheusLabelValue(modelID))

	// Add timeout to prevent hanging on Prometheus issues
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, warnings, err := utils.QueryPrometheusWithBackoff(queryCtx, cmc.promAPI, query)
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	if len(warnings) > 0 {
		logger.Info("Prometheus query returned warnings", "query", query, "warnings", warnings)
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
				logger.Info("KV cache metric", "pod", podName, "usage", kvValue, "usagePercent", kvValue*100)
			}
		}
	}

	logger.V(logging.DEBUG).Info("KV cache metrics collected (max over 1m)", "modelID", modelID, "namespace", namespace, "podCount", len(metricsMap))

	return metricsMap, nil
}

// queryQueueMetrics queries constants.VLLMNumRequestsWaiting metric with max_over_time[1m]
// to capture peak queue length in the last minute for conservative saturation analysis.
func (cmc *SaturationMetricsCollector) queryQueueMetrics(
	ctx context.Context,
	modelID string,
	namespace string,
) (map[string]int, error) {
	logger := ctrl.LoggerFrom(ctx)
	// Query for peak queue length over last minute
	// Using max_over_time ensures we catch burst traffic that could saturate the system
	// The outer 'max by (pod)' aggregates multiple scrape samples per pod into one value
	// vLLM uses 'model_name' label for the model identifier
	// Escape label values to prevent PromQL injection
	query := fmt.Sprintf(`max by (pod) (max_over_time(%s{namespace="%s",model_name="%s"}[1m]))`,
		constants.VLLMNumRequestsWaiting, escapePrometheusLabelValue(namespace), escapePrometheusLabelValue(modelID))

	// Add timeout to prevent hanging on Prometheus issues
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, warnings, err := utils.QueryPrometheusWithBackoff(queryCtx, cmc.promAPI, query)
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	if len(warnings) > 0 {
		logger.Info("Prometheus query returned warnings", "query", query, "warnings", warnings)
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
				logger.Info("Queue metric", "pod", podName, "queueLength", queueLen)
			}
		}
	}

	logger.V(logging.DEBUG).Info("Queue metrics collected (max over 1m)", "modelID", modelID, "namespace", namespace, "podCount", len(metricsMap))

	return metricsMap, nil
}

// mergeMetrics combines KV cache and queue metrics into ReplicaMetrics structs.
// Uses deployment label selectors to match pods to variants.
//
// Matching strategy:
// 1. Query for pods using deployment label selectors
// 2. Fallback: Use naming convention (deployment-name prefix matching)
//
// This approach is more robust than pure name-based matching and aligns with
// Kubernetes best practices for pod-to-controller attribution.
func (cmc *SaturationMetricsCollector) mergeMetrics(
	ctx context.Context,
	kvMetrics map[string]float64,
	queueMetrics map[string]int,
	modelID string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
	variantAutoscalings map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	variantCosts map[string]float64,
) []interfaces.ReplicaMetrics {
	logger := ctrl.LoggerFrom(ctx)
	// Use union of pod names from both metric sets
	podSet := make(map[string]bool)
	for pod := range kvMetrics {
		podSet[pod] = true
	}
	for pod := range queueMetrics {
		podSet[pod] = true
	}

	// Prometheus retains metrics from terminated pods for a time period, causing stale metrics to be pulled.
	// Verify pod existence using Prometheus kube-state-metrics to filter out stale pods.
	// Note: this may still be subject to staleness due to scrape intervals - the observed lag is typically ~30s.
	existingPods := cmc.getExistingPods(ctx, namespace, deployments, podSet)
	stalePodCount := 0

	// If getExistingPods returns empty but we have candidate pods with metrics,
	// skip the filtering - this handles the case where kube_pod_info hasn't been
	// scraped yet for new pods. It's better to include all candidates than to
	// filter them all out and skip saturation analysis entirely.
	// Note: This workaround may include metrics from recently-terminated pods if
	// kube_pod_info is stale. The typical staleness window is ~30s based on scrape intervals.
	// TODO: Consider adding time-based filtering or retry logic for more accurate pod filtering.
	if len(existingPods) == 0 && len(podSet) > 0 {
		logger.Info("kube_pod_info returned no pods but we have metric candidates, skipping stale pod filtering",
			"candidatePods", len(podSet), "namespace", namespace, "model", modelID)
	} else {
		// Filter out pods that don't exist according to the queried Prometheus kube-state-metrics
		for podName := range podSet {
			if !existingPods[podName] {
				stalePodCount++
				logger.V(logging.DEBUG).Info("Filtering pod from stale vLLM metrics", "pod", podName, "namespace", namespace, "model", modelID)
				delete(podSet, podName)
			}
		}
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
			logger.Info("Pod missing KV cache metrics, using 0 (may cause incorrect saturation analysis)", "pod", podName, "model", modelID, "namespace", namespace)
			kvUsage = 0
		}
		if !hasQueue {
			logger.Info("Pod missing queue metrics, using 0 (may cause incorrect saturation analysis)", "pod", podName, "model", modelID, "namespace", namespace)
			queueLen = 0
		}

		// Match pod to variant using deployment label selectors or owner references
		variantName := cmc.findDeploymentForPod(ctx, podName, namespace, deployments)

		// Skip pods that don't match any known deployment
		if variantName == "" {
			logger.Info("Skipping pod that doesn't match any deployment", "pod", podName, "deployments", getDeploymentNames(deployments))
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
			logger.Info("Missing acceleratorName label on VariantAutoscaling", "variant", variantName, "pod", podName)
		}

		// Look up cost by variant name, default to DefaultVariantCost if not found
		cost := saturation.DefaultVariantCost
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
		logger.Info("Pod-to-variant matching summary", "totalPods", len(podSet), "unmatchedPods", unmatchedPods, "variantCounts", variantPodCounts)
	} else {
		logger.V(logging.DEBUG).Info("Pod-to-variant matching successful", "totalPods", len(podSet), "variantCounts", variantPodCounts)
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
func (cmc *SaturationMetricsCollector) findDeploymentForPod(
	ctx context.Context,
	podName string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
) string {
	logger := ctrl.LoggerFrom(ctx)
	// Strategy 1: Use Kubernetes API with label selectors (preferred)
	if cmc.k8sClient != nil {
		for deploymentName, deployment := range deployments {
			// Use deployment's label selector to check if pod belongs to it
			selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
			if err != nil {
				logger.Info("Invalid label selector for deployment", "deployment", deploymentName, "error", err)
				continue
			}

			// List pods matching this deployment's selector
			podList := &corev1.PodList{}
			listOpts := &client.ListOptions{
				Namespace:     namespace,
				LabelSelector: selector,
			}

			if err := cmc.k8sClient.List(ctx, podList, listOpts); err != nil {
				logger.Info("Failed to list pods for deployment", "deployment", deploymentName, "error", err)
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

// getExistingPods filters candidate pods using Prometheus kube_pod_info metric.
// Queries for the current state from kube-state-metrics using deployment name filtering
//
// TODO(note): this approach may still be subject to staleness, as the scrape interval (typically 15-30s)
// adds latency between pod termination and metric removal
// Returns a map of pod names that have current metrics in Prometheus.
func (cmc *SaturationMetricsCollector) getExistingPods(
	ctx context.Context,
	namespace string,
	deployments map[string]*appsv1.Deployment,
	candidatePods map[string]bool,
) map[string]bool {
	logger := ctrl.LoggerFrom(ctx)
	existingPods := make(map[string]bool)

	// Build pod name regex filter from deployment names (pod=~"deployment1-.*|deployment2-.*|deployment3-.*")
	// To reduce the query scope
	var podQueryFilter string
	if len(deployments) > 0 {
		deploymentNames := make([]string, 0, len(deployments))
		for deploymentName := range deployments {
			// Escape deployment name for regex and add suffix pattern
			escapedName := escapePrometheusLabelValue(deploymentName)
			deploymentNames = append(deploymentNames, escapedName+"-.*")
		}
		podQueryFilter = fmt.Sprintf(`,pod=~"%s"`, strings.Join(deploymentNames, "|"))
	}

	// Query kube_pod_info for current pods in namespace with deployment name filtering
	// kube_pod_info is a gauge metric from kube-state-metrics that reflects current pod state
	// Note: this may still be subject to staleness due to scrape intervals - the observed lag is typically ~30s.
	query := fmt.Sprintf(`kube_pod_info{namespace="%s"%s}`, escapePrometheusLabelValue(namespace), podQueryFilter)

	result, warnings, err := utils.QueryPrometheusWithBackoff(ctx, cmc.promAPI, query)
	if err != nil {
		logger.Error(err, "Failed to query Prometheus for pod existence", "namespace", namespace)
		// On error, assume all candidate pods exist to prevent false negatives
		return candidatePods
	}

	if len(warnings) > 0 {
		logger.Info("Prometheus pod existence query warnings", "query", query, "warnings", warnings)
	}

	// Extract pod names from result
	if result.Type() == model.ValVector {
		vector := result.(model.Vector)
		for _, sample := range vector {
			podName := string(sample.Metric["pod"])
			if podName == "" {
				logger.Info("Empty pod name in kube_pod_info metric", "namespace", namespace, "metric", sample.Metric)
				continue
			}
			// Validate pod name is present in the candidate list
			if !candidatePods[podName] {
				continue
			}
			existingPods[podName] = true
		}
	}

	return existingPods
}
