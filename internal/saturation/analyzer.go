package saturation

import (
	"context"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"
)

// Analyzer implements the SaturationAnalyzer interface
type Analyzer struct{}

// NewAnalyzer creates a new saturation analyzer instance
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

// AnalyzeModelSaturation analyzes Saturation for all variants of a model.
// It aggregates metrics across all replicas (from all variants) and determines:
// 1. Which replicas are non-saturated
// 2. Average spare Saturation across non-saturated replicas
// 3. Whether to scale up (spare Saturation < trigger)
// 4. Whether scale-down is safe (worst-case simulation)
func (a *Analyzer) AnalyzeModelSaturation(
	ctx context.Context,
	modelID string,
	namespace string,
	replicaMetrics []interfaces.ReplicaMetrics,
	config interfaces.SaturationScalingConfig,
) (*interfaces.ModelSaturationAnalysis, error) {

	if len(replicaMetrics) == 0 {
		return &interfaces.ModelSaturationAnalysis{
			ModelID:       modelID,
			Namespace:     namespace,
			AnalyzedAt:    time.Now(),
			TotalReplicas: 0,
			ShouldScaleUp: false,

			ScaleDownSafe:   false,
			VariantAnalyses: []interfaces.VariantSaturationAnalysis{},
		}, nil
	}

	analysis := &interfaces.ModelSaturationAnalysis{
		ModelID:    modelID,
		Namespace:  namespace,
		AnalyzedAt: time.Now(),
	}

	// Step 1: Group metrics by variant and calculate per-variant analysis
	// Pre-count variants to pre-allocate slices (avoids repeated slice reallocation)
	variantCounts := make(map[string]int)
	for _, metric := range replicaMetrics {
		variantCounts[metric.VariantName]++
	}

	// Pre-allocate slices with exact Saturation
	variantMap := make(map[string][]interfaces.ReplicaMetrics, len(variantCounts))
	for variant, count := range variantCounts {
		variantMap[variant] = make([]interfaces.ReplicaMetrics, 0, count)
	}

	// Populate with metrics (no reallocation needed)
	for _, metric := range replicaMetrics {
		variantMap[metric.VariantName] = append(variantMap[metric.VariantName], metric)
	}

	// Aggregate statistics across all replicas
	var totalSpareKv float64
	var totalSpareQueue float64
	var nonSaturatedCount int
	var maxKvUsage float64
	var maxQueueLen int

	variantAnalyses := make([]interfaces.VariantSaturationAnalysis, 0, len(variantMap))

	for variantName, metrics := range variantMap {
		variantAnalysis := a.analyzeVariant(ctx, variantName, metrics, config)
		variantAnalyses = append(variantAnalyses, variantAnalysis)

		// Aggregate across variants
		nonSaturatedCount += variantAnalysis.NonSaturatedCount
		totalSpareKv += variantAnalysis.AvgSpareKvCapacity * float64(variantAnalysis.NonSaturatedCount)
		totalSpareQueue += variantAnalysis.AvgSpareQueueLength * float64(variantAnalysis.NonSaturatedCount)

		// Track worst-case metrics
		if variantAnalysis.MaxKvCacheUsage > maxKvUsage {
			maxKvUsage = variantAnalysis.MaxKvCacheUsage
		}
		if variantAnalysis.MaxQueueLength > maxQueueLen {
			maxQueueLen = variantAnalysis.MaxQueueLength
		}
	}

	analysis.TotalReplicas = len(replicaMetrics)
	analysis.NonSaturatedCount = nonSaturatedCount
	analysis.VariantAnalyses = variantAnalyses

	// Step 2: Calculate average spare Saturation across all non-saturated replicas
	if nonSaturatedCount > 0 {
		analysis.AvgSpareKvCapacity = totalSpareKv / float64(nonSaturatedCount)
		analysis.AvgSpareQueueLength = totalSpareQueue / float64(nonSaturatedCount)
	}

	// Step 3: Determine scale-up recommendation
	analysis.ShouldScaleUp, analysis.ScaleUpReason = a.shouldScaleUp(
		analysis.AvgSpareKvCapacity,
		analysis.AvgSpareQueueLength,
		config,
	)

	// Step 4: Determine if scale-down is safe
	analysis.ScaleDownSafe = a.isScaleDownSafe(
		ctx,
		replicaMetrics,
		config,
	)

	ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("saturation analysis completed",
		"modelID", modelID,
		"namespace", namespace,
		"totalReplicas", analysis.TotalReplicas,
		"nonSaturated", nonSaturatedCount,
		"avgSpareKv", analysis.AvgSpareKvCapacity,
		"avgSpareQueue", analysis.AvgSpareQueueLength,
		"shouldScaleUp", analysis.ShouldScaleUp,
		"scaleDownSafe", analysis.ScaleDownSafe)

	return analysis, nil
}

