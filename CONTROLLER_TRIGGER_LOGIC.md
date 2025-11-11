# Controller Trigger Logic and Reconciliation Behavior

This document describes all triggers that cause the VariantAutoscaling controller to reconcile, and what happens during each reconciliation.

## Overview: Global Optimization Pattern

**IMPORTANT**: This controller uses a **global optimization pattern** where:
- Every reconciliation processes **ALL** VariantAutoscaling resources together
- The `req ctrl.Request` parameter (which contains the specific resource that triggered reconciliation) is **NOT USED**
- All VAs are optimized as a group to find the cost-optimal allocation across all models and variants

This means:
- Creating/updating 1 VA → reconciles ALL VAs
- Changing 1 ConfigMap → reconciles ALL VAs
- 60-second timer → reconciles ALL VAs

---

## Trigger 1: Periodic Reconciliation (60-Second Loop)

### When It Happens
- Every 60 seconds (default) after the previous reconciliation completes
- Interval configurable via `GLOBAL_OPT_INTERVAL` in `variantautoscaling-config` ConfigMap

### How It's Implemented
**File**: `internal/controller/variantautoscaling_controller.go:353`

```go
return ctrl.Result{RequeueAfter: requeueDuration}, nil
```

Every successful reconciliation returns `RequeueAfter: requeueDuration` which tells Kubernetes to trigger reconciliation again after the specified duration.

**Interval Parsing** (line 135-146):
```go
interval, err := r.readOptimizationConfig(ctx)
if err != nil {
    logger.Log.Error(err, "Unable to read optimization config, using default interval")
    // Don't fail reconciliation - use default and continue
} else if interval != "" {
    if parsedDuration, parseErr := time.ParseDuration(interval); parseErr != nil {
        logger.Log.Error(parseErr, "Invalid reconciliation interval format, using default",
            "configured", interval,
            "default", requeueDuration.String())
    } else {
        requeueDuration = parsedDuration
    }
}
```

### What Happens
1. Lists **ALL** VAs across **ALL** namespaces (line 177)
2. Filters out VAs marked for deletion (line 182)
3. Processes all active VAs together:
   - Validates deployments exist
   - Collects Prometheus metrics
   - Runs global optimization
   - Updates all VA statuses
   - Actuates deployment replicas
4. Returns with `RequeueAfter: requeueDuration` to schedule next run

### Configuration
```yaml
# config/manager/configmap.yaml
data:
  GLOBAL_OPT_INTERVAL: "60s"  # Change this to adjust reconciliation interval
```

Valid formats: `"30s"`, `"1m"`, `"2m30s"`, etc.

---

## Trigger 2: VariantAutoscaling Resource Creation

### When It Happens
- A new VariantAutoscaling resource is created via `kubectl apply`

### How It's Implemented
**File**: `internal/controller/variantautoscaling_controller.go:1654-1656`

```go
CreateFunc: func(e event.CreateEvent) bool {
    return true  // Always reconcile on VA creation
},
```

### What Happens
1. **Trigger**: New VA created → reconciliation triggered
2. **Request Enqueued**: `ctrl.Request{NamespacedName: {Name: "new-va", Namespace: "default"}}`
3. **Reconcile Called**: BUT the `req` parameter is **IGNORED**
4. **Lists ALL VAs**: Including the newly created one (line 177)
5. **Processes ALL VAs together**: Global optimization runs for all VAs
6. **Result**:
   - New VA gets optimized allocation
   - All other VAs may also get updated allocations (cost optimization across all variants)
   - Returns `RequeueAfter: 60s` for next periodic reconciliation

### Example
```bash
kubectl apply -f - <<EOF
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: llama-3-1-8b-a100
  namespace: default
spec:
  modelID: "meta/llama-3.1-8b"
  variantID: "meta/llama-3.1-8b-A100-1"
  accelerator: "A100"
  acceleratorCount: 1
  scaleTargetRef:
    kind: Deployment
    name: vllm-llama-deployment
  # ... other fields
EOF

# Result: Controller reconciles ALL VAs in ALL namespaces
```

---

## Trigger 3: VariantAutoscaling Resource Update

