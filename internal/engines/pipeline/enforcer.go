package pipeline

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/config"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/logging"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/saturation"
)

// RequestCountFuncType is the signature for functions that retrieve the total request count
// for a model over a specified retention period. This type alias improves readability
// and makes the function signature reusable across the codebase.
type RequestCountFuncType func(ctx context.Context, modelID, namespace string, retentionPeriod time.Duration) (float64, error)

// Enforcer applies scale-to-zero and minimum replica enforcement after saturation analysis.
type Enforcer struct {
	// requestCountFunc is a function that returns the total request count for a model.
	// Injected for testability.
	requestCountFunc RequestCountFuncType
}

// NewEnforcer creates a new scale-to-zero enforcer.
func NewEnforcer(requestCountFunc RequestCountFuncType) *Enforcer {
	return &Enforcer{
		requestCountFunc: requestCountFunc,
	}
}

// EnforcePolicy applies scale-to-zero and minimum replica enforcement to saturation targets.
//
// The logic is:
// 1. If scale-to-zero is enabled for the model:
//   - Query request count over retention period
//   - If no requests: set all variant targets to 0
//   - If requests exist: keep saturation targets unchanged
//
// 2. If scale-to-zero is disabled:
//   - Ensure at least 1 replica across all variants
//   - If total replicas would be 0, preserve 1 replica on the cheapest variant
//
// Parameters:
//   - ctx: Context for the operation
//   - modelID: The model identifier
//   - namespace: The namespace where the model is deployed
//   - saturationTargets: Current saturation targets (may be modified)
//   - variantAnalyses: Per-variant saturation analysis (for cost information)
//   - scaleToZeroConfig: Scale-to-zero configuration
//
// Returns the modified targets map and whether scale-to-zero was applied.
func (e *Enforcer) EnforcePolicy(
	ctx context.Context,
	modelID string,
	namespace string,
	saturationTargets map[string]int,
	variantAnalyses []interfaces.VariantSaturationAnalysis,
	scaleToZeroConfig config.ScaleToZeroConfigData,
) (map[string]int, bool) {
	logger := ctrl.LoggerFrom(ctx)

	// Check if scale-to-zero is enabled for this model
	scaleToZeroEnabled := config.IsScaleToZeroEnabled(scaleToZeroConfig, modelID)

	if scaleToZeroEnabled {
		targets, applied := e.applyScaleToZero(ctx, modelID, namespace, saturationTargets, scaleToZeroConfig)
		logger.V(logging.DEBUG).Info("Scale-to-zero policy enforced",
			"modelID", modelID,
			"scaleToZeroEnabled", true,
			"scaledToZero", applied)
		return targets, applied
	}

	// Scale-to-zero disabled: ensure minimum replicas
	targets, applied := e.ensureMinimumReplicas(ctx, modelID, saturationTargets, variantAnalyses)
	logger.V(logging.DEBUG).Info("Minimum replica policy enforced",
		"modelID", modelID,
		"scaleToZeroEnabled", false,
		"minimumPreserved", applied)
	return targets, applied
}

// applyScaleToZero checks if the model has had any requests and scales to zero if idle.
func (e *Enforcer) applyScaleToZero(
	ctx context.Context,
	modelID string,
	namespace string,
	targets map[string]int,
	scaleToZeroConfig config.ScaleToZeroConfigData,
) (map[string]int, bool) {
	logger := ctrl.LoggerFrom(ctx)

	// Get retention period for this model
	retentionPeriod := config.ScaleToZeroRetentionPeriod(scaleToZeroConfig, modelID)

	// Query request count
	requestCount, err := e.requestCountFunc(ctx, modelID, namespace, retentionPeriod)
	if err != nil {
		logger.Error(err, "Failed to get request count, keeping current targets",
			"modelID", modelID,
			"namespace", namespace)
		return targets, false
	}

	// If there were requests in the retention period, keep saturation targets
	if requestCount > 0 {
		logger.V(logging.DEBUG).Info("Model has recent requests, keeping saturation targets",
			"modelID", modelID,
			"requestCount", requestCount,
			"retentionPeriod", retentionPeriod)
		return targets, false
	}

	// No requests: scale to zero
	logger.Info("No requests in retention period, scaling to zero",
		"modelID", modelID,
		"namespace", namespace,
		"retentionPeriod", retentionPeriod)

	for variant := range targets {
		targets[variant] = 0
	}

	return targets, true
}

// ensureMinimumReplicas ensures at least 1 replica exists across all variants when scale-to-zero is disabled.
func (e *Enforcer) ensureMinimumReplicas(
	ctx context.Context,
	modelID string,
	targets map[string]int,
	variantAnalyses []interfaces.VariantSaturationAnalysis,
) (map[string]int, bool) {
	logger := ctrl.LoggerFrom(ctx)

	// Calculate total replicas
	totalReplicas := 0
	for _, count := range targets {
		totalReplicas += count
	}

	// If total replicas > 0, no action needed
	if totalReplicas > 0 {
		return targets, false
	}

	// Total is 0, need to preserve at least 1 replica on the cheapest variant
	// Build cost map from variant analyses
	variantCosts := make(map[string]float64)
	for _, va := range variantAnalyses {
		variantCosts[va.VariantName] = va.Cost
	}

	// Find cheapest variant
	var cheapestVariant string
	cheapestCost := float64(-1)

	for variant := range targets {
		cost, hasCost := variantCosts[variant]
		if !hasCost {
			cost = saturation.DefaultVariantCost // Use default if cost not available
		}

		if cheapestCost < 0 || cost < cheapestCost || (cost == cheapestCost && variant < cheapestVariant) {
			cheapestVariant = variant
			cheapestCost = cost
		}
	}

	if cheapestVariant != "" {
		targets[cheapestVariant] = 1
		logger.Info("Preserving minimum replica on cheapest variant (scale-to-zero disabled)",
			"modelID", modelID,
			"variant", cheapestVariant,
			"cost", cheapestCost)
		return targets, true
	}

	return targets, false
}
