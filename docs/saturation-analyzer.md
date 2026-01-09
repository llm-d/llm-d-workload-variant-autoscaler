# Saturation Analyzer

## Overview

The Saturation Analyzer is a **fast, reactive, and safe saturation guardrail** that prevents capacity exhaustion by monitoring live vLLM metrics. It uses a **two-step decision architecture**:

**Step 1: Calculate Capacity Targets** - Pure saturation-based target replicas per variant
**Step 2: Arbitrate with Model-Based Optimizer** (optional) - Hybrid decision matrix

**Key Features:**
- ✅ Operates from live vLLM metrics (no offline profiling required)
- ✅ Detects imminent capacity exhaustion (KV-cache or request queue)
- ✅ Makes **per-variant** target replica calculations with cost-awareness
- ✅ Uses ready replicas (those reporting metrics) to avoid excessive scale-up
- ✅ Preserves desired replicas from previous optimizer runs (in Step 1)
- ✅ Arbitrates with model-based optimizer targets (in Step 2) using safety overrides
- ✅ Analyzes capacity across all variants of the same model

## Architecture

### Components

**1. Saturation Analyzer (`internal/saturation/analyzer.go`)**
- Core analysis logic for saturation-based scaling decisions
- Implements spare capacity calculations
- Performs worst-case scale-down safety simulation
- Makes **per-variant** scaling decisions with cost-awareness
- Supports saturation-only mode and hybrid mode with model-based optimization

**2. Metrics Collector (`internal/engines/saturation/metrics/replica_metrics.go`)**
- Collects vLLM metrics from Prometheus using the v2 collector infrastructure
- Uses `max_over_time[1m]` queries via registered query templates
- Queries `constants.VLLMKvCacheUsagePerc` and `constants.VLLMNumRequestsWaiting`
- Uses peak values over 1 minute for safety-first saturation analysis
- Enriches metrics with pod metadata (variant name, accelerator type, cost)
- Filters stale metrics (default: 2 minutes) to avoid data from terminated pods

**3. Interfaces (`internal/interfaces/saturation_analyzer.go`)**
- Defines data structures for replica metrics (including variant cost)
- Defines analysis results and per-variant decision types
- Provides interface for saturation analysis
- Defines `VariantDecision` for per-variant scaling decisions
- Defines `VariantReplicaState` for current/desired replica tracking

### Data Flow

```
┌─────────────┐
│  Prometheus │
└──────┬──────┘
       │ vLLM metrics (KV cache, queue length)
       ↓
┌──────────────────┐
│ MetricsCollector │
└────────┬─────────┘
         │ ReplicaMetrics[] (with cost)
         ↓
┌──────────────────────────┐
│ AnalyzeModelSaturation   │  ← SaturationScalingConfig
└────────┬─────────────────┘
         │ ModelSaturationAnalysis (with per-variant breakdown)
         ↓
┌─────────────────────────────┐
│ STEP 1: CalculateSaturationTargets│  ← VariantReplicaState[] (current/desired from CRD)
│ - Preserves desired replicas      │
│ - Cost-aware variant selection    │
└────────┬──────────────────────────┘
         │ Saturation Targets: map[variantName]targetReplicas
         ↓
   ┌─────┴──────────────────┐
   │                        │
   │ (if model-based        │
   │  optimizer available)  │
   ↓                        ↓
┌──────────────────────────────────┐
│ STEP 2: ArbitrateWithModelBased  │  ← ModelBased Targets: map[variantName]targetReplicas
│ - Hybrid decision matrix         │
│ - Capacity safety overrides      │
└────────┬─────────────────────────┘
         │ VariantDecision[] (one per variant)
         ↓
┌──────────────────┐
│    Controller    │
└──────────────────┘
```

## Analysis Algorithm

### Step 1: Identify Non-Saturated Replicas

A replica is **non-saturated** if:
```
kv_cache_usage < kvCacheThreshold AND queue_length < queueLengthThreshold
```