// analyzeVariant analyzes Saturation for a single variant
func (a *Analyzer) analyzeVariant(
	ctx context.Context,
	variantName string,
	metrics []interfaces.ReplicaMetrics,
	config interfaces.SaturationScalingConfig,
) interfaces.VariantSaturationAnalysis {

	analysis := interfaces.VariantSaturationAnalysis{
		VariantName:       variantName,
		ReplicaCount:      len(metrics),
		SaturatedReplicas: []string{},
	}

	if len(metrics) > 0 {
		analysis.AcceleratorName = metrics[0].AcceleratorName
		analysis.Cost = metrics[0].Cost
		ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Variant analysis initialized",
			"variant", variantName,
			"accelerator", analysis.AcceleratorName,
			"cost", analysis.Cost,
			"replicaCount", len(metrics))
	}

	var totalSpareKv float64
	var totalSpareQueue float64
	var nonSaturatedCount int

	for _, metric := range metrics {
		// Check if replica is saturated
		isSaturated := metric.KvCacheUsage >= config.KvCacheThreshold ||
			float64(metric.QueueLength) >= config.QueueLengthThreshold

		if isSaturated {
			analysis.SaturatedReplicas = append(analysis.SaturatedReplicas, metric.PodName)
		} else {
			// Calculate spare Saturation for non-saturated replica
			spareKv := config.KvCacheThreshold - metric.KvCacheUsage
			spareQueue := config.QueueLengthThreshold - float64(metric.QueueLength)

			totalSpareKv += spareKv
			totalSpareQueue += spareQueue
			nonSaturatedCount++
		}

		// Track max usage
		if metric.KvCacheUsage > analysis.MaxKvCacheUsage {
			analysis.MaxKvCacheUsage = metric.KvCacheUsage
		}
		if metric.QueueLength > analysis.MaxQueueLength {
			analysis.MaxQueueLength = metric.QueueLength
		}
	}

	analysis.NonSaturatedCount = nonSaturatedCount

	// Calculate averages for non-saturated replicas
	if nonSaturatedCount > 0 {
		analysis.AvgSpareKvCapacity = totalSpareKv / float64(nonSaturatedCount)
		analysis.AvgSpareQueueLength = totalSpareQueue / float64(nonSaturatedCount)
	}

	return analysis
}

// shouldScaleUp determines if scale-up is needed based on spare Saturation triggers
func (a *Analyzer) shouldScaleUp(
	avgSpareKv float64,
	avgSpareQueue float64,
	config interfaces.SaturationScalingConfig,
) (bool, string) {

	kvTriggered := avgSpareKv < config.KvSpareTrigger
	queueTriggered := avgSpareQueue < config.QueueSpareTrigger

	// Early return if no triggers fired
	if !kvTriggered && !queueTriggered {
		return false, ""
	}

	// Build reason string based on which trigger(s) fired
	switch {
	case kvTriggered && queueTriggered:
		return true, fmt.Sprintf("both KV spare (%.3f < %.3f) and queue spare (%.1f < %.1f)",
			avgSpareKv, config.KvSpareTrigger, avgSpareQueue, config.QueueSpareTrigger)
	case kvTriggered:
		return true, fmt.Sprintf("KV spare Saturation low (%.3f < %.3f)",
			avgSpareKv, config.KvSpareTrigger)
	default: // only queueTriggered is true
		return true, fmt.Sprintf("queue spare Saturation low (%.1f < %.1f)",
			avgSpareQueue, config.QueueSpareTrigger)
	}
}

