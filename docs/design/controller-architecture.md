# Controller Architecture

This document describes the internal architecture of the Workload-Variant-Autoscaler controller, including the reconciliation loop, resource watches, and event handling.

## Overview

The WVA controller is a Kubernetes operator built using the [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) framework. It follows the standard Kubernetes controller pattern:

1. **Watch** - Monitor resources for changes
2. **Reconcile** - Process changes and compute desired state
3. **Actuate** - Apply changes to the cluster

## Core Components

### VariantAutoscalingReconciler

The main controller struct that implements the reconciliation logic:

```go
type VariantAutoscalingReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    Recorder record.EventRecorder
    PromAPI promv1.API
    MetricsCollector interfaces.MetricsCollector
}
```

**Key Responsibilities:**
- Reconcile VariantAutoscaling resources
- Collect metrics from Prometheus or other backends
- Run saturation analysis or optimization algorithms
- Update VA status with current and desired allocations
- Emit metrics for HPA/KEDA to consume

### Resource Watches

The controller watches multiple resource types to trigger reconciliation:

#### 1. VariantAutoscaling (Primary Resource)

```go
For(&llmdVariantAutoscalingV1alpha1.VariantAutoscaling{})
```

**Purpose:** Main resource managed by the controller.

**Events Handled:**
- **Create:** Triggers initial reconciliation when a new VA is created
- **Update:** Blocked by EventFilter (controller reconciles periodically)
- **Delete:** Blocked by EventFilter (handled during periodic reconciliation)

**Rationale:** The controller reconciles all VAs periodically (default: 60s), so individual update/delete events would cause unnecessary work without benefit.

#### 2. Deployment Watch (Race Condition Handling)

```go
Watches(
    &appsv1.Deployment{},
    handler.EnqueueRequestsFromMapFunc(r.handleDeploymentEvent),
    builder.WithPredicates(DeploymentPredicate()),
)
```

**Purpose:** Handle the race condition where a VariantAutoscaling resource is created before its target deployment exists.

**Events Handled:**
- **Create:** When a deployment is created, find all VAs in the same namespace that reference it and enqueue them for reconciliation

**Behavior:**
1. Deployment created
2. Controller lists all VAs in the same namespace
3. For each VA where `va.GetScaleTargetName() == deployment.Name`
4. Enqueue a reconcile request for that VA

**Why This Is Needed:**

In Helm deployments, resources are often created in order, which can lead to:
```
11:15:22 - VariantAutoscaling created
11:15:22 - Controller reconciles VA, deployment not found
11:15:44 - Deployment created (22 seconds later)
11:15:44 - VA reconciliation triggered by deployment watch
11:15:44 - Controller finds deployment, VA gets status
```

Without this watch, the VA would never be reconciled again because:
- Initial reconcile failed (deployment not found)
- Update events for VAs are blocked by EventFilter
- VA would remain without status indefinitely

**Implementation Details:**

```go
func (r *VariantAutoscalingReconciler) handleDeploymentEvent(ctx context.Context, obj client.Object) []reconcile.Request {
    deploy, ok := obj.(*appsv1.Deployment)
    if !ok {
        return nil
    }
    
    // List all VAs in the same namespace
    var vaList llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
    if err := r.List(ctx, &vaList, client.InNamespace(deploy.Namespace)); err != nil {
        return nil
    }
    
    // Find VAs that reference this deployment
    var requests []reconcile.Request
    for _, va := range vaList.Items {
        if va.GetScaleTargetName() == deploy.Name {
            requests = append(requests, reconcile.Request{
                NamespacedName: client.ObjectKey{
                    Namespace: va.Namespace,
                    Name:      va.Name,
                },
            })
        }
    }
    
    return requests
}
```

#### 3. ConfigMap Watch

```go
Watches(
    &corev1.ConfigMap{},
    handler.EnqueueRequestsFromMapFunc(...),
    builder.WithPredicates(ConfigMapPredicate()),
)
```

**Purpose:** React to configuration changes without restarting the controller.

**ConfigMaps Watched:**
- `workload-variant-autoscaler-variantautoscaling-config` - Global optimization interval
- `saturation-scaling-config` - Saturation thresholds and analyzer configuration

**Events Handled:**
- **Create/Update:** Update in-memory configuration, trigger reconciliation if needed

**Predicate Filtering:**
```go
func ConfigMapPredicate() predicate.Predicate {
    return predicate.NewPredicateFuncs(func(obj client.Object) bool {
        name := obj.GetName()
        return (name == getConfigMapName() || name == getSaturationConfigMapName()) && 
               obj.GetNamespace() == configMapNamespace
    })
}
```