**Default thresholds:**
- `kvCacheThreshold`: 0.80 (80%)
- `queueLengthThreshold`: 5

### Step 2: Calculate Spare Capacity

For each non-saturated replica:
```
spare_kv_i = kvCacheThreshold - kv_cache_usage_i
spare_queue_i = queueLengthThreshold - queue_length_i
```

### Step 3: Average Spare Capacity

Across all non-saturated replicas:
```
avg_spare_kv = Σ spare_kv_i / N_non_sat
avg_spare_queue = Σ spare_queue_i / N_non_sat
```

### Step 4: Scale-Up Decision

Trigger scale-up if:
```
avg_spare_kv < kvSpareTrigger OR avg_spare_queue < queueSpareTrigger
```

**Default triggers:**
- `kvSpareTrigger`: 0.1 (10%)
- `queueSpareTrigger`: 3

### Step 5: Scale-Down Safety Simulation

Before allowing scale-down, simulate total load redistribution across remaining replicas:

```
remaining_replicas = N_non_sat - 1

// Calculate total load across all non-saturated replicas
total_kv_load = Σ kv_cache_usage_i (for all non-saturated replicas)
total_queue_load = Σ queue_length_i (for all non-saturated replicas)

// Simulate removing one replica: redistribute total load
avg_kv_after_removal = total_kv_load / remaining_replicas
avg_queue_after_removal = total_queue_load / remaining_replicas

// Calculate remaining spare capacity
remaining_spare_kv = kvCacheThreshold - avg_kv_after_removal
remaining_spare_queue = queueLengthThreshold - avg_queue_after_removal
```

**Scale-down is safe if:**
```
remaining_spare_kv >= kvSpareTrigger AND
remaining_spare_queue >= queueSpareTrigger AND
N_non_sat >= 2
```

## Two-Step Decision Logic

### Step 1: Calculate Saturation Targets

`CalculateSaturationTargets(saturationAnalysis, variantStates) → map[variantName]targetReplicas`

For each variant, determines target replicas based on **saturation needs only**:

| Condition | Target Replicas | Rationale |
|-----------|----------------|-----------|
| **desired ≠ 0 AND desired ≠ current** | target = **desired** | Preserve previous optimizer decision (from CRD status) |
| Saturation needs scale-up | **Cheapest** non-preserved variant: readyReplicas + 1 | Cost-optimized saturation expansion (deterministic: alphabetically first variant on tie) |
| Saturation allows scale-down | **Most expensive** non-preserved variant: readyReplicas - 1 | Cost-optimized saturation reduction (deterministic: alphabetically last variant on tie) |
| Otherwise | target = readyReplicas | No saturation action needed |

**Note:** `readyReplicas` = number of replicas reporting saturation metrics (from `VariantSaturationAnalysis.ReplicaCount`). This prevents excessive scale-up when replicas are still starting up.

**Example - Step 1 Output:**
```
Model: llama-70b
Variants:
  - v1-l4 (cost=$5): current=2, ready=2, desired=0 → target=3 (cheapest, scaled up for saturation)
  - v2-a100 (cost=$20): current=4, ready=3, desired=4 → target=4 (preserved desired)

Note: v2-a100 has 4 current replicas but only 3 are ready (reporting metrics).
      Target is set to desired=4 because desired ≠ current.
```

### Step 2: Arbitrate with Model-Based Optimizer (Optional)

`ArbitrateWithModelBased(saturationAnalysis, saturationTargets, modelBasedTargets, variantStates) → []VariantDecision`

Only runs when model-based optimizer provides per-variant targets. Applies hybrid decision matrix:

