# Saturation Analyzer

## Overview

The Saturation Analyzer is a **fast, reactive, and safe saturation guardrail** that prevents capacity exhaustion by monitoring live vLLM metrics.

**Key Features:**
- ✅ Operates from live vLLM metrics (no offline profiling required)
- ✅ Detects imminent capacity exhaustion (KV-cache or request queue)
- ✅ Makes **per-variant** target replica calculations with cost-awareness
- ✅ Uses ready replicas (those reporting metrics) to avoid excessive scale-up
- ✅ **Prevents cascade scaling** by blocking scale-up when replicas are pending
- ✅ Analyzes capacity across all variants of the same model

## Architecture

### Components

**1. Saturation Analyzer**
- Core analysis logic for saturation-based scaling decisions
- Implements spare capacity calculations
- Performs worst-case scale-down safety simulation
- Makes **per-variant** scaling decisions with cost-awareness

**2. Metrics Collector**
- Collects vLLM metrics from Prometheus using peak values over 1 minute
- Queries KV cache usage and request queue metrics
- Uses peak values for safety-first capacity analysis
- Enriches metrics with pod metadata (variant name, accelerator type)

**3. Interfaces**
- Defines data structures for replica metrics (including variant cost)
- Defines analysis results and per-variant decision types
- Provides interface for capacity analysis

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
│ AnalyzeModelCapacity     │  ← CapacityScalingConfig
└────────┬─────────────────┘
         │ ModelCapacityAnalysis (with per-variant breakdown)
         ↓
┌───────────────────────────────────┐
│ Calculate Capacity Targets        │  ← VariantReplicaState[]
│ - Cost-aware variant selection    │
└────────┬──────────────────────────┘
         │ Capacity Targets: map[variantName]targetReplicas
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

## Decision Logic

### Calculate Capacity Targets

For each variant, determines target replicas based on **capacity needs**:

| Condition | Target Replicas | Rationale |
|-----------|----------------|-----------|
| Capacity needs scale-up | **Cheapest** variant: readyReplicas + 1 | Cost-optimized capacity expansion (deterministic: alphabetically first variant on tie) |
| Capacity allows scale-down | **Most expensive** variant: readyReplicas - 1 | Cost-optimized capacity reduction (deterministic: alphabetically last variant on tie) |
| Otherwise | target = readyReplicas | No capacity action needed |

**Note:** `readyReplicas` = number of replicas reporting capacity metrics. This prevents excessive scale-up when replicas are still starting up.

**Cascade Scaling Prevention:** Variants with pending replicas (pods that exist but are not yet ready) are skipped during scale-up selection. This prevents the controller from repeatedly scaling up the same variant while previous scale-up operations are still in progress. Pod startup can take 2-7 minutes depending on model size and hardware (container initialization, model loading, health checks).

**Example:**
```
Model: llama-70b
Variants:
  - v1-l4 (cost=$5): current=2, ready=2 → target=3 (cheapest, scaled up for capacity)
  - v2-a100 (cost=$20): current=4, ready=3 → target=3 (most expensive, stays at ready count)

Note: v2-a100 has 4 current replicas but only 3 are ready (reporting metrics).
```

**Key Principles:**
1. **Ready replicas only**: Use replicas reporting metrics to avoid scaling up for not-yet-ready pods
2. **Cost-aware selection**: Cheapest variant for scale-up, most expensive for scale-down
3. **Deterministic tie-breaking**: When variants have equal costs, alphabetically first for scale-up, last for scale-down
4. **Pending replica awareness**: Skip variants with pending replicas during scale-up to prevent cascade scaling

## Multi-Variant Analysis

The saturation analyzer aggregates metrics **across all variants of the same model**:

**Example: Model "llama-70b" with 2 variants**
- variant-1 (A100, cost: $20, 2 replicas)
- variant-2 (H100, cost: $15, 3 replicas)

The analyzer aggregates across all 5 replicas and provides per-variant breakdown with cost information.

**Decision example:**
- If capacity needs scale-up: variant-2 (H100) will be scaled up (cheaper at $15 vs $20)
- If capacity allows scale-down: variant-1 (A100) will be scaled down (more expensive at $20)

## Configuration

### Cascade Scaling Prevention

**Problem:** Without pending replica awareness, the saturation analyzer could repeatedly trigger scale-up for the same variant before previous scale-up operations complete, leading to excessive replica counts.

**Timeline Example (Without Protection):**
```
T+0s:  Saturation detected → Scale up variant-1 from 2 to 3 replicas
T+30s: New pod created but not ready yet (still loading model)
       Saturation still detected (only 2 ready replicas) → Scale up to 4 replicas
T+60s: Both new pods still starting, saturation persists → Scale up to 5 replicas
T+90s: All 5 pods now ready, but we have 3 extra replicas (over-provisioned)
```

