package interfaces

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// Metadata contains freshness information (optional)
	Metadata *ReplicaMetricsMetadata `json:"metadata,omitempty"`
}

// ReplicaMetricsMetadata contains freshness information for replica metrics
type ReplicaMetricsMetadata struct {
	// CollectedAt is when the metrics were collected
	CollectedAt time.Time
	// Age is the age of the metrics
	Age time.Duration
	// FreshnessStatus indicates freshness: "fresh", "stale", "unavailable"
	FreshnessStatus string
}

// ModelSaturationAnalysis holds saturation analysis results for a model (across all variants)
type ModelSaturationAnalysis struct {
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
	ScaleDownSafe bool // Indicates if scale-down simulation passed

	// Detailed variant breakdown
	VariantAnalyses []VariantSaturationAnalysis
}

// VariantSaturationAnalysis holds saturation analysis for a single variant
type VariantSaturationAnalysis struct {
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
	Action             SaturationAction
	CurrentReplicas    int
	TargetReplicas     int // Suggested replica count
	DesiredReplicas    int // Desired replicas from optimizer (from CRD status)
	Reason             string
	SaturationBased    bool        // True if decision is primarily saturation-driven
	ModelBasedDecision bool        // True if decision considers model-based optimizer
	SafetyOverride     bool        // True if saturation veto overrode model-based decision
	LastRunTime        metav1.Time // Time when decision was made (for status updates)
	SaturationOnly     bool        // True if operating in saturation-only mode (no model-based analysis)

	// CurrentAllocation carries the collected metrics/allocation state
	// This helps the Controller update status without re-collecting metrics
	// CurrentAllocation carries the collected metrics/allocation state
	// This helps the Controller update status without re-collecting metrics
	CurrentAllocation *Allocation
}

// SaturationAction represents the scaling action
type SaturationAction string

const (
	ActionScaleUp   SaturationAction = "scale-up"
	ActionScaleDown SaturationAction = "scale-down"
	ActionNoChange  SaturationAction = "no-change"
)

// VariantReplicaState holds the current and desired replica counts for a variant
type VariantReplicaState struct {
	VariantName     string
	CurrentReplicas int
	DesiredReplicas int // From optimizer/CRD status, 0 if not set
	// PendingReplicas are pods that exist but are not yet ready to serve traffic
	// (CurrentReplicas - ReadyReplicas). This typically occurs during scale-up when
	// new pods are starting (containers initializing, model loading, health checks).
	// Pod startup can take 2-7 minutes depending on model size and hardware.
	// WVA uses this to prevent cascade scaling - avoiding new scale-up requests
	// while pending pods are still becoming ready.
	PendingReplicas int
}

// SaturationAnalyzer analyzes replica saturation metrics and recommends scaling decisions
type SaturationAnalyzer interface {
	// AnalyzeModelSaturation analyzes saturation for all variants of a model
	// Returns saturation analysis with scale-up/scale-down recommendations
	AnalyzeModelSaturation(
		ctx context.Context,
		modelID string,
		namespace string,
		replicaMetrics []ReplicaMetrics,
		config SaturationScalingConfig,
	) (*ModelSaturationAnalysis, error)

	// CalculateSaturationTargets determines target replicas per variant based on saturation analysis.
	// Step 1: Pure saturation-based target calculation
	// - Uses ready replica count (those with metrics) to avoid excessive scale-up
	// - Preserves desired replicas when desired ≠ current (from previous optimizer run)
	// - Uses cost-based selection (cheapest for scale-up, most expensive for scale-down)
	// Returns: map[variantName]targetReplicas
	CalculateSaturationTargets(
		saturationAnalysis *ModelSaturationAnalysis,
		variantStates []VariantReplicaState,
	) map[string]int
}
