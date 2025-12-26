# Common Package API Reference

Package: `github.com/llm-d-incubation/workload-variant-autoscaler/internal/engines/common`

## Overview

The `common` package provides shared cache and state management for all autoscaling engines in WVA. It enables efficient communication between Controllers and Engines without repeated API server queries.

## Global Variables

### DecisionCache

```go
var DecisionCache = &InternalDecisionCache{
    items: make(map[string]interfaces.VariantDecision),
}
```

Global decision cache instance. Thread-safe for concurrent access.

### Config

```go
var Config = &GlobalConfig{}
```

Global configuration instance. Thread-safe for concurrent access.

### DecisionTrigger

```go
var DecisionTrigger = make(chan event.GenericEvent, 1000)
```

Buffered channel for triggering controller reconciliation from engines. Buffer size: 1000 events.

## Types

### InternalDecisionCache

Thread-safe cache for storing scaling decisions.

```go
type InternalDecisionCache struct {
    sync.RWMutex
    items map[string]interfaces.VariantDecision
}
```

#### Methods

##### Set

```go
func (c *InternalDecisionCache) Set(name, namespace string, d interfaces.VariantDecision)
```

Store a scaling decision in the cache.

**Parameters:**
- `name` - VariantAutoscaling name
- `namespace` - VariantAutoscaling namespace
- `d` - The scaling decision to store

**Thread Safety:** Uses write lock. Safe for concurrent use.

**Example:**
```go
decision := interfaces.VariantDecision{
    VariantName:     "llama-8b-autoscaler",
    Namespace:       "llm-inference",
    TargetReplicas:  5,
    AcceleratorName: "A100",
    Action:          interfaces.ActionScaleUp,
}
common.DecisionCache.Set("llama-8b-autoscaler", "llm-inference", decision)
```

##### Get

```go
func (c *InternalDecisionCache) Get(name, namespace string) (interfaces.VariantDecision, bool)
```

Retrieve a scaling decision from the cache.

**Parameters:**
- `name` - VariantAutoscaling name
- `namespace` - VariantAutoscaling namespace

**Returns:**
- `interfaces.VariantDecision` - The cached decision
- `bool` - True if found, false otherwise

**Thread Safety:** Uses read lock. Multiple concurrent readers allowed.

**Example:**
```go
decision, ok := common.DecisionCache.Get("llama-8b-autoscaler", "llm-inference")
if !ok {
    // Decision not found
}
```

### GlobalConfig

Thread-safe cache for shared autoscaler configuration.

```go
type GlobalConfig struct {
    sync.RWMutex
    OptimizationInterval string
    SaturationConfig     map[string]interfaces.SaturationScalingConfig
}
```

#### Methods

##### UpdateOptimizationConfig

```go
func (c *GlobalConfig) UpdateOptimizationConfig(interval string)
```

Update the optimization interval.

**Parameters:**
- `interval` - Duration string (e.g., "30s", "1m")

**Thread Safety:** Uses write lock.

**Example:**
```go
common.Config.UpdateOptimizationConfig("30s")
```

##### UpdateSaturationConfig

```go
func (c *GlobalConfig) UpdateSaturationConfig(config map[string]interfaces.SaturationScalingConfig)
```

Update the saturation scaling configuration.

**Parameters:**
- `config` - Map of model ID to saturation config

**Thread Safety:** Uses write lock.

**Example:**
```go
configs := map[string]interfaces.SaturationScalingConfig{
    "llama-8b": {
        KvCacheThreshold:     0.80,
        QueueLengthThreshold: 5,
    },
}
common.Config.UpdateSaturationConfig(configs)
```

##### GetOptimizationInterval

```go
func (c *GlobalConfig) GetOptimizationInterval() string
```

Get the current optimization interval.

**Returns:**
- `string` - Duration string

**Thread Safety:** Uses read lock.

**Example:**
```go
interval := common.Config.GetOptimizationInterval()
duration, err := time.ParseDuration(interval)
```

##### GetSaturationConfig

```go
func (c *GlobalConfig) GetSaturationConfig() map[string]interfaces.SaturationScalingConfig
```

Get the current saturation scaling configuration.

**Returns:**
- `map[string]interfaces.SaturationScalingConfig` - Configuration map

**Thread Safety:** Uses read lock.

**Note:** Returned map should be treated as read-only.

**Example:**
```go
config := common.Config.GetSaturationConfig()
if modelConfig, ok := config["llama-8b"]; ok {
    threshold := modelConfig.KvCacheThreshold
}
```

## Functions

### DecisionToOptimizedAlloc

```go
func DecisionToOptimizedAlloc(d interfaces.VariantDecision) (int, string, metav1.Time)
```

Convert a VariantDecision to OptimizedAlloc status format.

**Parameters:**
- `d` - The scaling decision

**Returns:**
- `int` - Target replicas
- `string` - Accelerator name
- `metav1.Time` - Current timestamp

