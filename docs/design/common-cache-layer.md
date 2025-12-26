# Common Cache Layer Architecture

## Overview

The common cache layer provides shared state management for all autoscaling engines in WVA. Introduced in PR #471, it eliminates duplicate caching logic and enables efficient communication between the Controller and Engine components without requiring API server queries on every optimization cycle.

## Motivation

Prior to the common cache layer, each engine component maintained its own caching mechanisms, leading to:
- Code duplication across engine implementations
- Increased API server load from repeated queries
- Complex synchronization between controllers and engines
- Difficult testing due to scattered state management

The common cache layer centralizes this functionality, providing:
- Single source of truth for variant decisions and configurations
- Reduced API server traffic
- Thread-safe concurrent access
- Simplified engine implementations

## Architecture

### Components

The common cache layer consists of three main components located in `internal/engines/common/cache.go`:

#### 1. Decision Cache (`InternalDecisionCache`)

Stores the latest scaling decisions for VariantAutoscaling resources.

```go
type InternalDecisionCache struct {
    sync.RWMutex
    items map[string]interfaces.VariantDecision
}
```

**Key Methods:**
- `Set(name, namespace, decision)` - Store a decision
- `Get(name, namespace)` - Retrieve a decision
- `DecisionToOptimizedAlloc(decision)` - Convert decision to status format

**Usage:**
```go
// Engine makes a scaling decision
decision := interfaces.VariantDecision{
    VariantName:     "llama-8b-autoscaler",
    Namespace:       "llm-inference",
    TargetReplicas:  5,
    AcceleratorName: "A100",
    Action:          interfaces.ActionScaleUp,
}
common.DecisionCache.Set("llama-8b-autoscaler", "llm-inference", decision)

// Controller retrieves decision without API query
decision, ok := common.DecisionCache.Get("llama-8b-autoscaler", "llm-inference")
```

#### 2. Global Configuration (`GlobalConfig`)

Manages shared autoscaler configuration that needs to be accessible across components.

```go
type GlobalConfig struct {
    sync.RWMutex
    OptimizationInterval string
    SaturationConfig     map[string]interfaces.SaturationScalingConfig
}
```

**Key Methods:**
- `UpdateOptimizationConfig(interval)` - Update optimization interval
- `UpdateSaturationConfig(config)` - Update saturation scaling configuration
- `GetOptimizationInterval()` - Read current optimization interval
- `GetSaturationConfig()` - Read current saturation configuration

**Usage:**
```go
// Controller updates configuration from ConfigMap
common.Config.UpdateOptimizationConfig("30s")
common.Config.UpdateSaturationConfig(saturationConfigs)

// Engine reads configuration
interval := common.Config.GetOptimizationInterval()
satConfig := common.Config.GetSaturationConfig()
```

#### 3. VariantAutoscaling Cache

In-memory cache of VariantAutoscaling CRs to avoid API server queries during engine optimization cycles.

**Key Functions:**
- `UpdateVACache(va)` - Add/update a VA in cache
- `RemoveVACache(key)` - Remove a VA from cache
- `GetReadyVAs()` - Retrieve all ready VAs for optimization

**Usage:**
```go
// Controller updates cache on reconciliation
common.UpdateVACache(variantAutoscaling)

// Engine retrieves all ready VAs without API call
readyVAs := common.GetReadyVAs()
for _, va := range readyVAs {
    // Process each VA
}
```

#### 4. Decision Trigger Channel

Buffered channel for engines to trigger controller reconciliation.

```go
var DecisionTrigger = make(chan event.GenericEvent, 1000)
```

**Usage:**
```go
// Engine triggers reconciliation after making a decision
common.DecisionTrigger <- event.GenericEvent{
    Object: &wvav1alpha1.VariantAutoscaling{
        ObjectMeta: metav1.ObjectMeta{
            Name:      vaName,
            Namespace: vaNamespace,
        },
    },
}
```

## Data Flow

### Typical Optimization Cycle

```
┌──────────────┐
│  Controller  │
│ Reconciliation│
└──────┬───────┘
       │ 1. Update VA cache
       │ common.UpdateVACache(va)
       │
       │ 2. Update configuration
       │ common.Config.UpdateSaturationConfig(...)
       │
       ↓
┌──────────────┐
│    Engine    │
│  Optimization│
└──────┬───────┘
       │ 3. Read VAs from cache
       │ readyVAs := common.GetReadyVAs()
       │
       │ 4. Read configuration
       │ config := common.Config.GetSaturationConfig()
       │
       │ 5. Make decision
       │ decision := calculateScaling(...)
       │
       │ 6. Store decision in cache
       │ common.DecisionCache.Set(...)
       │
       │ 7. Trigger reconciliation
       │ common.DecisionTrigger <- event
       │
       ↓
┌──────────────┐
│  Controller  │
│ Status Update│
└──────┬───────┘
       │ 8. Read decision from cache
       │ decision := common.DecisionCache.Get(...)
       │
       │ 9. Update VA status
       │ va.Status.OptimizedAlloc = ...
       │
       └─> (cycle repeats)
```

## Thread Safety

All cache components use `sync.RWMutex` for thread-safe concurrent access:

- **Read operations** use `RLock()` allowing multiple concurrent readers
- **Write operations** use `Lock()` ensuring exclusive access
- Locks are always released via `defer` to prevent deadlocks

### Example: Safe Concurrent Access

```go
// Multiple goroutines can read simultaneously
decision1, ok1 := common.DecisionCache.Get("va1", "ns1")
decision2, ok2 := common.DecisionCache.Get("va2", "ns2")

// Writes are serialized
common.DecisionCache.Set("va1", "ns1", decision1)
common.DecisionCache.Set("va2", "ns2", decision2)
```