### When It Happens
- A VariantAutoscaling resource's **spec** is modified
- **Note**: Status-only updates do NOT trigger reconciliation

### How It's Implemented
**File**: `internal/controller/variantautoscaling_controller.go:1657-1665`

```go
UpdateFunc: func(e event.UpdateEvent) bool {
    // Reconcile only if spec changed (ignore status-only updates)
    oldVA, oldOK := e.ObjectOld.(*llmdVariantAutoscalingV1alpha1.VariantAutoscaling)
    newVA, newOK := e.ObjectNew.(*llmdVariantAutoscalingV1alpha1.VariantAutoscaling)
    if !oldOK || !newOK {
        return false
    }
    return !reflect.DeepEqual(oldVA.Spec, newVA.Spec)  // Only spec changes
},
```

### Spec Changes That Trigger Reconciliation
- `modelID` changed
- `variantID` changed
- `accelerator` changed
- `acceleratorCount` changed
- `minReplicas` changed
- `maxReplicas` changed
- `variantCost` changed
- `scaleTargetRef` changed
- `sloClassRef` changed
- `variantProfile` changed

### Status Changes That Do NOT Trigger
- `status.currentAlloc.numReplicas` changed
- `status.desiredOptimizedAlloc` changed
- `status.conditions` changed
- `status.actuation.applied` changed

### What Happens
Same as VA creation - reconciles **ALL VAs**, not just the updated one.

### Example
```bash
# Change minReplicas for one VA
kubectl patch va llama-3-1-8b-a100 -n default --type merge -p '{"spec":{"minReplicas":2}}'

# Result: Controller reconciles ALL VAs in ALL namespaces
# The change to minReplicas=2 might affect optimal allocation for OTHER variants too!
```

---

## Trigger 4: VariantAutoscaling Resource Deletion

### When It Happens
- A VariantAutoscaling resource is deleted via `kubectl delete`

### How It's Implemented
**File**: `internal/controller/variantautoscaling_controller.go:1666-1669`

```go
DeleteFunc: func(e event.DeleteEvent) bool {
    // Don't reconcile on delete (handled by deletion timestamp check)
    return false
},
```

### What Happens
**No immediate reconciliation triggered!**

However:
1. Deletion sets `metadata.deletionTimestamp` on the VA
2. Next periodic reconciliation (60s timer) picks up the change
3. VA is filtered out by `filterActiveVariantAutoscalings` (line 357-367):
   ```go
   func filterActiveVariantAutoscalings(items []VariantAutoscaling) []VariantAutoscaling {
       active := make([]VariantAutoscaling, 0, len(items))
       for _, va := range items {
           if va.DeletionTimestamp.IsZero() {
               active = append(active, va)
           } else {
               logger.Log.Info("skipping deleted variantAutoscaling", "name", va.Name)
           }
       }
       return active
   }
   ```
4. Deleted VA excluded from optimization
5. Other VAs re-optimized without it

**Implication**: Deleted VAs are excluded from optimization within 60 seconds (or next reconciliation trigger).

---

## Trigger 5: ConfigMap Change (Optimization Config)

### When It Happens
- The `workload-variant-autoscaler-variantautoscaling-config` ConfigMap is created/updated/deleted in `workload-variant-autoscaler-system` namespace

### How It's Implemented
**File**: `internal/controller/variantautoscaling_controller.go:1626-1638`

```go
Watches(
    &corev1.ConfigMap{},
    handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
        if obj.GetName() == configMapName && obj.GetNamespace() == configMapNamespace {
            return enqueueAllVAs(ctx, obj)  // Enqueues ALL VAs
        }
        return nil
    }),
    builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
        return obj.GetName() == configMapName && obj.GetNamespace() == configMapNamespace
    })),
).
```

### ConfigMap Details
- **Name**: `workload-variant-autoscaler-variantautoscaling-config` (hardcoded, line 118)
- **Namespace**: `workload-variant-autoscaler-system` (hardcoded, line 119)
- **Key Watched**: `GLOBAL_OPT_INTERVAL`

