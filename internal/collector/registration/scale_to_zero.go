// Package registration provides query registration functionality for metrics sources.
//
// This file provides scale-to-zero metrics collection using the source
// infrastructure with registered query templates.
package registration

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/source"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
)

// Query name constants for scale-to-zero metrics.
const (
	// QueryModelRequestCount is the query name for total model requests over a time window.
	QueryModelRequestCount = "model_request_count"

	// ParamRetentionPeriod is the parameter name for the retention period duration.
	ParamRetentionPeriod = "retentionPeriod"
)

// RegisterScaleToZeroQueries registers queries used for scale-to-zero decisions.
// This should be called during initialization to register query templates with the prometheus source.
func RegisterScaleToZeroQueries(sourceRegistry *source.SourceRegistry) {
	metricsSource := sourceRegistry.Get("prometheus")
	if metricsSource == nil {
		ctrl.Log.V(logging.DEBUG).Info("Prometheus source not registered, skipping scale-to-zero query registration")
		return
	}

	registry := metricsSource.QueryList()

	// Model request count over a retention period
	// Uses sum(increase(...)) to get total requests over the time window
	// The retentionPeriod parameter should be in Prometheus duration format (e.g., "10m", "1h")
	registry.MustRegister(source.QueryTemplate{
		Name:        QueryModelRequestCount,
		Type:        source.QueryTypePromQL,
		Template:    `sum(increase(vllm:request_success_total{namespace="{{.namespace}}",model_name="{{.modelID}}"}[{{.retentionPeriod}}]))`,
		Params:      []string{source.ParamNamespace, source.ParamModelID, ParamRetentionPeriod},
		Description: "Total successful requests for a model over the retention period",
	})
}

// CollectModelRequestCount collects the total number of successful requests for a model
// over the specified retention period. This is used for scale-to-zero decisions.
//
// Parameters:
//   - ctx: Context for the operation
//   - source: The metrics source to query
//   - modelID: The model identifier
//   - namespace: The namespace where the model is deployed
//   - retentionPeriod: How far back to look for requests
//
// Returns:
//   - float64: Total request count over the retention period (0 if no requests or error)
//   - error: Any error that occurred during collection
func CollectModelRequestCount(
	ctx context.Context,
	metricsSource source.MetricsSource,
	modelID string,
	namespace string,
	retentionPeriod time.Duration,
) (float64, error) {
	logger := ctrl.LoggerFrom(ctx)

	// Convert Go duration to Prometheus duration format
	retentionPeriodStr := utils.FormatPrometheusDuration(retentionPeriod)

	params := map[string]string{
		source.ParamModelID:   modelID,
		source.ParamNamespace: namespace,
		ParamRetentionPeriod:  retentionPeriodStr,
	}

	// Execute the query
	results, err := metricsSource.Refresh(ctx, source.RefreshSpec{
		Queries: []string{QueryModelRequestCount},
		Params:  params,
	})
	if err != nil {
		logger.V(logging.DEBUG).Info("Failed to query model request count, returning 0",
			"model", modelID,
			"namespace", namespace,
			"retentionPeriod", retentionPeriodStr,
			"error", err)
		return 0, nil // Return 0 instead of error - no requests is valid
	}

	// Extract the result
	result := results[QueryModelRequestCount]
	if result == nil {
		logger.V(logging.DEBUG).Info("No result for model request count query, returning 0",
			"model", modelID,
			"namespace", namespace,
			"retentionPeriod", retentionPeriodStr)
		return 0, nil
	}

	if result.HasError() {
		logger.V(logging.DEBUG).Info("Model request count query failed, returning 0",
			"model", modelID,
			"namespace", namespace,
			"retentionPeriod", retentionPeriodStr,
			"error", result.Error)
		return 0, nil
	}

	// Get the first value (sum query returns a single scalar)
	if len(result.Values) == 0 {
		logger.V(logging.DEBUG).Info("No values in model request count result, returning 0",
			"model", modelID,
			"namespace", namespace,
			"retentionPeriod", retentionPeriodStr)
		return 0, nil
	}

	count := result.FirstValue().Value

	logger.V(logging.DEBUG).Info("Collected model request count",
		"model", modelID,
		"namespace", namespace,
		"retentionPeriod", retentionPeriodStr,
		"count", count)

	return count, nil
}