**Solution:** WVA tracks **pending replicas** (total pods minus ready pods) per variant and skips variants with pending replicas during scale-up selection.

**How It Works:**
1. **Replica State Tracking**: Controller maintains variant state with:
   - Total pods (from Deployment)
   - Pods that exist but aren't ready (pending replicas)

2. **Scale-Up Selection**: When saturation triggers scale-up:
   - Skip variants with pending replicas (wait for them to become ready)
   - Select the cheapest eligible variant

3. **Per-Variant Tracking**: Each variant is tracked independently. If variant-1 has pending replicas, variant-2 can still scale up if it's the cheapest eligible variant.

**Timeline Example (With Protection):**
```
T+0s:  Saturation detected → Scale up variant-1 from 2 to 3 (pending=1)
T+30s: Saturation still detected, but variant-1 skipped (has 1 pending replica)
       If variant-2 is cheaper and has no pending replicas → Scale up variant-2
T+90s: variant-1 pod becomes ready (pending=0), now eligible for scale-up again
```

**Benefits:**
- ✅ Prevents excessive scale-up during model loading periods (2-7 minutes)
- ✅ Reduces infrastructure costs by avoiding over-provisioning
- ✅ Maintains cost-optimized scaling across multiple variants

**Note:** Scale-down operations are not affected by pending replicas, as removing capacity is always safe when replicas are starting up.

Saturation scaling thresholds are configured via ConfigMap (see [saturation-scaling-config.md](saturation-scaling-config.md)):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: capacity-scaling-config
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

**Capacity target calculation:**
```
INFO Capacity target: scale-up cheapest variant
  variant=v1-l4
  cost=5.00
  currentReplicas=2
  readyReplicas=2
  target=3
  reason=KV spare capacity low
```

## Performance Characteristics

### Computational Complexity

- **Per-replica analysis:** O(N) where N = number of replicas
- **Variant aggregation:** O(V) where V = number of variants
- **Overall:** O(N + V), typically O(N) as V << N

### Prometheus Queries

**Two queries per model:**
1. KV cache usage with peak values over 1 minute
2. Request queue length with peak values over 1 minute

**Query strategy:** Uses peak capacity usage in the last minute, providing conservative safety-first analysis that prevents missing saturation events between queries. Metrics are scoped to the specific model being analyzed, preventing cross-model metric pollution.

**Query frequency:** Once per reconciliation loop (typically every 60s)

## Integration Notes

### Controller Integration

The saturation analyzer is integrated into the controller's reconciliation loop:

1. **Collect metrics** for all pods of a model (across all variants)
   - Enrich with cost from CRD spec (default: 10)

2. **Analyze capacity** across all variants
   - Aggregates metrics across all variants
   - Produces capacity analysis with per-variant breakdown and cost

3. **Build variant states** with current replica counts
   - Current replicas: from actual pod count
   - Pending replicas: pods that exist but aren't ready yet

4. **Calculate capacity targets**
   - Uses cost-based selection (cheapest/most expensive) for capacity actions
   - Returns target replicas per variant

5. **Apply final decisions** per variant
   - Scale each variant to its target replicas

### Metrics Requirements

The analyzer requires these Prometheus metrics from vLLM:
- KV cache utilization (0.0-1.0 range)
- Queue length (integer)

These metrics must include the following labels:
- Pod identification
- Model identification (to prevent cross-model metric pollution)
- Kubernetes namespace

### CRD Requirements

The analyzer requires the following field from the CRD spec:
- **cost** (float64, optional): Cost per replica for this variant (default: 10)
  - Used for cost-aware variant selection
  - Cheapest variant scaled up, most expensive scaled down

## Limitations

1. **Minimum replicas:** Scale-down requires at least 2 non-saturated replicas for safety simulation; variants cannot be scaled below 1 replica
2. **Metric availability:** Assumes vLLM metrics are available in Prometheus
3. **Pod identification:** Requires pod and model_id labels in Prometheus metrics
4. **No model profiling:** Does not account for model-specific capacity curves
5. **Cost field:** Currently uses constant default value (10.0) if not specified in CRD

## Future Enhancements

Potential improvements:
- Per-accelerator type threshold overrides
- Historical capacity trend analysis
- Predictive capacity planning
- Integration with Inference Scheduler thresholds
- Metric-based cache invalidation

## References
- Related: [Saturation Scaling Configuration](saturation-scaling-config.md)