| Saturation Target | Model-Based Target | Final Decision | Reason |
|----------------|-------------------|----------------|--------|
| Scale-up (4) | Scale-down (2) | **No change** (veto) | Saturation veto: needs more capacity |
| No change (3) | Scale-down (2) | **No change** (block) if unsafe | Safety block: saturation analysis says unsafe |
| Scale-up (4) | No change (3) | **Scale-up to 4** | Saturation-driven: model-based doesn't object |
| No change (3) | Scale-up (5) | **Scale-up to 5** | Model-based-driven: saturation allows |
| No change (3) | Scale-down (2) | **Scale-down to 2** if safe | Model-based-driven: saturation approved |

**Key Principles:**
1. **Ready replicas only** (Step 1): Use replicas reporting metrics to avoid scaling up for not-yet-ready pods
2. **Preserve desired replicas** (Step 1): When desired ≠ current, always use desired as saturation target
3. **Cost-aware selection** (Step 1): Cheapest variant for scale-up, most expensive for scale-down
4. **Deterministic tie-breaking** (Step 1): When variants have equal costs, alphabetically first for scale-up, last for scale-down
5. **Saturation veto** (Step 2): Saturation needs override model-based scale-down suggestions
6. **Safety block** (Step 2): Unsafe scale-down blocked regardless of model-based recommendation
7. **Model-based priority** (Step 2): When saturation allows, follow model-based recommendations

## Usage Examples

### Complete Two-Step Flow

```go
import (
    "context"
    "github.com/llm-d-incubation/workload-variant-autoscaler/internal/saturation"
    saturationmetrics "github.com/llm-d-incubation/workload-variant-autoscaler/internal/engines/saturation/metrics"
    collectorv2 "github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/v2"
    "github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
)

// Create analyzer
analyzer := saturation.NewAnalyzer()

// Create metrics registry and collector
metricsRegistry := collectorv2.NewSourceRegistry()
promSource := metricsRegistry.Get("prometheus") // assumes prometheus source is registered
metricsCollector := saturationmetrics.NewReplicaMetricsCollector(promSource, k8sClient)

// Collect metrics (uses max_over_time[1m] for safety-first analysis)
// Note: Cost should be populated from CRD spec (default 10)
replicaMetrics, err := metricsCollector.CollectReplicaMetrics(ctx, modelID, namespace, deployments, variantAutoscalings, variantCosts)

// Get saturation config
config := interfaces.DefaultSaturationScalingConfig()

// Analyze saturation across all variants
analysis, err := analyzer.AnalyzeModelSaturation(ctx, modelID, namespace, replicaMetrics, config)

// Build variant states (current replicas from pod count, desired from CRD status)
variantStates := []interfaces.VariantReplicaState{
    {VariantName: "v1-l4", CurrentReplicas: 2, DesiredReplicas: 0},    // no previous optimizer run
    {VariantName: "v2-a100", CurrentReplicas: 3, DesiredReplicas: 4},  // optimizer wanted 4 last time
}

// === STEP 1: Calculate saturation-based targets ===
saturationTargets := analyzer.CalculateSaturationTargets(analysis, variantStates)

log.Printf("Saturation targets: %+v", saturationTargets)
// Output: map[v1-l4:3 v2-a100:4]
// - v1-l4: scaled to 3 (cheapest variant, saturation needs scale-up)
// - v2-a100: preserved at 4 (desired ≠ current)
```

### Step 1 Only: Saturation-Based Targets (No Model-Based Optimizer)

```go
// Calculate saturation targets
saturationTargets := analyzer.CalculateSaturationTargets(analysis, variantStates)

// Convert to decisions and apply
for variantName, target := range saturationTargets {
    state := getVariantState(variantName, variantStates) // helper function

    if target > state.CurrentReplicas {
        log.Printf("Scale-up %s: %d → %d", variantName, state.CurrentReplicas, target)
        // Apply scale-up
    } else if target < state.CurrentReplicas {
        log.Printf("Scale-down %s: %d → %d", variantName, state.CurrentReplicas, target)
        // Apply scale-down
    } else {
        log.Printf("No change for %s: %d", variantName, target)
    }
}
```

### Step 1 + Step 2: Hybrid Mode (With Model-Based Optimizer)