Only watches ConfigMaps with specific names in the controller's namespace.

#### 4. ServiceMonitor Watch

```go
Watches(
    &promoperator.ServiceMonitor{},
    handler.EnqueueRequestsFromMapFunc(r.handleServiceMonitorEvent),
    builder.WithPredicates(ServiceMonitorPredicate()),
)
```

**Purpose:** Detect when the controller's own ServiceMonitor is deleted, which would prevent Prometheus from scraping WVA metrics.

**Events Handled:**
- **Update (with deletionTimestamp):** Log error and emit Kubernetes event
- **Delete:** Log error and emit Kubernetes event

**Rationale:** The ServiceMonitor enables Prometheus to scrape the controller's metrics endpoint, which exports the `workload_optimized_replicas` metric consumed by HPA/KEDA. If deleted, autoscaling stops working silently.

#### 5. DecisionTrigger Channel

```go
WatchesRawSource(
    source.Channel(common.DecisionTrigger, &handler.EnqueueRequestForObject{}),
)
```

**Purpose:** Enable the optimization engine to trigger reconciliation without modifying resources in the API server.

**Use Case:** When running in experimental proactive mode, the background optimization engine can push decisions to VAs via this channel, triggering immediate reconciliation.

### Event Filtering

The controller uses a custom EventFilter to control which events trigger reconciliation:

```go
func EventFilter() predicate.Funcs {
    return predicate.Funcs{
        CreateFunc: func(e event.CreateEvent) bool {
            return true  // Allow all Create events
        },
        UpdateFunc: func(e event.UpdateEvent) bool {
            gvk := e.ObjectNew.GetObjectKind().GroupVersionKind()
            
            // Allow ConfigMap updates (config changes)
            if gvk.Kind == "ConfigMap" && gvk.Group == "" {
                return true
            }
            
            // Allow ServiceMonitor updates when deletion starts
            if gvk.Group == serviceMonitorGVK.Group && gvk.Kind == serviceMonitorGVK.Kind {
                if deletionTimestamp := e.ObjectNew.GetDeletionTimestamp(); deletionTimestamp != nil {
                    return true
                }
            }
            
            // Block VariantAutoscaling updates (reconcile periodically instead)
            return false
        },
        DeleteFunc: func(e event.DeleteEvent) bool {
            // Only allow ServiceMonitor deletes (for observability)
            gvk := e.Object.GetObjectKind().GroupVersionKind()
            return gvk.Group == serviceMonitorGVK.Group && gvk.Kind == serviceMonitorGVK.Kind
        },
        GenericFunc: func(e event.GenericEvent) bool {
            return false  // Block all Generic events
        },
    }
}
```

**Design Rationale:**

- **Periodic Reconciliation:** The controller reconciles all VAs every 60 seconds (configurable), making individual update events unnecessary
- **Reduced API Server Load:** Blocking update events reduces reconciliation frequency, lowering API server and controller CPU usage
- **Exception Handling:** ConfigMap and ServiceMonitor events are allowed because they require immediate response

## Reconciliation Flow

### 1. Resource Grouping

VAs are grouped by `modelID` for efficient batch processing:

```go
modelVAs := groupVariantAutoscalingsByModelID(ctx, allVAs)
```

### 2. Mode Selection

The controller operates in two modes:

#### CAPACITY-ONLY Mode (Default)

```go
if !IsExperimentalProactiveModelEnabled() {
    // Collect metrics for saturation analysis
    CollectMetricsForSaturationMode(ctx, modelVAs, vaMap, r.Client, r.MetricsCollector)
    
    // Analyze saturation and calculate targets
    saturationTargets, saturationAnalysis, variantStates, err := 
        AnalyzeSaturationForModel(ctx, modelID, modelVAs, r.Client, r.MetricsCollector, SaturationConfig)
    
    // Apply saturation-based scaling decisions
    ApplySaturationTargets(ctx, modelVAs, saturationTargets, vaMap, r.Client)
}
```

**Flow:**
1. Collect metrics from Prometheus (KV cache usage, queue depth, request rate)
2. Analyze saturation across all variants
3. Calculate desired replicas based on saturation thresholds
4. Update VA status with current and desired allocations
5. Emit metrics to Prometheus for HPA/KEDA consumption

#### HYBRID Mode (Experimental)

```go
if IsExperimentalProactiveModelEnabled() {
    // Run both saturation analyzer and model-based optimizer
    // Arbitrate between the two (capacity safety overrides proactive decisions)
}
```

### 3. Status Updates

