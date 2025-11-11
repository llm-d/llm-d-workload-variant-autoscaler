# ConfigMap Reconciliation Issue and Solutions

## Problem Statement

### Current Behavior (Inefficient)

When a ConfigMap is updated:
1. Controller lists ALL VariantAutoscaling resources
2. Creates a separate reconcile request for **EACH VA**
3. Each reconciliation processes **ALL VAs** anyway (global optimization pattern)

**Example with 100 VAs:**
- ConfigMap updated → 100 reconcile requests enqueued
- Each request triggers reconciliation that processes all 100 VAs
- Total operations: **100 reconciliations × 100 VAs = 10,000 VA processing operations**
- **99 reconciliations are completely redundant!**

### Code Location

**File**: `internal/controller/variantautoscaling_controller.go:1606-1617`

```go
enqueueAllVAs := func(ctx context.Context, obj client.Object) []reconcile.Request {
    var list llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
    if err := r.List(ctx, &list); err != nil {
        logger.Log.Error(err, "Failed to list VariantAutoscalings for ConfigMap watch")
        return nil
    }
    requests := make([]reconcile.Request, len(list.Items))  // ❌ N requests!
    for i, va := range list.Items {
        requests[i] = reconcile.Request{
            NamespacedName: client.ObjectKeyFromObject(&va),
        }
    }
    return requests  // ❌ Returns N requests (one per VA)
}
```

### Impact

| VAs | ConfigMap Changes per Hour | Reconciliations | VA Processing Operations |
|-----|----------------------------|-----------------|-------------------------|
| 10 | 6 | 60 | 600 |
| 50 | 6 | 300 | 15,000 |
| 100 | 6 | 600 | 60,000 |
| 500 | 6 | 3,000 | 1,500,000 |

---

## Solution 1: Single Reconcile Request (Recommended)

### Description

Instead of enqueuing N requests (one per VA), enqueue just **ONE** reconcile request. Since reconciliation processes ALL VAs anyway, one trigger is sufficient.

### Implementation

**Option A: Use First VA as Trigger**

```go
enqueueAllVAs := func(ctx context.Context, obj client.Object) []reconcile.Request {
    var list llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
    if err := r.List(ctx, &list); err != nil {
        logger.Log.Error(err, "Failed to list VariantAutoscalings for ConfigMap watch")
        return nil
    }

    if len(list.Items) == 0 {
        logger.Log.Info("No VariantAutoscalings found, skipping ConfigMap reconciliation")
        return nil
    }

    // ✅ Return only ONE request (using first VA as sentinel)
    return []reconcile.Request{
        {NamespacedName: client.ObjectKeyFromObject(&list.Items[0])},
    }
}
```

**Option B: Use Sentinel VA Name**

```go
enqueueAllVAs := func(ctx context.Context, obj client.Object) []reconcile.Request {
    var list llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
    if err := r.List(ctx, &list); err != nil {
        logger.Log.Error(err, "Failed to list VariantAutoscalings for ConfigMap watch")
        return nil
    }

    if len(list.Items) == 0 {
        logger.Log.Info("No VariantAutoscalings found, skipping ConfigMap reconciliation")
        return nil
    }

    // ✅ Return single request with sentinel name
    // Note: Reconcile() ignores the specific VA name anyway and processes ALL VAs
    return []reconcile.Request{
        {NamespacedName: types.NamespacedName{
            Name:      "__configmap_trigger__",
            Namespace: list.Items[0].Namespace,
        }},
    }
}
```

Then update Reconcile to handle missing VA gracefully:

```go
func (r *VariantAutoscalingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Note: req parameter is not used - we always process ALL VAs
    // The req may contain a sentinel value from ConfigMap watch - this is expected

    // Read configuration...
    // List ALL VAs...
    // Process all VAs together...
}
```

### Pros
- ✅ Simple implementation (change ~3 lines of code)
- ✅ Reduces reconciliations from N to 1
- ✅ Reduces operations from N² to N
- ✅ No architectural changes needed
- ✅ Works with existing global optimization pattern

### Cons
- ❌ Slightly confusing that controller enqueues a specific VA but processes all VAs
- ❌ If no VAs exist, can't enqueue anything (ConfigMap change ignored until VA created)

### Performance Impact

