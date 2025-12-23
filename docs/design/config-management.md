# Configuration Management Architecture

## Overview

The WVA controller and saturation engine use a **shared in-memory configuration cache** to avoid repeated ConfigMap reads during reconciliation loops. This design improves performance and reduces API server load while maintaining real-time configuration updates through Kubernetes watch mechanisms.

## Architecture Change Summary

### Previous Design (Before PR #464)

- **Engine reads ConfigMaps directly**: The saturation engine (`internal/engines/saturation/engine.go`) made direct API calls to read ConfigMaps during each optimization cycle
- **Per-iteration overhead**: Every optimization loop (default: 30s) performed 2 ConfigMap reads:
  - `workload-variant-autoscaler-variantautoscaling-config` for optimization interval
  - `saturation-scaling-config` for saturation thresholds
- **Retry logic in engine**: Backoff and retry logic embedded in engine code
- **Tight coupling**: Engine component tightly coupled to Kubernetes API client

### Current Design (After PR #464)

- **Controller owns configuration**: The reconciler watches ConfigMaps and updates a shared global cache
- **Engine reads from cache**: The saturation engine reads configuration from thread-safe in-memory cache
- **Zero API calls during optimization**: Engine has no direct ConfigMap dependencies
- **Watch-based updates**: ConfigMap changes trigger immediate cache updates via Kubernetes watch
- **Separation of concerns**: Clear boundary between configuration management (controller) and optimization logic (engine)

## Implementation Details

### Shared Configuration Cache

**Location:** `internal/saturation/shared.go`

```go
// GlobalConfig holds the shared configuration for the autoscaler components.
type GlobalConfig struct {
    sync.RWMutex
    OptimizationInterval string
    SaturationConfig     map[string]interfaces.SaturationScalingConfig
}

// Thread-safe update methods
func (c *GlobalConfig) UpdateOptimizationConfig(interval string)
func (c *GlobalConfig) UpdateSaturationConfig(config map[string]interfaces.SaturationScalingConfig)

// Thread-safe read methods
func (c *GlobalConfig) GetOptimizationInterval() string
func (c *GlobalConfig) GetSaturationConfig() map[string]interfaces.SaturationScalingConfig

// Global singleton instance
var Config = &GlobalConfig{}
```

**Key characteristics:**
- **Thread-safe**: Uses `sync.RWMutex` for concurrent access from controller and engine threads
- **Read-optimized**: Multiple readers can access configuration concurrently without blocking
- **Global scope**: Single instance shared across all components
- **No API dependencies**: Pure in-memory data structure

### Controller ConfigMap Watch

**Location:** `internal/controller/variantautoscaling_controller.go`

The reconciler watches two ConfigMaps:

1. **Optimization Config** (`workload-variant-autoscaler-variantautoscaling-config`):
   - Contains: `GLOBAL_OPT_INTERVAL` (optimization loop interval)
   - Updates: `saturation.Config.UpdateOptimizationConfig(interval)`

2. **Saturation Scaling Config** (`saturation-scaling-config`):
   - Contains: Per-model saturation thresholds (YAML format)
   - Updates: `saturation.Config.UpdateSaturationConfig(configs)`

**Watch implementation:**

```go
func (r *VariantAutoscalingReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&llmdVariantAutoscalingV1alpha1.VariantAutoscaling{}).
        Watches(
            &corev1.ConfigMap{},
            handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
                cm, ok := obj.(*corev1.ConfigMap)
                if !ok || cm.GetNamespace() != configMapNamespace {
                    return nil
                }

                if cm.GetName() == getConfigMapName() {
                    // Parse and update optimization config
                    if interval, ok := cm.Data["GLOBAL_OPT_INTERVAL"]; ok {
                        saturation.Config.UpdateOptimizationConfig(interval)
                    }
                    return []reconcile.Request{{}}
                } else if cm.GetName() == getSaturationConfigMapName() {
                    // Parse and update saturation config
                    configs := parseAndValidateSaturationConfigs(cm.Data)
                    saturation.Config.UpdateSaturationConfig(configs)
                    return []reconcile.Request{{}}
                }
                return nil
            }),
            builder.WithPredicates(ConfigMapPredicate()),
        )
        // ... other watches
}
```

