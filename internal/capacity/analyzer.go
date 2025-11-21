package capacity

import (
	"context"
	"fmt"
	"time"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
)

// Analyzer implements the CapacityAnalyzer interface
type Analyzer struct{}

// NewAnalyzer creates a new capacity analyzer instance
func NewAnalyzer() interfaces.CapacityAnalyzer {
	return &Analyzer{}
}

// AnalyzeModelCapacity analyzes capacity for all variants of a model.
// It aggregates metrics across all replicas (from all variants) and determines:
// 1. Which replicas are non-saturated
// 2. Average spare capacity across non-saturated replicas
// 3. Whether to scale up (spare capacity < trigger)
// 4. Whether scale-down is safe (worst-case simulation)
func (a *Analyzer) AnalyzeModelCapacity(
	ctx context.Context,
	modelID string,
	namespace string,
	replicaMetrics []interfaces.ReplicaMetrics,
	config interfaces.CapacityScalingConfig,
) (*interfaces.ModelCapacityAnalysis, error) {

	if len(replicaMetrics) == 0 {
		return &interfaces.ModelCapacityAnalysis{
			ModelID:         modelID,
			Namespace:       namespace,
			AnalyzedAt:      time.Now(),
			TotalReplicas:   0,
			ShouldScaleUp:   false,
			ShouldScaleDown: false,
			ScaleDownSafe:   false,
			VariantAnalyses: []interfaces.VariantCapacityAnalysis{},
		}, nil
	}

	analysis := &interfaces.ModelCapacityAnalysis{
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

	// Pre-allocate slices with exact capacity
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

	variantAnalyses := make([]interfaces.VariantCapacityAnalysis, 0, len(variantMap))

	for variantName, metrics := range variantMap {
		variantAnalysis := a.analyzeVariant(variantName, metrics, config)
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

	// Step 2: Calculate average spare capacity across all non-saturated replicas
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
	analysis.ShouldScaleDown, analysis.ScaleDownSafe = a.isScaleDownSafe(
		replicaMetrics,
		config,
	)

	logger.Log.Debug("Capacity analysis completed",
		"modelID", modelID,
		"namespace", namespace,
		"totalReplicas", analysis.TotalReplicas,
		"nonSaturated", nonSaturatedCount,
		"avgSpareKv", fmt.Sprintf("%.3f", analysis.AvgSpareKvCapacity),
		"avgSpareQueue", fmt.Sprintf("%.1f", analysis.AvgSpareQueueLength),
		"shouldScaleUp", analysis.ShouldScaleUp,
		"scaleDownSafe", analysis.ScaleDownSafe)

	return analysis, nil
}

// analyzeVariant analyzes capacity for a single variant
func (a *Analyzer) analyzeVariant(
	variantName string,
	metrics []interfaces.ReplicaMetrics,
	config interfaces.CapacityScalingConfig,
) interfaces.VariantCapacityAnalysis {

	analysis := interfaces.VariantCapacityAnalysis{
		VariantName:       variantName,
		ReplicaCount:      len(metrics),
		SaturatedReplicas: []string{},
	}

	if len(metrics) > 0 {
		analysis.AcceleratorName = metrics[0].AcceleratorName
		analysis.Cost = metrics[0].Cost
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
			// Calculate spare capacity for non-saturated replica
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

// shouldScaleUp determines if scale-up is needed based on spare capacity triggers
func (a *Analyzer) shouldScaleUp(
	avgSpareKv float64,
	avgSpareQueue float64,
	config interfaces.CapacityScalingConfig,
) (bool, string) {

	kvTrigger := avgSpareKv < config.KvSpareTrigger
	queueTrigger := avgSpareQueue < config.QueueSpareTrigger

	if kvTrigger && queueTrigger {
		return true, fmt.Sprintf("both KV spare (%.3f < %.3f) and queue spare (%.1f < %.1f)",
			avgSpareKv, config.KvSpareTrigger, avgSpareQueue, config.QueueSpareTrigger)
	} else if kvTrigger {
		return true, fmt.Sprintf("KV spare capacity low (%.3f < %.3f)",
			avgSpareKv, config.KvSpareTrigger)
	} else if queueTrigger {
		return true, fmt.Sprintf("queue spare capacity low (%.1f < %.1f)",
			avgSpareQueue, config.QueueSpareTrigger)
	}

	return false, ""
}

// isScaleDownSafe simulates realistic load redistribution after removing one replica.
// Returns (shouldScaleDown, isSafe) where:
// - shouldScaleDown: always false (capacity analyzer only approves, doesn't initiate scale-down)
// - isSafe: true if removing one replica would leave adequate headroom
//
// Algorithm: Calculates total current load across non-saturated replicas, then simulates
// redistributing that load across (N-1) replicas to determine if spare capacity remains adequate.
func (a *Analyzer) isScaleDownSafe(
	replicaMetrics []interfaces.ReplicaMetrics,
	config interfaces.CapacityScalingConfig,
) (bool, bool) {

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
		logger.Log.Debugf("Scale-down unsafe: insufficient non-saturated replicas: nonSaturated=%d, required=%d",
			nonSaturatedCount, MinNonSaturatedReplicasForScaleDown)
		return false, false
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

	// Calculate spare capacity after redistribution
	remainingSpareKv := config.KvCacheThreshold - avgKvAfterRemoval
	remainingSpareQueue := config.QueueLengthThreshold - avgQueueAfterRemoval

	// Safe if both spare margins still exceed triggers
	kvSafe := remainingSpareKv >= config.KvSpareTrigger
	queueSafe := remainingSpareQueue >= config.QueueSpareTrigger

	isSafe := kvSafe && queueSafe

	if !isSafe {
		logger.Log.Debug("Scale-down unsafe: insufficient headroom after redistribution",
			"remainingSpareKv", fmt.Sprintf("%.3f", remainingSpareKv),
			"kvTrigger", fmt.Sprintf("%.3f", config.KvSpareTrigger),
			"kvSafe", kvSafe,
			"remainingSpareQueue", fmt.Sprintf("%.1f", remainingSpareQueue),
			"queueTrigger", fmt.Sprintf("%.1f", config.QueueSpareTrigger),
			"queueSafe", queueSafe)
	}

	// Capacity analyzer never initiates scale-down, only approves/denies
	return false, isSafe
}

// CalculateCapacityTargets determines target replicas per variant based on capacity analysis.
// Step 1: Pure capacity-based target calculation
// Uses replica count from capacity metrics (ready replicas) to avoid excessive scale-up.
// Rules:
// - If desired ≠ 0 and desired ≠ current: target = desired (preserve previous optimizer decision)
// - Else if capacity needs scale-up: cheapest variant gets readyReplicas+1
// - Else if capacity allows scale-down: most expensive variant gets readyReplicas-1
// - Else: target = readyReplicas (replicas with metrics)
func (a *Analyzer) CalculateCapacityTargets(
	capacityAnalysis *interfaces.ModelCapacityAnalysis,
	variantStates []interfaces.VariantReplicaState,
) map[string]int {

	targets := make(map[string]int)

	// Nil safety
	if capacityAnalysis == nil || len(capacityAnalysis.VariantAnalyses) == 0 {
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

	// Initialize all targets to ready replicas (those with metrics)
	// This prevents excessive scale-up when replicas are not yet ready
	for _, va := range capacityAnalysis.VariantAnalyses {
		targets[va.VariantName] = va.ReplicaCount
	}

	// Check if we should preserve any desired replicas
	// If desired ≠ 0 and desired ≠ current, preserve desired
	preservedVariants := make(map[string]bool)
	for _, va := range capacityAnalysis.VariantAnalyses {
		state := stateMap[va.VariantName]
		if state.DesiredReplicas != 0 && state.DesiredReplicas != state.CurrentReplicas {
			targets[va.VariantName] = state.DesiredReplicas
			preservedVariants[va.VariantName] = true
			logger.Log.Debug("Preserving desired replicas",
				"variant", va.VariantName,
				"currentReplicas", state.CurrentReplicas,
				"readyReplicas", va.ReplicaCount,
				"desired", state.DesiredReplicas)
		}
	}

	// Determine capacity action
	if capacityAnalysis.ShouldScaleUp {
		// Find cheapest variant that doesn't have preserved desired
		var cheapestNonPreserved *interfaces.VariantCapacityAnalysis
		for i := range capacityAnalysis.VariantAnalyses {
			va := &capacityAnalysis.VariantAnalyses[i]
			if preservedVariants[va.VariantName] {
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
			targets[cheapestNonPreserved.VariantName] = cheapestNonPreserved.ReplicaCount + 1
			logger.Log.Info("Capacity target: scale-up cheapest variant",
				"variant", cheapestNonPreserved.VariantName,
				"cost", cheapestNonPreserved.Cost,
				"currentReplicas", state.CurrentReplicas,
				"readyReplicas", cheapestNonPreserved.ReplicaCount,
				"target", targets[cheapestNonPreserved.VariantName],
				"reason", capacityAnalysis.ScaleUpReason)
		}

	} else if capacityAnalysis.ScaleDownSafe {
		// Find most expensive variant that doesn't have preserved desired
		var mostExpensiveNonPreserved *interfaces.VariantCapacityAnalysis
		for i := range capacityAnalysis.VariantAnalyses {
			va := &capacityAnalysis.VariantAnalyses[i]
			if preservedVariants[va.VariantName] {
				continue
			}
			// Can't scale down if at or below minimum (1 replica)
			if va.ReplicaCount <= 1 {
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
			targets[mostExpensiveNonPreserved.VariantName] = mostExpensiveNonPreserved.ReplicaCount - 1
			logger.Log.Info("Capacity target: scale-down most expensive variant",
				"variant", mostExpensiveNonPreserved.VariantName,
				"cost", mostExpensiveNonPreserved.Cost,
				"currentReplicas", state.CurrentReplicas,
				"readyReplicas", mostExpensiveNonPreserved.ReplicaCount,
				"target", targets[mostExpensiveNonPreserved.VariantName])
		}
	} else {
		logger.Log.Debug("Capacity targets: no change needed",
			"shouldScaleUp", capacityAnalysis.ShouldScaleUp,
			"scaleDownSafe", capacityAnalysis.ScaleDownSafe)
	}

	return targets
}

// ArbitrateWithModelBased arbitrates between capacity targets and model-based optimizer targets.
// Step 2: Arbitration using hybrid decision matrix
// Applies capacity safety overrides:
// - Capacity wants scale-up but model-based wants scale-down → veto (no change or scale-up)
// - Model-based wants scale-down but capacity unsafe → safety block (no change)
// - Otherwise: follow model-based recommendation if capacity allows
func (a *Analyzer) ArbitrateWithModelBased(
	capacityAnalysis *interfaces.ModelCapacityAnalysis,
	capacityTargets map[string]int,
	modelBasedTargets map[string]int,
	variantStates []interfaces.VariantReplicaState,
) []interfaces.VariantDecision {

	decisions := make([]interfaces.VariantDecision, 0, len(variantStates))

	// Build variant analysis map
	variantAnalysisMap := make(map[string]*interfaces.VariantCapacityAnalysis)
	if capacityAnalysis != nil {
		for i := range capacityAnalysis.VariantAnalyses {
			va := &capacityAnalysis.VariantAnalyses[i]
			variantAnalysisMap[va.VariantName] = va
		}
	}

	// Build state map
	stateMap := make(map[string]interfaces.VariantReplicaState)
	for _, state := range variantStates {
		stateMap[state.VariantName] = state
	}

	// Arbitrate for each variant
	for _, state := range variantStates {
		va := variantAnalysisMap[state.VariantName]
		capacityTarget := capacityTargets[state.VariantName]
		modelBasedTarget := modelBasedTargets[state.VariantName]

		decision := a.arbitrateVariant(
			capacityAnalysis,
			va,
			state,
			capacityTarget,
			modelBasedTarget,
		)

		decisions = append(decisions, decision)
	}

	return decisions
}

// arbitrateVariant applies hybrid decision matrix for a single variant
func (a *Analyzer) arbitrateVariant(
	modelAnalysis *interfaces.ModelCapacityAnalysis,
	variantAnalysis *interfaces.VariantCapacityAnalysis,
	state interfaces.VariantReplicaState,
	capacityTarget int,
	modelBasedTarget int,
) interfaces.VariantDecision {

	decision := interfaces.VariantDecision{
		VariantName:     state.VariantName,
		CurrentReplicas: state.CurrentReplicas,
		DesiredReplicas: state.DesiredReplicas,
	}

	// Populate fields from modelAnalysis (nil-safe)
	if modelAnalysis != nil {
		decision.Namespace = modelAnalysis.Namespace
		decision.ModelID = modelAnalysis.ModelID
	}

	// Populate fields from variantAnalysis (nil-safe)
	if variantAnalysis != nil {
		decision.AcceleratorName = variantAnalysis.AcceleratorName
		decision.Cost = variantAnalysis.Cost
	}

	// Determine actions
	var capacityAction interfaces.CapacityAction
	if capacityTarget > state.CurrentReplicas {
		capacityAction = interfaces.ActionScaleUp
	} else if capacityTarget < state.CurrentReplicas {
		capacityAction = interfaces.ActionScaleDown
	} else {
		capacityAction = interfaces.ActionNoChange
	}

	var modelBasedAction interfaces.CapacityAction
	if modelBasedTarget > state.CurrentReplicas {
		modelBasedAction = interfaces.ActionScaleUp
	} else if modelBasedTarget < state.CurrentReplicas {
		modelBasedAction = interfaces.ActionScaleDown
	} else {
		modelBasedAction = interfaces.ActionNoChange
	}

	// Apply hybrid decision matrix
	switch {
	case capacityAction == interfaces.ActionScaleUp && modelBasedAction == interfaces.ActionScaleDown:
		// Capacity veto: model-based wants to scale down but capacity needs more
		decision.Action = interfaces.ActionNoChange
		decision.TargetReplicas = state.CurrentReplicas
		decision.Reason = fmt.Sprintf("capacity veto: capacity needs scale-up (capacity=%d, model-based=%d)",
			capacityTarget, modelBasedTarget)
		decision.SafetyOverride = true
		decision.CapacityBased = true
		decision.ModelBasedDecision = true

	case modelBasedAction == interfaces.ActionScaleDown && modelAnalysis != nil && !modelAnalysis.ScaleDownSafe:
		// Safety block: model-based wants scale-down but capacity says unsafe
		decision.Action = interfaces.ActionNoChange
		decision.TargetReplicas = state.CurrentReplicas
		decision.Reason = fmt.Sprintf("capacity safety block: scale-down unsafe (model-based wants %d)",
			modelBasedTarget)
		decision.SafetyOverride = true
		decision.CapacityBased = true
		decision.ModelBasedDecision = true

	case capacityAction == interfaces.ActionScaleUp && modelBasedAction == interfaces.ActionNoChange:
		// Capacity-driven scale-up (model-based doesn't object)
		decision.Action = interfaces.ActionScaleUp
		decision.TargetReplicas = capacityTarget
		decision.Reason = fmt.Sprintf("capacity-driven scale-up to %d", capacityTarget)
		decision.CapacityBased = true
		decision.ModelBasedDecision = false

	case modelBasedAction == interfaces.ActionScaleUp || modelBasedAction == interfaces.ActionScaleDown:
		// Follow model-based recommendation (capacity allows it)
		decision.Action = modelBasedAction
		decision.TargetReplicas = modelBasedTarget
		decision.Reason = fmt.Sprintf("model-based recommendation: %s to %d (capacity allows)",
			modelBasedAction, modelBasedTarget)
		decision.CapacityBased = false
		decision.ModelBasedDecision = true

	default:
		// No change
		decision.Action = interfaces.ActionNoChange
		decision.TargetReplicas = state.CurrentReplicas
		decision.Reason = "no action needed"
		decision.CapacityBased = false
		decision.ModelBasedDecision = false
	}

	logger.Log.Info("Variant decision arbitrated",
		"variant", state.VariantName,
		"current", state.CurrentReplicas,
		"capacityTarget", capacityTarget,
		"modelBasedTarget", modelBasedTarget,
		"action", decision.Action,
		"targetReplicas", decision.TargetReplicas,
		"reason", decision.Reason)

	return decision
}