The controller updates VA status with:
- **CurrentAlloc:** Current replica count, accelerator type, load profile, performance metrics
- **DesiredOptimizedAlloc:** Target replica count recommended by WVA
- **Actuation:** Whether the recommendation was applied
- **Conditions:** Status conditions for observability

### 4. Metrics Emission

The controller emits Prometheus metrics consumed by HPA/KEDA:

```go
workload_optimized_replicas{
    model_id="meta/llama-3.1-8b",
    variant="llama-8b-a100",
    namespace="llm-inference"
} 3.0
```

## RBAC Permissions

The controller requires the following permissions:

```yaml
# VariantAutoscaling resources
- apiGroups: ["llmd.ai"]
  resources: ["variantautoscalings", "variantautoscalings/status", "variantautoscalings/finalizers"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# Deployments (for getting target deployment info)
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list", "watch", "update", "patch"]

# Pods (for metrics collection)
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch"]

# ConfigMaps (for configuration)
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "update", "list", "watch"]

# ServiceMonitors (for observability)
- apiGroups: ["monitoring.coreos.com"]
  resources: ["servicemonitors"]
  verbs: ["get", "list", "watch"]

# Events (for status reporting)
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
```

## Performance Considerations

### Reconciliation Frequency

**Default:** 60 seconds (configurable via `GLOBAL_OPT_INTERVAL` in ConfigMap)

**Trade-offs:**
- **Higher frequency (e.g., 30s):** Faster response to load changes, higher CPU/API usage
- **Lower frequency (e.g., 120s):** Lower overhead, slower response to load changes

**Recommendation:** 60s provides good balance for most workloads. Adjust based on:
- Rate of load change in your workloads
- Controller CPU budget
- API server capacity

### Watch Efficiency

The controller uses predicates to filter events at the source, reducing unnecessary reconciliations:

```go
builder.WithPredicates(DeploymentPredicate())
```

Only deployment **Create** events are watched, not Update or Delete, significantly reducing event volume.

### Metrics Collection Caching

The PrometheusCollector implements caching to avoid repeated queries:

```go
// Background fetching with TTL-based cache
metricsCollector.StartBackgroundFetching(ctx, refreshInterval)
```

Metrics are fetched in the background and cached, so reconciliation reads from cache rather than querying Prometheus every time.

## Failure Handling

### Transient Errors

The controller uses exponential backoff for transient errors:

```go
func GetDeploymentWithBackoff(ctx context.Context, client client.Client, name, namespace string, deploy *appsv1.Deployment) error {
    backoff := wait.Backoff{
        Steps:    3,
        Duration: 100 * time.Millisecond,
        Factor:   2.0,
        Jitter:   0.1,
    }
    
    return retry.OnError(backoff, func(err error) bool {
        return apierrors.IsServiceUnavailable(err) || apierrors.IsTimeout(err)
    }, func() error {
        return client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deploy)
    })
}
```

### Partial Failures

If metrics collection fails for one VA, the controller continues processing other VAs:

```go
for i := range modelVAs {
    va := &modelVAs[i]
    if err := collectMetricsForVA(va); err != nil {
        logger.V(logging.DEBUG).Info("Could not collect metrics for VA, skipping", "va", va.Name)
        continue  // Skip this VA, process others
    }
}
```

### Deployment Not Found

When a deployment is not found, the controller:
1. Logs a debug message
2. Skips the VA for this reconciliation cycle
3. Relies on the deployment watch to trigger reconciliation when deployment is created

## Testing

### Unit Tests

Controller logic is tested with envtest (lightweight Kubernetes API server):

```go
func TestReconcile_DeploymentNotFound(t *testing.T) {
    // Create VA without deployment
    // Verify reconciliation skips VA without error
    // Create deployment
    // Verify VA gets reconciled and receives status
}
```

### E2E Tests

Full integration tests run on real Kubernetes clusters:

```go
func TestE2E_RaceCondition(t *testing.T) {
    // Create VA before deployment
    // Wait for deployment creation
    // Verify VA eventually gets status via deployment watch
}
```

## References

- [Controller Runtime Documentation](https://pkg.go.dev/sigs.k8s.io/controller-runtime)
- [Kubernetes Controller Pattern](https://kubernetes.io/docs/concepts/architecture/controller/)
- [Operator Best Practices](https://sdk.operatorframework.io/docs/best-practices/best-practices/)

## Next Steps

- [Troubleshooting Guide](../user-guide/troubleshooting.md) - Debug common controller issues
- [Developer Guide](../developer-guide/development.md) - Set up local development environment
- [Debugging Guide](../developer-guide/debugging.md) - Run controller locally for debugging