**Benefits:**
- **Immediate updates**: Configuration changes take effect on next optimization cycle (no restart required)
- **Validation at watch time**: Invalid configurations are logged and skipped during parsing
- **Single source of truth**: Controller is sole writer; engine is read-only consumer

### Engine Configuration Access

**Location:** `internal/engines/saturation/engine.go`

The saturation engine reads configuration from the global cache:

```go
func (e *Engine) optimize(ctx context.Context) error {
    // Read optimization interval from cache (no API call)
    interval := saturation.Config.GetOptimizationInterval()
    
    // Read saturation scaling config from cache (no API call)
    saturationConfigMap := saturation.Config.GetSaturationConfig()
    if len(saturationConfigMap) == 0 {
        logger.Info("Saturation scaling config not loaded yet, skipping optimization")
        return nil
    }
    
    // Proceed with optimization using cached configuration
    // ...
}
```

**Key changes:**
- **Removed**: Direct ConfigMap API calls (`e.readOptimizationConfig`, `e.readSaturationScalingConfig`)
- **Removed**: Retry logic and backoff (now handled at controller watch level)
- **Added**: Graceful handling of empty configuration (early return)

## Configuration Flow

### Startup Sequence

1. **Controller initialization**:
   - Reconciler starts and establishes ConfigMap watches
   - Initial ConfigMap values loaded into `saturation.Config` cache

2. **Engine initialization**:
   - Saturation engine starts optimization loop
   - Reads initial configuration from `saturation.Config` cache

3. **Normal operation**:
   - Engine runs optimization every 30s (or configured interval)
   - Engine reads configuration from cache (zero API overhead)

### Configuration Update Flow

1. **Administrator updates ConfigMap**:
   ```bash
   kubectl edit configmap saturation-scaling-config -n workload-variant-autoscaler-system
   ```

2. **Kubernetes notifies controller**:
   - Watch triggers `EnqueueRequestsFromMapFunc` handler
   - Controller parses and validates new configuration

3. **Controller updates cache**:
   ```go
   saturation.Config.UpdateSaturationConfig(configs)
   logger.Info("Updated global saturation config from ConfigMap", "entries", count)
   ```

4. **Engine picks up changes**:
   - Next optimization cycle reads updated configuration
   - New thresholds/intervals take effect immediately

**Latency:** Configuration changes typically take effect within 30-60 seconds (optimization cycle + reconciliation delay)

## Thread Safety

### Concurrent Access Patterns

**Writers (Controller thread):**
- ConfigMap watch handler calls `UpdateOptimizationConfig()` or `UpdateSaturationConfig()`
- Acquires write lock (`sync.Mutex.Lock()`)
- Updates configuration map
- Releases lock

**Readers (Engine thread):**
- Optimization loop calls `GetOptimizationInterval()` or `GetSaturationConfig()`
- Acquires read lock (`sync.RWMutex.RLock()`)
- Reads configuration (returns copy or pointer to map)
- Releases lock

**Correctness guarantees:**
- **No race conditions**: RWMutex ensures serializable access
- **No stale reads**: Readers always see consistent configuration state
- **No write starvation**: RWMutex favors readers but ensures writers eventually proceed

### Potential Data Races

⚠️ **Warning:** Current implementation returns the map directly from `GetSaturationConfig()`:

```go
func (c *GlobalConfig) GetSaturationConfig() map[string]interfaces.SaturationScalingConfig {
    c.RLock()
    defer c.RUnlock()
    // Returns map directly - caller must treat as read-only
    return c.SaturationConfig
}
```

**Safe usage requires:**
- Callers must **not modify** the returned map
- Engine code treats configuration as read-only (current implementation is safe)

**Future improvement:** Return a deep copy to eliminate shared mutable state:
```go
return copyMap(c.SaturationConfig)  // Defensive copy
```

## ConfigMap Reference

### Optimization Config

**ConfigMap:** `workload-variant-autoscaler-variantautoscaling-config`  
**Namespace:** `workload-variant-autoscaler-system` (or `$POD_NAMESPACE`)  
**Override:** Set `CONFIG_MAP_NAME` environment variable

**Schema:**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: workload-variant-autoscaler-variantautoscaling-config
  namespace: workload-variant-autoscaler-system
data:
  GLOBAL_OPT_INTERVAL: "30s"  # Go duration format (e.g., "30s", "1m", "90s")
