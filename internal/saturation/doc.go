// Package saturation implements saturation-based analysis for LLM inference workloads.
//
// The saturation package provides algorithms to analyze server saturation levels
// and make scaling decisions based on capacity utilization. This is the default
// and recommended scaling mode for WVA.
//
// # What is Saturation?
//
// Saturation measures how close an inference server is to full capacity:
//
//	Saturation = max(KV_Cache_Utilization, Queue_Depth_Factor)
//
// Where:
//   - KV_Cache_Utilization: Percentage of GPU KV cache memory used (0-100%)
//   - Queue_Depth_Factor: Normalized queue depth relative to threshold
//
// Key insight: A saturated server has limited spare capacity for additional
// requests, leading to increased latency and potential SLO violations.
//
// # Saturation Thresholds
//
// Configured via ConfigMap (wva-configmap-saturation-scaling):
//
//	saturation_threshold: 70.0     # Target saturation (scale at this point)
//	saturation_trigger: 50.0       # Spare capacity trigger (scale-up signal)
//	scale_down_threshold: 30.0     # Safe scale-down saturation level
//
// Example scenarios:
//
//	Saturation 85% → Scale up (above threshold)
//	Saturation 65% → Maintain (within target range)
//	Saturation 25% → Consider scale down (low utilization)
//
// # Analysis Algorithm
//
// The Analyzer performs per-model saturation analysis:
//
//  1. Collect metrics from all replicas (via MetricsCollector)
//  2. Calculate saturation for each replica
//  3. Identify non-saturated replicas (below threshold)
//  4. Calculate average spare capacity
//  5. Determine scaling action:
//     - Scale up if spare capacity < trigger
//     - Scale down if simulation shows safety
//     - Maintain if within target range
//
// # Replica-Level Analysis
//
// Each replica is analyzed individually:
//
//	type ReplicaMetrics struct {
//	    ReplicaName          string
//	    KVCacheUtilization   float64  // 0-100%
//	    QueueDepth           int
//	    RequestRate          float64  // requests/sec
//	    AvgInputTokens       float64
//	    AvgOutputTokens      float64
//	}
//
// Saturation calculation:
//
//	replica_saturation = max(
//	    kv_cache_utilization,
//	    queue_depth / queue_threshold * 100
//	)
//
// # Model-Level Aggregation
//
// Results are aggregated across all replicas of a model:
//
//	type ModelSaturationAnalysis struct {
//	    ModelID             string
//	    TotalReplicas       int
//	    SaturatedReplicas   int
//	    NonSaturatedReplicas int
//	    AvgSpareCapacity    float64  // Average across non-saturated
//	    ShouldScaleUp       bool
//	    ScaleDownSafe       bool
//	    VariantAnalyses     []VariantSaturationAnalysis
//	}
//
// # Scale-Up Decision
//
// Scale up when:
//
//	avg_spare_capacity < saturation_trigger
//
// This indicates non-saturated replicas are approaching capacity and
// additional replicas are needed to maintain headroom.
//
// Example:
//
//	Replicas: [85%, 80%, 75%]  → All saturated
//	Avg Spare: 0%              → Scale up immediately
//
//	Replicas: [65%, 60%, 55%]  → All non-saturated
//	Avg Spare: 35%             → Spare capacity < 50% trigger → Scale up
//
// # Scale-Down Decision
//
// Scale down only if simulation shows safety:
//
//  1. Remove replica with lowest saturation
//  2. Simulate redistributing its load to remaining replicas
//  3. Check if any replica would exceed scale_down_threshold
//  4. Scale down only if all replicas remain under threshold
//
// This conservative approach prevents oscillation and ensures stability.
//
// # Variant-Level Tracking
//
// For multi-variant models (different batch sizes, GPU types):
//
//	type VariantSaturationAnalysis struct {
//	    VariantID           string
//	    TotalReplicas       int
//	    SaturatedReplicas   int
//	    AvgSaturation       float64
//	    DesiredReplicas     int
//	}
//
// Each variant is scaled independently based on its saturation profile.
//
// # Configuration
//
// Saturation parameters are loaded from ConfigMap:
//
//	apiVersion: v1
//	kind: ConfigMap
//	metadata:
//	  name: wva-configmap-saturation-scaling
//	data:
//	  saturation_threshold: "70.0"
//	  saturation_trigger: "50.0"
//	  scale_down_threshold: "30.0"
//	  queue_depth_threshold: "10"
//
// Adjust based on workload characteristics:
//   - Higher threshold: More aggressive utilization, potential latency spikes
//   - Lower threshold: More conservative, higher costs
//   - Wider trigger: Faster scale-up response
//
// # Integration with Controller
//
// The analyzer is called during reconciliation:
//
//	analyzer := saturation.NewAnalyzer()
//	analysis, err := analyzer.AnalyzeModelSaturation(
//	    ctx,
//	    modelID,
//	    namespace,
//	    replicaMetrics,
//	    config,
//	)
//
//	if analysis.ShouldScaleUp {
//	    desiredReplicas = currentReplicas + 1
//	} else if analysis.ScaleDownSafe {
//	    desiredReplicas = currentReplicas - 1
//	}
//
// # Metrics
//
// The analyzer emits these metrics:
//
//	wva_replica_saturation{replica="$name"} = 75.0
//	wva_model_spare_capacity{model="$id"} = 35.0
//	wva_saturated_replica_count{model="$id"} = 2
//	wva_non_saturated_replica_count{model="$id"} = 3
//
// # Advantages vs. Queueing Theory
//
// Saturation-based scaling offers several benefits:
//
//  1. No model parameters required (works with any LLM architecture)
//  2. Fast analysis (no solver needed)
//  3. Adaptive to real workload patterns
//  4. Handles bursty traffic gracefully
//  5. Compatible with model architectures like MoE, HSSM
//
// Queueing theory mode (experimental) provides:
//   - Multi-variant global optimization
//   - Cost-aware allocation
//   - Performance prediction
//
// # Usage Example
//
//	import (
//	    "github.com/llm-d-incubation/workload-variant-autoscaler/internal/saturation"
//	    "github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
//	)
//
//	// Create analyzer
//	analyzer := saturation.NewAnalyzer()
//
//	// Configure thresholds
//	config := interfaces.SaturationScalingConfig{
//	    SaturationThreshold:  70.0,
//	    SaturationTrigger:    50.0,
//	    ScaleDownThreshold:   30.0,
//	}
//
//	// Collect replica metrics
//	replicaMetrics := []interfaces.ReplicaMetrics{
//	    {ReplicaName: "llama-8b-0", KVCacheUtilization: 75.0, QueueDepth: 5},
//	    {ReplicaName: "llama-8b-1", KVCacheUtilization: 68.0, QueueDepth: 3},
//	    {ReplicaName: "llama-8b-2", KVCacheUtilization: 55.0, QueueDepth: 2},
//	}
//
//	// Analyze
//	analysis, err := analyzer.AnalyzeModelSaturation(ctx, modelID, namespace, replicaMetrics, config)
//	if err != nil {
//	    // Handle error
//	}
//
//	// Make decision
//	if analysis.ShouldScaleUp {
//	    desiredReplicas = currentReplicas + 1
//	}
//
// See also:
//   - internal/interfaces/saturation_analyzer.go: SaturationAnalyzer interface
//   - internal/collector: Metrics collection
//   - docs/saturation-scaling-config.md: Configuration guide
//   - docs/saturation-analyzer.md: Algorithm details
package saturation