**Example:**
```go
replicas, accelerator, timestamp := common.DecisionToOptimizedAlloc(decision)
va.Status.OptimizedAlloc.Replicas = replicas
va.Status.OptimizedAlloc.Accelerator = accelerator
va.Status.OptimizedAlloc.LastUpdateTime = timestamp
```

### UpdateVACache

```go
func UpdateVACache(va *wvav1alpha1.VariantAutoscaling)
```

Add or update a VariantAutoscaling in the global cache.

**Parameters:**
- `va` - VariantAutoscaling resource

**Thread Safety:** Uses write lock.

**Note:** Creates a deep copy of the VA to prevent external modifications.

**Example:**
```go
// In controller reconciliation
common.UpdateVACache(variantAutoscaling)
```

### RemoveVACache

```go
func RemoveVACache(key client.ObjectKey)
```

Remove a VariantAutoscaling from the global cache.

**Parameters:**
- `key` - ObjectKey with Name and Namespace

**Thread Safety:** Uses write lock.

**Example:**
```go
// When VA is deleted
key := client.ObjectKey{
    Name:      va.Name,
    Namespace: va.Namespace,
}
common.RemoveVACache(key)
```

### GetReadyVAs

```go
func GetReadyVAs() []wvav1alpha1.VariantAutoscaling
```

Retrieve all ready VariantAutoscalings from the cache.

**Returns:**
- `[]wvav1alpha1.VariantAutoscaling` - List of ready VAs

**Thread Safety:** Uses read lock.

**Filtering:** Excludes VAs with non-zero DeletionTimestamp.

**Example:**
```go
// In engine optimization loop
readyVAs := common.GetReadyVAs()
for _, va := range readyVAs {
    // Process each VA
}
```

## Usage Patterns

### Controller Pattern

```go
func (r *VariantAutoscalingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. Fetch VA from API
    va := &wvav1alpha1.VariantAutoscaling{}
    if err := r.Get(ctx, req.NamespacedName, va); err != nil {
        if apierrors.IsNotFound(err) {
            // 2. Remove from cache if deleted
            common.RemoveVACache(req.NamespacedName)
            return ctrl.Result{}, nil
        }
        return ctrl.Result{}, err
    }
    
    // 3. Update cache
    common.UpdateVACache(va)
    
    // 4. Read decision from cache
    decision, ok := common.DecisionCache.Get(va.Name, va.Namespace)
    if ok {
        // 5. Update status
        replicas, accelerator, timestamp := common.DecisionToOptimizedAlloc(decision)
        va.Status.OptimizedAlloc.Replicas = replicas
        va.Status.OptimizedAlloc.Accelerator = accelerator
        va.Status.OptimizedAlloc.LastUpdateTime = timestamp
        
        if err := r.Status().Update(ctx, va); err != nil {
            return ctrl.Result{}, err
        }
    }
    
    return ctrl.Result{}, nil
}
```

### Engine Pattern

```go
func (e *Engine) optimize(ctx context.Context) error {
    // 1. Read configuration
    interval := common.Config.GetOptimizationInterval()
    satConfig := common.Config.GetSaturationConfig()
    
    // 2. Get VAs from cache (no API call)
    readyVAs := common.GetReadyVAs()
    
    // 3. Process each VA
    for _, va := range readyVAs {
        // Analyze and make decision
        decision := e.analyzeAndDecide(va, satConfig)
        
        // 4. Store decision
        common.DecisionCache.Set(va.Name, va.Namespace, decision)
        
        // 5. Trigger reconciliation
        common.DecisionTrigger <- event.GenericEvent{
            Object: &wvav1alpha1.VariantAutoscaling{
                ObjectMeta: metav1.ObjectMeta{
                    Name:      va.Name,
                    Namespace: va.Namespace,
                },
            },
        }
    }
    
    return nil
}
```

## Thread Safety Guarantees

All exported functions and types are thread-safe:

- **Read operations** use `sync.RWMutex.RLock()` allowing concurrent reads
- **Write operations** use `sync.RWMutex.Lock()` for exclusive access
- All locks are released via `defer` to prevent deadlocks
- Deep copies are made where necessary to prevent external modifications

## Performance Characteristics

- **Cache Reads**: O(1) map lookup, ~100ns latency
- **Cache Writes**: O(1) map write, ~1-10μs latency
- **GetReadyVAs**: O(n) iteration where n = number of VAs
- **Memory**: O(n) where n = number of active VAs

## Related Documentation

- [Common Cache Layer Architecture](common-cache-layer.md) - Detailed architecture guide
- [Developer Guide](../developer-guide/development.md) - Development setup and workflow
- [Testing Guide](../developer-guide/testing.md) - Testing strategies including cache tests

## See Also

- `internal/interfaces/interfaces.go` - Interface definitions
- `internal/controller/variantautoscaling_controller.go` - Controller implementation
- `internal/engines/saturation/engine.go` - Saturation engine implementation
