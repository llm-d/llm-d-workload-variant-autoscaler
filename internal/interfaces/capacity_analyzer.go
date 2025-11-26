package interfaces

import (
	"context"
	"time"
)

// ReplicaMetrics holds capacity-related metrics for a single replica
type ReplicaMetrics struct {
	PodName         string
	KvCacheUsage    float64 // KV cache utilization (0.0-1.0)
	QueueLength     int     // Number of requests waiting
	VariantName     string  // Name of the variant this replica belongs to
	Namespace       string
	ModelID         string  // Model ID for grouping variants
	AcceleratorName string  // Accelerator type for this variant
	Cost            float64 // Cost per replica (from CRD spec, default 10)
}

// ModelCapacityAnalysis holds capacity analysis results for a model (across all variants)
type ModelCapacityAnalysis struct {
	ModelID    string
	Namespace  string
	AnalyzedAt time.Time // Timestamp when analysis was performed

	// Aggregated metrics across all variants of this model
	TotalReplicas       int
	NonSaturatedCount   int // Replicas below saturation thresholds
	AvgSpareKvCapacity  float64
	AvgSpareQueueLength float64

	// Scale decision recommendations
	ShouldScaleUp bool
	ScaleUpReason string
	ScaleDownSafe bool // Indicates if scale-down is safe (simulation passed)

	// Detailed variant breakdown
	VariantAnalyses []VariantCapacityAnalysis
}

// VariantCapacityAnalysis holds capacity analysis for a single variant
type VariantCapacityAnalysis struct {
	VariantName         string
	AcceleratorName     string
	Cost                float64 // Cost per replica for this variant
	ReplicaCount        int
	NonSaturatedCount   int
	MaxKvCacheUsage     float64
	MaxQueueLength      int
	AvgSpareKvCapacity  float64
	AvgSpareQueueLength float64
	SaturatedReplicas   []string // Pod names of saturated replicas
}

// VariantDecision represents the scaling decision for a single variant
type VariantDecision struct {
	VariantName        string
	Namespace          string
	ModelID            string
	AcceleratorName    string
	Cost               float64
	Action             CapacityAction
	CurrentReplicas    int
	TargetReplicas     int // Suggested replica count
	DesiredReplicas    int // Desired replicas from optimizer (from CRD status)
	Reason             string
	CapacityBased      bool // True if decision is primarily capacity-driven
	ModelBasedDecision bool // True if decision considers model-based optimizer
	SafetyOverride     bool // True if capacity veto overrode model-based decision
	CapacityOnly       bool // True if operating in capacity-only mode (no model-based analysis)
}

// CapacityAction represents the scaling action
type CapacityAction string

const (
	ActionScaleUp   CapacityAction = "scale-up"
	ActionScaleDown CapacityAction = "scale-down"
	ActionNoChange  CapacityAction = "no-change"
)

// VariantReplicaState holds the current and desired replica counts for a variant
type VariantReplicaState struct {
	VariantName     string
	CurrentReplicas int
	DesiredReplicas int // From optimizer/CRD status, 0 if not set
}

// CapacityAnalyzer analyzes replica capacity metrics and recommends scaling decisions
type CapacityAnalyzer interface {
	// AnalyzeModelCapacity analyzes capacity for all variants of a model
	// Returns capacity analysis with scale-up/scale-down recommendations
	AnalyzeModelCapacity(
		ctx context.Context,
		modelID string,
		namespace string,
		replicaMetrics []ReplicaMetrics,
		config CapacityScalingConfig,
	) (*ModelCapacityAnalysis, error)

	// CalculateCapacityTargets determines target replicas per variant based on capacity analysis.
	// Step 1: Pure capacity-based target calculation
	// - Uses ready replica count (those with metrics) to avoid excessive scale-up
	// - Preserves desired replicas when desired ≠ current (from previous optimizer run)
	// - Uses cost-based selection (cheapest for scale-up, most expensive for scale-down)
	// Returns: map[variantName]targetReplicas
	CalculateCapacityTargets(
		capacityAnalysis *ModelCapacityAnalysis,
		variantStates []VariantReplicaState,
	) map[string]int

	// ArbitrateWithModelBased arbitrates between capacity targets and model-based optimizer targets.
	// Step 2: Arbitration (only when model-based optimizer provides recommendations)
	// - Applies hybrid decision matrix with capacity safety overrides
	// - Returns final per-variant decisions
	ArbitrateWithModelBased(
		capacityAnalysis *ModelCapacityAnalysis,
		capacityTargets map[string]int,
		modelBasedTargets map[string]int,
		variantStates []VariantReplicaState,
	) []VariantDecision
}