| VAs | Current: ConfigMap Change | After Fix: ConfigMap Change |
|-----|---------------------------|----------------------------|
| 10 | 10 reconciliations → 100 operations | 1 reconciliation → 10 operations |
| 100 | 100 reconciliations → 10,000 operations | 1 reconciliation → 100 operations |
| 500 | 500 reconciliations → 250,000 operations | 1 reconciliation → 500 operations |

**Improvement: 99% reduction in operations!**

---

## Solution 2: Custom Workqueue with Deduplication

### Description

Use a custom workqueue that automatically deduplicates multiple reconcile requests into a single reconciliation.

### Implementation

```go
import (
    "k8s.io/client-go/util/workqueue"
)

func (r *VariantAutoscalingReconciler) SetupWithManager(mgr ctrl.Manager) error {
    // ... existing setup ...

    // Create custom workqueue with rate limiting
    rateLimiter := workqueue.NewItemExponentialFailureRateLimiter(
        5*time.Second,  // Base delay
        60*time.Second, // Max delay
    )

    return ctrl.NewControllerManagedBy(mgr).
        For(&llmdVariantAutoscalingV1alpha1.VariantAutoscaling{}).
        Watches(
            &corev1.ConfigMap{},
            handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
                if obj.GetName() == configMapName && obj.GetNamespace() == configMapNamespace {
                    // Enqueue ALL VAs, but workqueue will deduplicate
                    return enqueueAllVAs(ctx, obj)
                }
                return nil
            }),
            builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
                return obj.GetName() == configMapName && obj.GetNamespace() == configMapNamespace
            })),
        ).
        WithOptions(controller.Options{
            RateLimiter: rateLimiter,
            // MaxConcurrentReconciles: 1,  // Process one at a time
        }).
        Complete(r)
}
```

### Pros
- ✅ Kubernetes-native approach using workqueue
- ✅ Handles bursts of changes gracefully
- ✅ Rate limiting prevents API server overload

### Cons
- ❌ Still enqueues N requests (workqueue deduplicates, but adds overhead)
- ❌ More complex than Solution 1
- ❌ Requires understanding of workqueue mechanics
- ❌ Still inefficient compared to Solution 1

---

## Solution 3: Don't Watch ConfigMaps (Periodic Only)

### Description

Remove ConfigMap watches entirely. Let periodic reconciliation (60s) pick up configuration changes naturally.

### Implementation

```go
func (r *VariantAutoscalingReconciler) SetupWithManager(mgr ctrl.Manager) error {
    // ... existing setup ...

    return ctrl.NewControllerManagedBy(mgr).
        For(&llmdVariantAutoscalingV1alpha1.VariantAutoscaling{}).
        // ❌ Remove ConfigMap watches entirely
        Named("variantAutoscaling").
        WithEventFilter(predicate.Funcs{
            CreateFunc: func(e event.CreateEvent) bool {
                return true
            },
            UpdateFunc: func(e event.UpdateEvent) bool {
                oldVA, oldOK := e.ObjectOld.(*llmdVariantAutoscalingV1alpha1.VariantAutoscaling)
                newVA, newOK := e.ObjectNew.(*llmdVariantAutoscalingV1alpha1.VariantAutoscaling)
                if !oldOK || !newOK {
                    return false
                }
                return !reflect.DeepEqual(oldVA.Spec, newVA.Spec)
            },
            DeleteFunc: func(e event.DeleteEvent) bool {
                return false
            },
            GenericFunc: func(e event.GenericEvent) bool {
                return false
            },
        }).
        Complete(r)
}
```

### Pros
- ✅ Simplest solution - just remove code
- ✅ Zero reconciliations triggered by ConfigMap changes
- ✅ Reduces code complexity
- ✅ Reduces API server watch load

### Cons
- ❌ ConfigMap changes take up to 60 seconds to take effect (until next periodic reconciliation)
- ❌ No immediate feedback when operator updates configuration
- ❌ May be confusing for users (update ConfigMap, nothing happens immediately)

### When to Use
- Development/test environments where immediate updates not critical
- Clusters with many VAs where ConfigMap watch overhead is significant
- Environments where ConfigMaps change infrequently

---

## Solution 4: Global Reconciliation Trigger (Advanced)

### Description

Create a dummy "controller-wide" resource that represents global reconciliation. ConfigMap changes enqueue this single resource.

### Implementation

**Step 1: Create Singleton Resource**