// isScaleDownSafe simulates realistic load redistribution after removing one replica.
// Returns isSafe where:
// - isSafe: true if removing one replica would leave adequate headroom
//
// Algorithm: Calculates total current load across non-saturated replicas, then simulates
// redistributing that load across (N-1) replicas to determine if spare Saturation remains adequate.
func (a *Analyzer) isScaleDownSafe(
	ctx context.Context,
	replicaMetrics []interfaces.ReplicaMetrics,
	config interfaces.SaturationScalingConfig,
) bool {

	// Collect non-saturated replicas
	var nonSaturatedMetrics []interfaces.ReplicaMetrics
	for _, m := range replicaMetrics {
		isSaturated := m.KvCacheUsage >= config.KvCacheThreshold ||
			float64(m.QueueLength) >= config.QueueLengthThreshold
		if !isSaturated {
			nonSaturatedMetrics = append(nonSaturatedMetrics, m)
		}
	}

	nonSaturatedCount := len(nonSaturatedMetrics)

	// Require minimum non-saturated replicas for scale-down safety
	// With fewer replicas, we cannot safely redistribute load without risking saturation
	if nonSaturatedCount < MinNonSaturatedReplicasForScaleDown {
		ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Scale-down unsafe: insufficient non-saturated replicas",
			"nonSaturated", nonSaturatedCount, "required", MinNonSaturatedReplicasForScaleDown)
		return false
	}

	// Calculate total load across all non-saturated replicas
	var totalKvLoad float64
	var totalQueueLoad int
	for _, m := range nonSaturatedMetrics {
		totalKvLoad += m.KvCacheUsage
		totalQueueLoad += m.QueueLength
	}

	// Simulate removing one replica: redistribute total load across remaining replicas
	remainingCount := nonSaturatedCount - 1
	avgKvAfterRemoval := totalKvLoad / float64(remainingCount)
	avgQueueAfterRemoval := float64(totalQueueLoad) / float64(remainingCount)

	// Calculate spare Saturation after redistribution
	remainingSpareKv := config.KvCacheThreshold - avgKvAfterRemoval
	remainingSpareQueue := config.QueueLengthThreshold - avgQueueAfterRemoval

	// Safe if both spare margins still exceed triggers
	kvSafe := remainingSpareKv >= config.KvSpareTrigger
	queueSafe := remainingSpareQueue >= config.QueueSpareTrigger

	isSafe := kvSafe && queueSafe

	if !isSafe {
		ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Scale-down unsafe: insufficient headroom after redistribution",
			"remainingSpareKv", remainingSpareKv, "kvTrigger", config.KvSpareTrigger, "kvSafe", kvSafe,
			"remainingSpareQueue", remainingSpareQueue, "queueTrigger", config.QueueSpareTrigger, "queueSafe", queueSafe)
	}

	// Saturation analyzer never initiates scale-down, only approves/denies
	return isSafe
}