```go
// === STEP 1: Calculate saturation-based targets ===
saturationTargets := analyzer.CalculateSaturationTargets(analysis, variantStates)

// === Get model-based optimizer targets (from your optimizer) ===
modelBasedTargets := map[string]int{
    "v1-l4":   5,  // optimizer wants to scale up v1-l4
    "v2-a100": 2,  // optimizer wants to scale down v2-a100
}

// === STEP 2: Arbitrate between saturation and model-based ===
decisions := analyzer.ArbitrateWithModelBased(
    analysis,
    saturationTargets,
    modelBasedTargets,
    variantStates,
)

// Apply final decisions
for _, decision := range decisions {
    log.Printf("Variant: %s", decision.VariantName)
    log.Printf("  Current: %d", decision.CurrentReplicas)
    log.Printf("  Saturation target: %d", saturationTargets[decision.VariantName])
    log.Printf("  Model-based target: %d", modelBasedTargets[decision.VariantName])
    log.Printf("  Final target: %d", decision.TargetReplicas)
    log.Printf("  Reason: %s", decision.Reason)

    if decision.SafetyOverride {
        log.Printf("  ⚠️  Saturation safety override applied")
    }

    switch decision.Action {
    case interfaces.ActionScaleUp:
        // Apply scale-up to decision.TargetReplicas
    case interfaces.ActionScaleDown:
        // Apply scale-down to decision.TargetReplicas
    case interfaces.ActionNoChange:
        // No action needed
    }
}
```

## Multi-Variant Analysis

The saturation analyzer aggregates metrics **across all variants of the same model**:

```go
// Example: Model "llama-70b" with 2 variants
// - variant-1 (A100, cost: $20, 2 replicas)
// - variant-2 (H100, cost: $15, 3 replicas)

replicaMetrics := []interfaces.ReplicaMetrics{
    // Variant 1 (more expensive)
    {PodName: "v1-pod-1", VariantName: "variant-1", ModelID: "llama-70b",
     AcceleratorName: "A100", Cost: 20, KvCacheUsage: 0.70, QueueLength: 2},
    {PodName: "v1-pod-2", VariantName: "variant-1", ModelID: "llama-70b",
     AcceleratorName: "A100", Cost: 20, KvCacheUsage: 0.75, QueueLength: 3},

    // Variant 2 (cheaper)
    {PodName: "v2-pod-1", VariantName: "variant-2", ModelID: "llama-70b",
     AcceleratorName: "H100", Cost: 15, KvCacheUsage: 0.60, QueueLength: 1},
    {PodName: "v2-pod-2", VariantName: "variant-2", ModelID: "llama-70b",
     AcceleratorName: "H100", Cost: 15, KvCacheUsage: 0.65, QueueLength: 2},
    {PodName: "v2-pod-3", VariantName: "variant-2", ModelID: "llama-70b",
     AcceleratorName: "H100", Cost: 15, KvCacheUsage: 0.55, QueueLength: 1},
}

// Analyzer aggregates across all 5 replicas
analysis, _ := analyzer.AnalyzeModelSaturation(ctx, "llama-70b", "prod", replicaMetrics, config)

// Results include per-variant breakdown with cost
fmt.Printf("Total replicas: %d\n", analysis.TotalReplicas) // 5
fmt.Printf("Non-saturated: %d\n", analysis.NonSaturatedCount) // 5
fmt.Printf("Variants analyzed: %d\n", len(analysis.VariantAnalyses)) // 2

for _, va := range analysis.VariantAnalyses {
    fmt.Printf("Variant: %s, Replicas: %d, Accelerator: %s, Cost: %.2f\n",
        va.VariantName, va.ReplicaCount, va.AcceleratorName, va.Cost)
    // Note: va.ReplicaCount = ready replicas (those reporting metrics)
}

// If saturation needs scale-up and no optimizer guidance:
// → variant-2 (H100) will be scaled up (cheaper at $15 vs $20)
// → Target = readyReplicas + 1 (prevents excessive scale-up for not-yet-ready pods)
//
// If saturation allows scale-down in saturation-only mode:
// → variant-1 (A100) will be scaled down (more expensive at $20)
```