### What Gets Enqueued
**File**: `internal/controller/variantautoscaling_controller.go:1609-1631`

```go
enqueueAllVAs := func(ctx context.Context, obj client.Object) []reconcile.Request {
    var list llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
    if err := r.List(ctx, &list); err != nil {
        logger.Log.Error(err, "Failed to list VariantAutoscalings for ConfigMap watch")
        return nil
    }

    if len(list.Items) == 0 {
        logger.Log.Info("No VariantAutoscalings found, skipping ConfigMap reconciliation",
            "configMap", obj.GetName())
        return nil
    }

    // Return only ONE request to trigger global reconciliation.
    // The specific VA used as trigger is arbitrary since Reconcile() processes ALL VAs.
    logger.Log.Info("ConfigMap changed, triggering global reconciliation",
        "configMap", obj.GetName(),
        "totalVAs", len(list.Items),
        "trigger", list.Items[0].Name)

    return []reconcile.Request{
        {NamespacedName: client.ObjectKeyFromObject(&list.Items[0])},
    }
}
```

### What Happens
1. ConfigMap updated → Lists ALL VAs → Enqueues **SINGLE** reconcile request
2. If there are 10 VAs → **1 reconcile request** enqueued (using first VA as trigger)
3. That single reconciliation processes ALL VAs (efficient!)
4. **Optimization interval changes** take effect on the NEXT reconciliation

**Performance**: ✅ **Fixed** - Previously enqueued N requests, now enqueues only 1 request.

### Example
```bash
kubectl edit configmap workload-variant-autoscaler-variantautoscaling-config \
  -n workload-variant-autoscaler-system

# Change GLOBAL_OPT_INTERVAL from "60s" to "30s"

# Result:
# - Single reconciliation triggered (not 10 reconciliations!)
# - That reconciliation processes all 10 VAs once
# - New 30s interval takes effect after reconciliation completes
# - 90% reduction in redundant work!
```

---

## Trigger 6: ConfigMap Change (Scale-to-Zero Config)

### When It Happens
- The `model-scale-to-zero-config` ConfigMap is created/updated/deleted in `workload-variant-autoscaler-system` namespace

### How It's Implemented
**File**: `internal/controller/variantautoscaling_controller.go:1639-1651`

```go
Watches(
    &corev1.ConfigMap{},
    handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
        if obj.GetName() == "model-scale-to-zero-config" && obj.GetNamespace() == configMapNamespace {
            return enqueueAllVAs(ctx, obj)  // Enqueues ALL VAs
        }
        return nil
    }),
    builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
        return obj.GetName() == "model-scale-to-zero-config" && obj.GetNamespace() == configMapNamespace
    })),
).
```

### ConfigMap Details
- **Name**: `model-scale-to-zero-config` (hardcoded)
- **Namespace**: `workload-variant-autoscaler-system` (hardcoded, line 119)
- **Keys Watched**:
  - `__defaults__` (global defaults)
  - `model.<safe-key>` (per-model configs)

### What Happens
Same efficiency as optimization config (fixed):
1. ConfigMap updated → Enqueues **SINGLE** reconciliation (not N reconciliations)
2. That reconciliation reads new scale-to-zero config (line 170-174)
3. Scale-to-zero decisions updated for all affected models

**Performance**: ✅ **Fixed** - Uses same `enqueueAllVAs` function, so only 1 reconciliation triggered.

### Example
```bash
kubectl patch configmap model-scale-to-zero-config \
  -n workload-variant-autoscaler-system \
  --type merge -p '{
    "data": {
      "model.meta.llama-3.1-8b": "modelID: \"meta/llama-3.1-8b\"\nenableScaleToZero: true\nretentionPeriod: \"5m\""
    }
  }'

# Result:
# - Single reconciliation triggered (efficient!)
# - All VAs processed once (including those not using this model)
# - VAs using meta/llama-3.1-8b get new 5-minute retention period
# - Scale-to-zero logic updated in optimizer
```

---

## Triggers That Do NOT Cause Reconciliation

### 1. Deployment Changes
The controller does **NOT** watch Deployment resources. Changes to deployments (replicas, image, etc.) do not trigger reconciliation.