```

**Default:** If not set, engine uses previous interval (defaults to 30s on startup)

### Saturation Scaling Config

**ConfigMap:** `saturation-scaling-config`  
**Namespace:** `workload-variant-autoscaler-system` (or `$POD_NAMESPACE`)  
**Override:** Set `SATURATION_CONFIG_MAP_NAME` environment variable

**Schema:** See [Saturation Scaling Configuration](../saturation-scaling-config.md) for complete reference

**Example:**
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
    kvSpareTrigger: 0.10
    queueSpareTrigger: 3
  
  llama-3-8b-l40s: |
    kvCacheThreshold: 0.85
    queueLengthThreshold: 8
    kvSpareTrigger: 0.15
    queueSpareTrigger: 4
```

**Validation:** Invalid entries are logged and skipped; engine continues with remaining valid configurations

## Benefits of New Architecture

### Performance

- **Zero API overhead in hot path**: Engine optimization loop has no API server dependencies
- **Reduced API server load**: 2 ConfigMap reads eliminated every 30s per WVA instance
- **Faster reconciliation**: No retry/backoff delays during optimization

### Scalability

- **Cluster efficiency**: Scales to many VariantAutoscaling resources without API throttling
- **Multi-tenant ready**: Shared cache reduces per-VA overhead

### Reliability

- **Failure isolation**: ConfigMap read failures don't block optimization (uses last known good config)
- **Graceful degradation**: Engine continues with cached configuration if watch temporarily fails
- **No retry storms**: Controller watch handles reconnection automatically

### Maintainability

- **Separation of concerns**: Configuration management isolated to controller layer
- **Simpler engine logic**: Engine focuses on optimization, not Kubernetes API interactions
- **Easier testing**: Engine can be tested with mock configurations without fake Kubernetes API

## Migration Notes

### Breaking Changes

None. This is an internal refactoring that maintains the same external API and behavior.

### Backward Compatibility

- ConfigMap schemas unchanged
- Environment variables unchanged
- Metrics and logging unchanged
- External autoscaler integration unchanged

### Upgrade Path

1. Deploy new controller version
2. Existing ConfigMaps continue to work without modification
3. Configuration updates take effect via watch (no restart required)

## Troubleshooting

### Configuration Not Updating

**Symptom:** Changes to ConfigMap don't take effect

**Diagnosis:**
```bash
# Check controller logs for ConfigMap watch events
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  | grep "Updated global.*config"

# Expected output:
# Updated global optimization config from ConfigMap interval=60s
# Updated global saturation config from ConfigMap entries=3
```

**Resolution:**
- Verify ConfigMap is in correct namespace
- Check ConfigMap name matches environment variables
- Ensure RBAC permissions allow ConfigMap watch

### Engine Skipping Optimization

**Symptom:** Logs show "Saturation scaling config not loaded yet"

**Cause:** Controller hasn't successfully loaded saturation config into cache

**Diagnosis:**
```bash
# Check if ConfigMap exists
kubectl get configmap saturation-scaling-config \
  -n workload-variant-autoscaler-system

# Check controller logs for parse errors
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  | grep "Failed to parse saturation"
```

**Resolution:**
- Verify ConfigMap exists and has valid YAML
- Check for validation errors in logs
- Deploy default ConfigMap if missing

### Stale Configuration

**Symptom:** Engine uses old configuration despite ConfigMap update

**Diagnosis:**
```bash
# Force reconciliation by annotating a VariantAutoscaling resource
kubectl annotate variantautoscaling my-va \
  force-reconcile="$(date +%s)" \
  -n llm-inference
```

**Resolution:**
- Wait 30-60s for next optimization cycle
- Check controller watch is active (restart controller if needed)
- Verify no RBAC issues blocking ConfigMap watch

## Related Documentation

- [Saturation Scaling Configuration](../saturation-scaling-config.md) - ConfigMap schema and parameters
- [Architecture Limitations](architecture-limitations.md) - Model architecture assumptions
- [Modeling & Optimization](modeling-optimization.md) - Core optimization algorithms

## References

- **PR #464**: "Engine should not read configmaps" - Configuration cache architecture
- **PR #460**: "Engine thread should not do any VA updates" - Engine/controller separation
- **Code**: `internal/saturation/shared.go` - Global configuration cache implementation
- **Code**: `internal/controller/variantautoscaling_controller.go` - ConfigMap watch logic