## Configuration

Saturation scaling thresholds are configured via ConfigMap (see [saturation-scaling-config.md](saturation-scaling-config.md)):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: saturation-scaling-config
  namespace: workload-variant-autoscaler-system
data:
  default: |
    kvCacheThreshold: 0.80
    queueLengthThreshold: 5
    kvSpareTrigger: 0.1
    queueSpareTrigger: 3
```

**Per-model overrides:**
```yaml
  llama-70b-prod: |
    model_id: meta/llama-70b
    namespace: production
    kvCacheThreshold: 0.85
    kvSpareTrigger: 0.15
```

## Testing

Comprehensive unit tests are provided in `internal/saturation/analyzer_test.go`:

```bash
cd internal/saturation
go test -v
```

**Test coverage:**
- ✅ Scale-up trigger conditions
- ✅ Scale-down safety simulation (total load redistribution)
- ✅ Multi-variant aggregation
- ✅ **Step 1: CalculateSaturationTargets**
  - ✅ Scale-up cheapest variant
  - ✅ Scale-down most expensive variant
  - ✅ Preserve desired replicas (desired ≠ current)
  - ✅ All variants preserved scenario
  - ✅ Equal costs with deterministic tie-breaking
  - ✅ Scale-down below minimum (prevents scaling to 0)
- ✅ **Step 2: ArbitrateWithModelBased**
  - ✅ Saturation veto (saturation wants up, model-based wants down)
  - ✅ Safety block (model-based wants down, but unsafe)
  - ✅ Follow model-based (when saturation allows)
  - ✅ Saturation-driven (saturation needs scale-up)
  - ✅ Both saturation and model-based agree
  - ✅ Nil analysis safety
- ✅ Saturated replica identification
- ✅ Edge cases (empty metrics, single replica, nil analysis)

## Observability

### Log Messages

**Saturation analysis:**
```
DEBUG Saturation analysis completed
  modelID=llama-70b
  namespace=prod
  totalReplicas=5
  nonSaturated=4
  avgSpareKv=0.150
  avgSpareQueue=2.5
  shouldScaleUp=true
  scaleDownSafe=false
```

**Scale-down safety:**
```
DEBUG Scale-down unsafe: insufficient headroom after redistribution
  remainingSpareKv=0.050
  kvTrigger=0.100
  kvSafe=false
  remainingSpareQueue=1.0
  queueTrigger=3
  queueSafe=false
```

**Decision arbitration (deprecated ArbitrateDecision):**
```
INFO Saturation decision arbitrated
  modelID=llama-70b
  namespace=prod
  currentReplicas=3
  modelBasedReplicas=2
  action=no-change
  targetReplicas=3
  reason=saturation veto: KV spare capacity low (model-based wanted scale-down to 2)
  safetyOverride=true
```

**Saturation target calculation (Step 1):**
```
INFO Saturation target: scale-up cheapest variant
  variant=v1-l4
  cost=5.00
  currentReplicas=2     (total replicas per CRD)
  readyReplicas=2       (replicas reporting metrics)
  target=3
  reason=KV spare capacity low
```

**Per-variant decision arbitration (Step 2):**
```
INFO Variant decision arbitrated
  variant=v1-l4
  current=2
  saturationTarget=3
  modelBasedTarget=2
  action=no-change
  targetReplicas=3
  reason=saturation veto: saturation needs scale-up (saturation=3, model-based=2)