**Why**: Controller only cares about current replica count, which it reads during reconciliation from `deployment.Spec.Replicas`.

**Implication**: If deployment replicas are manually changed, controller will detect this on next periodic reconciliation (60s) and may override it.

### 2. HPA Changes
The controller does **NOT** watch HorizontalPodAutoscaler resources.

**Integration**: HPA reads `wva_desired_replicas` metric from Prometheus, which the controller emits during each reconciliation.

### 3. Pod Changes
Pod creation/deletion/updates do **NOT** trigger reconciliation.

### 4. ServiceMonitor Changes
ServiceMonitor changes do **NOT** trigger reconciliation, but affect metrics availability.

---

## Reconciliation Flow (What Happens Every Time)

Regardless of trigger, every reconciliation follows this flow:

### 1. Read Configuration (lines 135-174)
```go
// Read reconciliation interval
interval, err := r.readOptimizationConfig(ctx)

// Read accelerator configuration (required)
acceleratorCm, err := r.readAcceleratorConfig(ctx, "accelerator-unit-costs", configMapNamespace)

// Read service class configuration (required)
serviceClassCm, err := r.readServiceClassConfig(ctx, "service-classes-config", configMapNamespace)

// Read scale-to-zero configuration (optional)
scaleToZeroConfigData, err := r.readScaleToZeroConfig(ctx, "model-scale-to-zero-config", configMapNamespace)
```

### 2. List ALL VAs (line 177)
```go
var variantAutoscalingList llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
if err := r.List(ctx, &variantAutoscalingList); err != nil {
    logger.Log.Error(err, "unable to list variantAutoscaling resources")
    return ctrl.Result{}, err
}
```

**No filters applied** - lists ALL VAs across ALL namespaces.

### 3. Filter Active VAs (line 182)
```go
activeVAs := filterActiveVariantAutoscalings(variantAutoscalingList.Items)
```

Removes VAs with non-zero `deletionTimestamp`.

### 4. Early Exit if No VAs (line 184-187)
```go
if len(activeVAs) == 0 {
    logger.Log.Info("No active VariantAutoscalings found, skipping optimization")
    return ctrl.Result{}, nil  // NO periodic requeue!
}
```

**Note**: If no VAs exist, reconciliation does NOT requeue itself. Controller becomes idle until next VA created or ConfigMap changed.

### 5. Prepare Variant Data (line 264)
For each active VA:
- Check modelID not empty
- Lookup SLO configuration
- Get Deployment (via `scaleTargetRef.name`)
- **Get fresh VA from API server** (using `va.Name` - **BUG FIXED!**)
- Set owner reference (Deployment owns VA)
- Validate metrics availability
- Collect allocation metrics from Prometheus
- Collect aggregate metrics (load, TTFT, ITL)
- Add variant to system data for optimization

VAs with errors are added to `updateList` for fallback allocation but excluded from optimization.

### 6. Run Global Optimization (lines 271-304)
```go
system := inferno.NewSystem()
optimizerSpec := system.SetFromSpec(&systemData.Spec)
optimizer := infernoSolver.NewOptimizerFromSpec(optimizerSpec)
manager := infernoManager.NewManager(system, optimizer)

// ... analyze models ...

engine := variantAutoscalingOptimizer.NewVariantAutoscalingsEngine(manager, system)
optimizedAllocation, err := engine.Optimize(ctx, *updateList, allAnalyzerResponses, &scaleToZeroConfigData, r.ScaleToZeroMetricsCache)
```

Finds cost-optimal allocation across all variants considering:
- Current load
- SLO requirements (TTFT, ITL)
- Variant costs
- Min/max replica bounds
- Scale-to-zero configuration

### 7. Apply Optimized Allocations (line 346)
For each VA in updateList:
- Apply replica bounds (min/max)
- Apply scale-to-zero logic
- Update VA status
- Emit Prometheus metrics
- Actuate deployment replicas (if changed)

### 8. Return with Requeue (line 353)
```go
return ctrl.Result{RequeueAfter: requeueDuration}, nil
```

