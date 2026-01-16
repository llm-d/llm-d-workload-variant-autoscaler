/*
Copyright 2025 The llm-d Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package collector provides replica metrics collection functionality.
//
// This package provides ReplicaMetricsCollector which collects replica-level
// metrics for saturation analysis using the source infrastructure.
package collector

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/registration"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/source"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/saturation"
)

// ReplicaMetricsCollector collects replica-level metrics for saturation analysis
// using the source infrastructure.
type ReplicaMetricsCollector struct {
	source      source.MetricsSource
	k8sClient   client.Client
	podVAMapper *source.PodVAMapper
}

// NewReplicaMetricsCollector creates a new replica metrics collector.
func NewReplicaMetricsCollector(metricsSource source.MetricsSource, k8sClient client.Client) *ReplicaMetricsCollector {
	return &ReplicaMetricsCollector{
		source:      metricsSource,
		k8sClient:   k8sClient,
		podVAMapper: source.NewPodVAMapper(k8sClient),
	}
}

// CollectReplicaMetrics collects KV cache and queue metrics for all replicas of a model
// using the source infrastructure.
//
// This function mirrors the functionality of the original CollectReplicaMetrics in
// internal/collector/prometheus/saturation_metrics.go but uses the source
// infrastructure with registered query templates.
//
// Parameters:
//   - ctx: Context for the operation
//   - modelID: The model identifier to collect metrics for
//   - namespace: The namespace where the model is deployed
//   - deployments: Map of deployment name to deployment object
//   - variantAutoscalings: Map of deployment name to VA object
//   - variantCosts: Map of deployment name to cost value
//
// Returns:
//   - []interfaces.ReplicaMetrics: Per-pod metrics for saturation analysis
//   - error: Any error that occurred during collection
func (c *ReplicaMetricsCollector) CollectReplicaMetrics(
	ctx context.Context,
	modelID string,
	namespace string,
	deployments map[string]*appsv1.Deployment,
	variantAutoscalings map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	variantCosts map[string]float64,
) ([]interfaces.ReplicaMetrics, error) {
	logger := ctrl.LoggerFrom(ctx)

	params := map[string]string{
		source.ParamModelID:   modelID,
		source.ParamNamespace: namespace,
	}

	// Refresh saturation queries (KV cache and queue length)
	queries := []string{
		registration.QueryKvCacheUsage,
		registration.QueryQueueLength,
	}

	results, err := c.source.Refresh(ctx, source.RefreshSpec{
		Queries: queries,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to refresh saturation metrics: %w", err)
	}

	// podMetricData holds per-pod metric values and timestamps
	type podMetricData struct {
		kvUsage        float64
		kvTimestamp    time.Time
		hasKv          bool
		queueLen       int
		queueTimestamp time.Time
		hasQueue       bool
	}

	// Extract per-pod metrics from results
	podData := make(map[string]*podMetricData)

	// Process KV cache results
	if result := results[registration.QueryKvCacheUsage]; result != nil {
		if result.HasError() {
			return nil, fmt.Errorf("KV cache query failed: %w", result.Error)
		}
		for _, value := range result.Values {
			podName := value.Labels["pod"]
			if podName == "" {
				podName = value.Labels["pod_name"]
			}
			if podName == "" {
				continue
			}

			if podData[podName] == nil {
				podData[podName] = &podMetricData{}
			}
			podData[podName].kvUsage = value.Value
			podData[podName].kvTimestamp = value.Timestamp
			podData[podName].hasKv = true

			logger.V(logging.DEBUG).Info("KV cache metric",
				"pod", podName,
				"usage", value.Value,
				"usagePercent", value.Value*100)
		}
	}

	// Process queue length results
	if result := results[registration.QueryQueueLength]; result != nil {
		if result.HasError() {
			return nil, fmt.Errorf("queue length query failed: %w", result.Error)
		}
		for _, value := range result.Values {
			podName := value.Labels["pod"]
			if podName == "" {
				podName = value.Labels["pod_name"]
			}
			if podName == "" {
				continue
			}

			if podData[podName] == nil {
				podData[podName] = &podMetricData{}
			}
			podData[podName].queueLen = int(value.Value)
			podData[podName].queueTimestamp = value.Timestamp
			podData[podName].hasQueue = true

			logger.V(logging.DEBUG).Info("Queue metric",
				"pod", podName,
				"queueLength", int(value.Value))
		}
	}

	// Build replica metrics from pod data
	replicaMetrics := make([]interfaces.ReplicaMetrics, 0, len(podData))
	collectedAt := time.Now()

	for podName, data := range podData {
		// Skip pods that have no metrics at all
		if !data.hasKv && !data.hasQueue {
			continue
		}

		kvUsage := data.kvUsage
		queueLen := data.queueLen

		if !data.hasKv {
			logger.Info("Pod missing KV cache metrics, using 0",
				"pod", podName,
				"model", modelID,
				"namespace", namespace)
			kvUsage = 0
		}
		if !data.hasQueue {
			logger.Info("Pod missing queue metrics, using 0",
				"pod", podName,
				"model", modelID,
				"namespace", namespace)
			queueLen = 0
		}

		// Match pod to variant using deployment label selectors
		variantName := c.podVAMapper.FindVAForPod(ctx, podName, namespace, deployments, variantAutoscalings)

		if variantName == "" {
			logger.Info("Skipping pod that doesn't match any deployment",
				"pod", podName,
				"deployments", getDeploymentNames(deployments))
			continue
		}

		// Get accelerator name from VariantAutoscaling label
		acceleratorName := ""
		if va, ok := variantAutoscalings[variantName]; ok && va != nil {
			if va.Labels != nil {
				if accName, exists := va.Labels["inference.optimization/acceleratorName"]; exists {
					acceleratorName = accName
				}
			}
		}

		// Look up cost by variant name
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
			Metadata: &interfaces.ReplicaMetricsMetadata{
				CollectedAt:     collectedAt,
				Age:             0, // Fresh
				FreshnessStatus: "fresh",
			},
		}

		replicaMetrics = append(replicaMetrics, metric)
	}

	logger.V(logging.DEBUG).Info("Collected replica metrics",
		"modelID", modelID,
		"namespace", namespace,
		"replicaCount", len(replicaMetrics))

	return replicaMetrics, nil
}

// getDeploymentNames extracts deployment names from the deployments map.
func getDeploymentNames(deployments map[string]*appsv1.Deployment) []string {
	names := make([]string, 0, len(deployments))
	for name := range deployments {
		names = append(names, name)
	}
	return names
}