```go
// Add to controller struct
type VariantAutoscalingReconciler struct {
    Client client.Client
    Scheme *runtime.Scheme
    // ... existing fields ...

    // Singleton namespace for global reconciliation triggers
    ControllerNamespace string
}

const GlobalReconcileTriggerName = "workload-variant-autoscaler-global-trigger"
```

**Step 2: Update ConfigMap Watch**

```go
enqueueGlobalReconcile := func(ctx context.Context, obj client.Object) []reconcile.Request {
    // ✅ Single request representing "global reconciliation"
    return []reconcile.Request{
        {NamespacedName: types.NamespacedName{
            Name:      GlobalReconcileTriggerName,
            Namespace: configMapNamespace,
        }},
    }
}

return ctrl.NewControllerManagedBy(mgr).
    For(&llmdVariantAutoscalingV1alpha1.VariantAutoscaling{}).
    Watches(
        &corev1.ConfigMap{},
        handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
            if obj.GetName() == configMapName && obj.GetNamespace() == configMapNamespace {
                return enqueueGlobalReconcile(ctx, obj)
            }
            return nil
        }),
        builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
            return obj.GetName() == configMapName && obj.GetNamespace() == configMapNamespace
        })),
    ).
    // ... rest of setup
```

**Step 3: Update Reconcile to Handle Global Trigger**

```go
func (r *VariantAutoscalingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Check if this is the global reconciliation trigger
    if req.Name == GlobalReconcileTriggerName {
        logger.Log.Info("Global reconciliation triggered by ConfigMap change")
        // Continue with normal processing - list ALL VAs
    }

    // Regular reconciliation logic - always processes ALL VAs
    // ...
}
```

### Pros
- ✅ Clean separation: explicit "global reconciliation" concept
- ✅ Easy to understand intent in logs
- ✅ Can be extended for other global triggers
- ✅ Self-documenting code

### Cons
- ❌ More boilerplate code
- ❌ Creates conceptual overhead (what is GlobalReconcileTriggerName?)
- ❌ Doesn't provide significant benefit over Solution 1

---

## Solution 5: Reconcile Only Affected VAs (Architectural Change)

### Description

**Change the reconciliation pattern** - stop processing ALL VAs on every reconciliation. Instead:
- ConfigMap change → Reconcile only affected VAs
- VA creation/update → Reconcile only that VA (or its model group)

This requires **major architectural refactoring** of the global optimization logic.

### Implementation Approach

**Option A: Per-Model Optimization**

```go
func (r *VariantAutoscalingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Get the specific VA that triggered reconciliation
    var va llmdVariantAutoscalingV1alpha1.VariantAutoscaling
    if err := r.Get(ctx, req.NamespacedName, &va); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // List only VAs for the same model
    modelID := va.Spec.ModelID
    allVAs, err := r.listVAsForModel(ctx, modelID)

    // Optimize only this model's variants
    optimizedAllocation, err := r.optimizeModel(ctx, modelID, allVAs)

    // Apply allocations only to this model's variants
    // ...

    return ctrl.Result{RequeueAfter: requeueDuration}, nil
}
```

**Option B: Periodic Global + Event-Driven Per-VA**

```go
func (r *VariantAutoscalingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Check if this is periodic global reconciliation
    if isPeriodicReconciliation(req) {
        // Do global optimization for all VAs
        return r.reconcileGlobal(ctx, requeueDuration)
    }

    // Event-driven: reconcile only the affected VA
    var va llmdVariantAutoscalingV1alpha1.VariantAutoscaling
    if err := r.Get(ctx, req.NamespacedName, &va); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Apply fallback allocation or quick heuristic (not full optimization)
    return r.reconcileSingleVA(ctx, &va)
}
```

### Pros
- ✅ Truly solves the scalability problem
- ✅ ConfigMap change affects only relevant VAs
- ✅ VA update affects only that VA (or model group)
- ✅ Scales to thousands of VAs

### Cons
- ❌ **MAJOR refactoring required**
- ❌ May lose global cost optimization benefits
- ❌ Complex to implement per-model optimization
- ❌ Requires rethinking scale-to-zero "cheapest variant" logic
- ❌ May need dual-mode: global periodic + per-VA event-driven

### Feasibility
**Not recommended** unless:
- Cluster has 500+ VAs
- ConfigMap changes are very frequent
- Willing to invest significant engineering effort

---

## Recommended Solution

### Short Term: **Solution 1 (Single Reconcile Request)**

**Effort**: Low (1-2 hours)
**Impact**: 99% reduction in redundant reconciliations
**Risk**: Very low