## Integration Points

### In Controller (`internal/controller/variantautoscaling_controller.go`)

The controller:
1. Updates VA cache on every reconciliation
2. Watches for events from `DecisionTrigger` channel
3. Reads decisions from `DecisionCache` to update VA status
4. Updates `GlobalConfig` when ConfigMaps change

```go
// Setup controller with decision trigger watch
func (r *VariantAutoscalingReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&llmdVariantAutoscalingV1alpha1.VariantAutoscaling{}).
        Watches(
            &source.Channel{Source: common.DecisionTrigger},
            &handler.EnqueueRequestForObject{},
        ).
        Complete(r)
}
```

### In Saturation Engine (`internal/engines/saturation/engine.go`)

The saturation engine:
1. Reads VAs from cache via `GetReadyVAs()`
2. Reads configuration via `Config.GetSaturationConfig()`
3. Stores decisions via `DecisionCache.Set()`
4. Triggers reconciliation via `DecisionTrigger` channel

```go
func (e *Engine) optimize(ctx context.Context) error {
    // Get VAs without API call
    readyVAs := common.GetReadyVAs()
    
    // Get configuration
    satConfig := common.Config.GetSaturationConfig()
    
    // Make scaling decision
    for _, va := range readyVAs {
        decision := e.analyzeAndDecide(va, satConfig)
        
        // Store decision
        common.DecisionCache.Set(va.Name, va.Namespace, decision)
        
        // Trigger reconciliation
        common.DecisionTrigger <- event.GenericEvent{Object: &va}
    }
    
    return nil
}
```

## Testing

The cache layer includes comprehensive unit tests in `internal/engines/common/cache_test.go`:

### Decision Cache Tests
- Basic Set/Get operations
- Non-existent item handling
- Concurrent access with 100 goroutines
- Decision to OptimizedAlloc conversion

### Global Config Tests
- Optimization interval updates
- Saturation config updates
- Concurrent read/write operations

### VA Cache Tests
- Add/update VAs
- Remove VAs
- Retrieve ready VAs (filtering deleted)
- Concurrent operations

### Running Tests

```bash
# Run all cache tests
go test ./internal/engines/common/...

# Run with race detector
go test -race ./internal/engines/common/...

# Run with coverage
go test -cover ./internal/engines/common/...
```

## Performance Characteristics

### Memory Usage

- **Decision Cache**: O(n) where n = number of active VAs
- **VA Cache**: O(n) where n = number of VAs (full CR copies)
- **Config Cache**: O(1) for interval, O(m) where m = number of models for saturation config

### Latency

- **Cache Reads**: ~100ns (in-memory map lookup with RLock)
- **Cache Writes**: ~1-10μs (in-memory map write with Lock)
- **API Server Queries Saved**: ~10-100ms per optimization cycle

### Scalability

The cache layer is designed for clusters with:
- Up to 1000 concurrent VariantAutoscaling resources
- Optimization intervals as low as 1 second
- High-frequency reconciliation (multiple times per second)

## Best Practices

### For Engine Developers

1. **Always use GetReadyVAs()** instead of querying the API server
2. **Store all decisions** in DecisionCache for controller consumption
3. **Trigger reconciliation** after storing decisions
4. **Read configuration** from GlobalConfig, never query ConfigMaps directly

### For Controller Developers

1. **Update VA cache** on every reconciliation, including deletions
2. **Remove from cache** when VA is deleted
3. **Watch DecisionTrigger** channel for engine-initiated reconciliations
4. **Update GlobalConfig** whenever ConfigMaps change

### Thread Safety Guidelines

1. **Never hold locks** across I/O operations (API calls, network requests)
2. **Keep critical sections short** to minimize contention
3. **Use RLock** for reads to allow concurrent access
4. **Test with race detector** to catch concurrency issues

## Migration Guide

### Before (Direct API Queries)

```go
// Old approach - API server query on every optimization
vaList := &llmdVariantAutoscalingV1alpha1.VariantAutoscalingList{}
if err := r.Client.List(ctx, vaList); err != nil {
    return err
}

for _, va := range vaList.Items {
    // Process each VA
}
```

### After (Common Cache)

```go
// New approach - in-memory cache read
readyVAs := common.GetReadyVAs()
for _, va := range readyVAs {
    // Process each VA (same logic)
}
```

## Troubleshooting

### Stale Cache Data

**Symptom**: Controller sees outdated VA state
**Solution**: Ensure `UpdateVACache()` is called in all reconciliation paths

### Missed Reconciliations

**Symptom**: Status not updating after engine decisions
**Solution**: Verify `DecisionTrigger` channel is watched by controller

### Configuration Not Applied

**Symptom**: Engine uses old configuration
**Solution**: Check ConfigMap watch is triggering `UpdateSaturationConfig()`

### Memory Leaks

**Symptom**: Growing memory usage over time
**Solution**: Ensure `RemoveVACache()` is called when VAs are deleted

## Future Enhancements

Potential improvements to the common cache layer:

1. **TTL-based expiration** for stale decisions
2. **Metrics emission** for cache hit/miss rates
3. **Size limits** with LRU eviction
4. **Persistence** for recovery after restarts
5. **Distributed caching** for multi-controller deployments

## Related Documentation

- [Architecture Overview](modeling-optimization.md)
- [Saturation Analyzer](../saturation-analyzer.md)
- [Developer Guide](../developer-guide/development.md)
- [Testing Guide](../developer-guide/testing.md)

## References

- PR #471: Add common cache layer for all engines
- Package: `github.com/llm-d-incubation/workload-variant-autoscaler/internal/engines/common`
- Interfaces: `github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces`