```

## Performance Characteristics

### Computational Complexity

- **Per-replica analysis:** O(N) where N = number of replicas
- **Variant aggregation:** O(V) where V = number of variants
- **Overall:** O(N + V), typically O(N) as V << N

### Prometheus Queries

**Two queries per model:**
1. `max_over_time(constants.VLLMKvCacheUsagePerc{namespace="prod",model_id="llama-70b"}[1m])` (returns N samples with peak values)
2. `max_over_time(constants.VLLMNumRequestsWaiting{namespace="prod",model_id="llama-70b"}[1m])` (returns N samples with peak values)

**Query strategy:** Uses `max_over_time[1m]` to capture peak capacity usage in the last minute, providing conservative safety-first analysis that prevents missing saturation events between queries. The `model_id` filter ensures metrics are scoped to the specific model being analyzed, preventing cross-model metric pollution.

**Query frequency:** Once per reconciliation loop (typically every 60s)

## Integration Notes

### Controller Integration

The saturation analyzer is integrated into the controller's reconciliation loop using the two-step architecture:

1. **Collect metrics** for all pods of a model (across all variants)
   - Uses v2 collector infrastructure with registered query templates
   - Enriches with cost from CRD spec (default: 10)
   - Filters stale metrics (default: 2 minutes staleness threshold)

2. **Analyze saturation** using `AnalyzeModelSaturation`
   - Aggregates metrics across all variants
   - Produces `ModelSaturationAnalysis` with per-variant breakdown and cost

3. **Build variant states** with current and desired replicas
   - Current replicas: from actual pod count
   - Desired replicas: from CRD status field (previous optimizer run), 0 if not set

4. **STEP 1: Calculate saturation targets** using `CalculateSaturationTargets`
   - Preserves desired replicas when desired ≠ current
   - Uses cost-based selection (cheapest/most expensive) for saturation actions
   - Returns `map[variantName]targetReplicas`

5. **STEP 2: Arbitrate with model-based optimizer** (if available)
   - Get model-based targets from optimizer: `map[variantName]targetReplicas`
   - Call `ArbitrateWithModelBased` to apply hybrid decision matrix
   - Returns `[]VariantDecision` with safety overrides applied

6. **Apply final decisions** per variant
   - Scale each variant to its final target replicas

### Metrics Requirements

The analyzer requires these Prometheus metrics from vLLM (defined in `internal/constants/metrics.go`):
- `constants.VLLMKvCacheUsagePerc` (`vllm:kv_cache_usage_perc`) — KV cache utilization (0.0-1.0)
- `constants.VLLMNumRequestsWaiting` (`vllm:num_requests_waiting`) — Queue length (integer)

These metrics must include the following labels:
- `pod` or `pod_name` — Pod identification
- `model_id` — Model identification (to prevent cross-model metric pollution)
- `namespace` — Kubernetes namespace

### CRD Requirements

The analyzer requires two fields from the CRD:

**Spec fields:**
- `cost` (float64, optional): Cost per replica for this variant (default: 10)
  - Used for cost-aware variant selection in Step 1
  - Cheapest variant scaled up, most expensive scaled down

**Status fields:**
- `desiredReplicas` (int, optional): Target replicas from previous optimizer run
  - Set to 0 or omit if no optimizer has run yet
  - Used in Step 1 to preserve previous optimizer decisions
  - When `desired ≠ 0 AND desired ≠ current`: Step 1 sets `capacityTarget = desired`

## Limitations

1. **Minimum replicas:** Scale-down requires ≥2 non-saturated replicas for safety simulation; variants cannot be scaled below 1 replica
2. **Metric availability:** Assumes vLLM metrics are available in Prometheus
3. **Pod identification:** Requires pod and model_id labels in Prometheus metrics
4. **No model profiling:** Does not account for model-specific capacity curves
5. **Cost field:** Currently uses constant value (DefaultReplicaCost = 10.0); CRD integration pending

## Future Enhancements

Potential improvements:
- Per-accelerator type threshold overrides
- Historical capacity trend analysis
- Predictive capacity planning
- Integration with Inference Scheduler thresholds
- Metric-based cache invalidation

## References
- Related: [Saturation Scaling Configuration](saturation-scaling-config.md)