Schedules next periodic reconciliation.

---

## Performance Characteristics

### Reconciliation Efficiency (After ConfigMap Fix)

**With 10 VAs:**
- Creating 1 VA → 1 reconciliation processing 10 VAs
- Updating 1 VA → 1 reconciliation processing 10 VAs
- ConfigMap change → **1 reconciliation** processing 10 VAs ✅
- Periodic timer (60s) → 1 reconciliation processing 10 VAs

**With 100 VAs:**
- Creating 1 VA → 1 reconciliation processing 100 VAs
- ConfigMap change → **1 reconciliation** processing 100 VAs ✅
- Total work: 100 operations (not 10,000!) ✅

**Performance Improvement**: ConfigMap changes now trigger only 1 reconciliation instead of N reconciliations (99% reduction in redundant work).

### Why This Design?

The global optimization pattern is necessary because:
- Cost optimization requires comparing all variants
- Scale-to-zero decisions depend on "cheapest variant" across all VAs
- Cluster-wide resource allocation decisions

The ConfigMap watch has been optimized to enqueue only a single reconciliation request instead of one per VA.

---

## Summary Table

| Trigger | Reconciliation Triggered? | Processes ALL VAs? | Periodic Requeue? |
|---------|---------------------------|--------------------|--------------------|
| **Periodic timer (60s)** | ✅ Yes (1 reconciliation) | ✅ Yes | ✅ Yes |
| **VA created** | ✅ Yes (1 reconciliation) | ✅ Yes | ✅ Yes |
| **VA spec updated** | ✅ Yes (1 reconciliation) | ✅ Yes | ✅ Yes |
| **VA status updated** | ❌ No (filtered out) | N/A | N/A |
| **VA deleted** | ❌ No (handled by timer) | N/A | N/A |
| **Optimization ConfigMap changed** | ✅ Yes (1 reconciliation) ✅ FIXED | ✅ Yes | ✅ Yes |
| **Scale-to-zero ConfigMap changed** | ✅ Yes (1 reconciliation) ✅ FIXED | ✅ Yes | ✅ Yes |
| **Deployment changed** | ❌ No | N/A | N/A |
| **HPA changed** | ❌ No | N/A | N/A |
| **Pod changed** | ❌ No | N/A | N/A |

---

## Configuration Reference

### Reconciliation Interval
```yaml
# config/manager/configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: variantautoscaling-config
  namespace: workload-variant-autoscaler-system
data:
  GLOBAL_OPT_INTERVAL: "60s"  # Default: 60s, Range: 10s-10m
```

### Controller Constants
```go
// internal/controller/variantautoscaling_controller.go

const (
    configMapName      = "workload-variant-autoscaler-variantautoscaling-config"
    configMapNamespace = "workload-variant-autoscaler-system"
)

const (
    DefaultReconciliationInterval = 60 * time.Second
    MinReconciliationInterval     = 10 * time.Second
    MaxReconciliationInterval     = 10 * time.Minute
)
```

---

## Debugging Tips

### Check Reconciliation Activity
```bash
# Watch controller logs
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -f | grep "Reconcile"
```

### Check Current Reconciliation Interval
```bash
kubectl get configmap variantautoscaling-config \
  -n workload-variant-autoscaler-system \
  -o jsonpath='{.data.GLOBAL_OPT_INTERVAL}'
```

### Force Immediate Reconciliation
```bash
# Trigger by updating any VA spec (even a label change)
kubectl annotate va <va-name> force-reconcile="$(date +%s)"
```

### Count VAs Being Processed
```bash
kubectl get va --all-namespaces --no-headers | wc -l
```

Each reconciliation processes ALL of these VAs together.

### Verify ConfigMap Change Efficiency
```bash
# Watch for ConfigMap-triggered reconciliations
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -f | grep "ConfigMap changed, triggering global reconciliation"

# Expected output after ConfigMap update:
# ConfigMap changed, triggering global reconciliation
#   configMap=variantautoscaling-config totalVAs=10 trigger=<first-va-name>
#
# Should see ONLY 1 message per ConfigMap update (not N messages for N VAs)
```