**Implementation Steps:**

1. Modify `enqueueAllVAs` function (line 1606):
   ```go
   enqueueAllVAs := func(ctx context.Context, obj client.Object) []reconcile.Request {
       var list llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
       if err := r.List(ctx, &list); err != nil {
           logger.Log.Error(err, "Failed to list VariantAutoscalings for ConfigMap watch")
           return nil
       }

       if len(list.Items) == 0 {
           logger.Log.Info("No VariantAutoscalings found, skipping ConfigMap reconciliation")
           return nil
       }

       // Return only ONE request instead of N requests
       logger.Log.Info("ConfigMap changed, triggering global reconciliation",
           "configMap", obj.GetName(),
           "totalVAs", len(list.Items))

       return []reconcile.Request{
           {NamespacedName: client.ObjectKeyFromObject(&list.Items[0])},
       }
   }
   ```

2. Add comment in Reconcile explaining the pattern:
   ```go
   func (r *VariantAutoscalingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
       // NOTE: This controller uses a global optimization pattern.
       // The 'req' parameter indicates which event triggered reconciliation, but is not used
       // to scope the reconciliation. Every reconciliation processes ALL VariantAutoscaling
       // resources across ALL namespaces to find the globally optimal allocation.
       //
       // Triggers:
       //   - Periodic timer (60s default)
       //   - VA created/updated
       //   - ConfigMap changed (single reconciliation for all VAs)

       // Default requeue duration...
   ```

3. Test with multiple VAs and ConfigMap update:
   ```bash
   # Create 10 VAs
   for i in {1..10}; do
     kubectl apply -f va-$i.yaml
   done

   # Update ConfigMap
   kubectl patch configmap variantautoscaling-config \
     -n workload-variant-autoscaler-system \
     --type merge -p '{"data":{"GLOBAL_OPT_INTERVAL":"45s"}}'

   # Check controller logs - should see only 1 reconciliation
   kubectl logs -n workload-variant-autoscaler-system \
     deployment/workload-variant-autoscaler-controller-manager \
     --tail=100 | grep -c "Starting reconciliation"

   # Expected: 1 reconciliation, not 10
   ```

### Long Term: Consider **Solution 5** if scaling beyond 500 VAs

---

## Comparison Matrix

| Solution | Effort | Reconciliations Reduced | ConfigMap Latency | Code Complexity | Scalability |
|----------|--------|------------------------|-------------------|-----------------|-------------|
| **1. Single Request** | Low | 99% | Immediate | Simple | Good (< 500 VAs) |
| **2. Workqueue Dedup** | Medium | ~70% | Immediate | Medium | Medium |
| **3. Periodic Only** | Very Low | 100% | Up to 60s | Very Simple | Excellent |
| **4. Global Trigger** | Medium | 99% | Immediate | Medium | Good |
| **5. Per-Model Opt** | Very High | 99% | Immediate | Complex | Excellent (1000+ VAs) |

---

## Implementation Checklist

### Solution 1 (Recommended)

- [ ] Modify `enqueueAllVAs` to return single request
- [ ] Add logging for ConfigMap-triggered reconciliation
- [ ] Add comment in `Reconcile` explaining global pattern
- [ ] Test with 10+ VAs and ConfigMap update
- [ ] Verify only 1 reconciliation occurs
- [ ] Update documentation (CONTROLLER_TRIGGER_LOGIC.md)
- [ ] Create unit test for ConfigMap watch behavior

### Performance Metrics to Track

Before fix:
```bash
# Count reconciliations after ConfigMap update
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  --since=1m | grep -c "Starting reconciliation"
```

After fix:
```bash
# Should see exactly 1 reconciliation per ConfigMap update
```

---

## Related Issues to Fix

While fixing ConfigMap reconciliation, consider also addressing:

1. **req Parameter Never Used** (line 130)
   - Add comment explaining why (global optimization pattern)
   - Or refactor to use req for per-VA reconciliation

2. **No Namespace Scoping** (line 177)
   - `r.List(ctx, &variantAutoscalingList)` lists ALL namespaces
   - Consider adding namespace filter option

3. **Deployment Watch Missing**
   - Manual deployment changes not detected until next periodic reconciliation
   - Consider watching Deployments referenced by VAs

4. **Delete Event Not Triggering Reconciliation** (line 1666)
   - Deleted VAs only removed after 60s
   - Consider immediate reconciliation on delete to free resources faster