// CalculateSaturationTargets determines target replicas per variant based on saturation analysis.
// Step 1: Pure saturation-based target calculation
// Uses replica count from Saturation metrics (ready replicas) to avoid excessive scale-up.
// Rules:
// - If desired ≠ 0 and desired ≠ current: target = desired (preserve previous optimizer decision)
// - Else if Saturation needs scale-up: cheapest variant gets readyReplicas+1
// - Else if Saturation allows scale-down: most expensive variant gets readyReplicas-1
// - Else: target = readyReplicas (replicas with metrics)
func (a *Analyzer) CalculateSaturationTargets(
	ctx context.Context,
	saturationAnalysis *interfaces.ModelSaturationAnalysis,
	variantStates []interfaces.VariantReplicaState,
) map[string]int {

	targets := make(map[string]int)

	// Nil safety
	if saturationAnalysis == nil || len(saturationAnalysis.VariantAnalyses) == 0 {
		// Default: current replicas
		for _, state := range variantStates {
			targets[state.VariantName] = state.CurrentReplicas
		}
		return targets
	}

	// Build state map for quick lookup
	stateMap := make(map[string]interfaces.VariantReplicaState)
	for _, state := range variantStates {
		stateMap[state.VariantName] = state
	}

	// Initialize all targets to current ready replicas (those with metrics) from deployment status
	// This prevents excessive scale-up when replicas are not yet ready
	for _, va := range saturationAnalysis.VariantAnalyses {
		state := stateMap[va.VariantName]

		// TODO: will need to adjust this logic to address readiness based on metrics
		// Check if the VA state is stable (DesiredReplicas and CurrentReplicas match) and all expected pods are reporting metrics
		isStable := (state.DesiredReplicas == 0 || state.DesiredReplicas == state.CurrentReplicas)
		allMetricsAvailable := (va.ReplicaCount == state.CurrentReplicas)

		if isStable && allMetricsAvailable {
			// Stable VA state and all pods have report metrics: use metrics count
			targets[va.VariantName] = va.ReplicaCount
			ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Target initialized to metrics count (stable)",
				"variant", va.VariantName, "count", va.ReplicaCount)
		} else {
			// Transitional state or incomplete metrics: preserve current replica count
			targets[va.VariantName] = state.CurrentReplicas
			if !allMetricsAvailable {
				ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Target initialized to current replicas (incomplete metrics)",
					"variant", va.VariantName, "currentReplicas", state.CurrentReplicas, "metricsCount", va.ReplicaCount)
			} else if !isStable {
				ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Target initialized to current replicas (transitioning)",
					"variant", va.VariantName, "desired", state.DesiredReplicas, "current", state.CurrentReplicas, "metricsCount", va.ReplicaCount)
			}
		}
	}

	// Check if we should preserve any desired replicas
	// If desired ≠ 0 and desired ≠ current, preserve desired
	preservedVariants := make(map[string]bool)
	for _, va := range saturationAnalysis.VariantAnalyses {
		state := stateMap[va.VariantName]
		if state.DesiredReplicas != 0 && state.DesiredReplicas != state.CurrentReplicas {
			targets[va.VariantName] = state.DesiredReplicas
			preservedVariants[va.VariantName] = true
			ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Preserving desired replicas",
				"variant", va.VariantName, "currentReplicas", state.CurrentReplicas, "readyReplicas", va.ReplicaCount, "desired", state.DesiredReplicas)
		}
	}

	// Determine saturation action
	if saturationAnalysis.ShouldScaleUp {
		// Find cheapest variant that doesn't have preserved desired and has no pending replicas.
		// We check pending replicas per-variant rather than globally, so a variant without
		// pending pods can scale up even if another variant has pending pods.
		var cheapestNonPreserved *interfaces.VariantSaturationAnalysis
		for i := range saturationAnalysis.VariantAnalyses {
			va := &saturationAnalysis.VariantAnalyses[i]
			if preservedVariants[va.VariantName] {
				continue
			}
			// Skip variants with pending replicas to prevent cascade scaling
			state := stateMap[va.VariantName]
			if state.PendingReplicas > 0 {
				ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Skipping variant with pending replicas for scale-up",
					"variant", va.VariantName, "pendingReplicas", state.PendingReplicas)
				continue
			}
			// Select cheapest, with stable tie-breaking by variant name (alphabetically first)
			if cheapestNonPreserved == nil ||
				va.Cost < cheapestNonPreserved.Cost ||
				(va.Cost == cheapestNonPreserved.Cost && va.VariantName < cheapestNonPreserved.VariantName) {
				cheapestNonPreserved = va
			}
		}

		if cheapestNonPreserved != nil {
			state := stateMap[cheapestNonPreserved.VariantName]
			// The base target is from initialization: if we preserved desired, it uses that; else, it uses current/metrics
			baseTarget := targets[cheapestNonPreserved.VariantName]
			targets[cheapestNonPreserved.VariantName] = baseTarget + 1
			ctrl.LoggerFrom(ctx).V(logging.VERBOSE).Info("Saturation target: scale-up cheapest variant",
				"variant", cheapestNonPreserved.VariantName, "cost", cheapestNonPreserved.Cost, "currentReplicas", state.CurrentReplicas,
				"readyReplicas", cheapestNonPreserved.ReplicaCount, "baseTarget", baseTarget, "target", targets[cheapestNonPreserved.VariantName], "reason", saturationAnalysis.ScaleUpReason)
		}
	} else if saturationAnalysis.ScaleDownSafe {
		// Find most expensive variant that doesn't have preserved desired
		var mostExpensiveNonPreserved *interfaces.VariantSaturationAnalysis
		for i := range saturationAnalysis.VariantAnalyses {
			va := &saturationAnalysis.VariantAnalyses[i]
			if preservedVariants[va.VariantName] {
				continue
			}
			// Can't scale down if at or below minimum (1 replica)
			// The base target is from initialization: if we preserved desired, it uses that; else, it uses current/metrics
			baseTarget := targets[va.VariantName]
			if baseTarget <= 1 {
				continue
			}
			// Select most expensive, with stable tie-breaking by variant name
			if mostExpensiveNonPreserved == nil ||
				va.Cost > mostExpensiveNonPreserved.Cost ||
				(va.Cost == mostExpensiveNonPreserved.Cost && va.VariantName > mostExpensiveNonPreserved.VariantName) {
				mostExpensiveNonPreserved = va
			}
		}

		if mostExpensiveNonPreserved != nil {
			state := stateMap[mostExpensiveNonPreserved.VariantName]
			// The base target is from initialization: if we preserved desired, it uses that; else, it uses current/metrics
			baseTarget := targets[mostExpensiveNonPreserved.VariantName]
			targets[mostExpensiveNonPreserved.VariantName] = baseTarget - 1
			ctrl.LoggerFrom(ctx).V(logging.VERBOSE).Info("Saturation target: scale-down most expensive variant",
				"variant", mostExpensiveNonPreserved.VariantName, "cost", mostExpensiveNonPreserved.Cost, "currentReplicas", state.CurrentReplicas,
				"readyReplicas", mostExpensiveNonPreserved.ReplicaCount, "baseTarget", baseTarget, "target", targets[mostExpensiveNonPreserved.VariantName])
		}
	} else {
		// No scaling action needed - Saturation is adequate and stable
		ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Saturation targets: no scaling needed",
			"avgSpareKvCapacity", saturationAnalysis.AvgSpareKvCapacity,
			"avgSpareQueueLength", saturationAnalysis.AvgSpareQueueLength)
	}

	return targets
}
