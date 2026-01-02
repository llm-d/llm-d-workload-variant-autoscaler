# Metrics Collector Architecture

## Overview

The Metrics Collector is a pluggable component responsible for gathering metrics from various backends (Prometheus, EPP, etc.) for use by the WVA controller. It implements the `MetricsCollector` interface and provides:

- **Pluggable backend support** - Prometheus, EPP, or custom implementations
- **Intelligent caching** - Reduces load on metric backends
- **Background fetching** - Proactive metric collection to minimize reconciliation latency
- **Freshness tracking** - Monitors metric age and staleness
- **Thread-safe operations** - Concurrent access from multiple goroutines

## Architecture

### Components

```
┌─────────────────────────────────────┐
│   VariantAutoscaling Controller     │
│                                     │
│   (Reconciliation Loop)             │
└──────────────┬──────────────────────┘
               │
               ↓ MetricsCollector Interface
┌──────────────────────────────────────────────┐
│         PrometheusCollector                  │
│                                              │
│  ┌──────────────┐    ┌──────────────────┐  │
│  │  Memory      │    │  Background       │  │
│  │  Cache       │←───│  Fetch Executor   │  │
│  │              │    │  (Polling)        │  │
│  └──────┬───────┘    └────────┬─────────┘  │
│         │                     │             │
│         └─────────┬───────────┘             │
│                   ↓                         │
│         Prometheus Query Layer              │
└──────────────┬───────────────────────────────┘
               │
               ↓ PromQL Queries
┌──────────────────────────────┐
│       Prometheus Server       │
│                               │
│  - vLLM metrics               │
│  - KV cache utilization       │
│  - Queue depth                │
│  - Request rates              │
└───────────────────────────────┘
```

### Interfaces

The collector system is built around key interfaces:

**`MetricsCollector`** (`internal/interfaces/metrics_collector.go`)
- Primary interface for metric collection
- Methods: `CollectReplicaMetrics()`, `CollectAllocation()`
- Implemented by: `PrometheusCollector`, custom backends

**`MetricsCache`** (`internal/collector/cache/cache.go`)
- Generic caching layer for metrics
- Implementations: `MemoryCache`, `NoOpCache`
- Supports TTL-based expiration and prefix-based invalidation

## Metrics Caching

### Cache Strategy

WVA uses an intelligent caching strategy to balance performance and freshness:

1. **Cache-first reads** - Check cache before querying Prometheus
2. **Background updates** - Proactively refresh metrics in the background
3. **TTL-based expiration** - Automatic cleanup of stale entries
4. **Selective invalidation** - Invalidate specific metrics or prefixes

### Cache Configuration

Caching is configured via `CacheConfig` structure:

```go
type CacheConfig struct {
    Enabled         bool          // Enable/disable caching
    TTL             time.Duration // How long metrics stay valid
    CleanupInterval time.Duration // How often to clean expired entries
    FetchInterval   time.Duration // Background fetch interval (0 = disabled)
    FreshnessThresholds FreshnessThresholds // Fresh/stale/unavailable thresholds
}
```

**Default values:**
- `Enabled`: `true`
- `TTL`: `30s` (metrics valid for 30 seconds)
- `CleanupInterval`: `1m` (cleanup every minute)
- `FetchInterval`: `30s` (fetch every 30 seconds)

### Freshness Tracking

Metrics are classified by age:

- **Fresh** - Age < 1 minute (default) - optimal for scaling decisions
- **Stale** - Age 1-5 minutes - usable but may be outdated
- **Unavailable** - Age > 5 minutes - should not be used

Configure thresholds via `FreshnessThresholds`:

```go
type FreshnessThresholds struct {
    FreshThreshold       time.Duration // Default: 1 minute
    StaleThreshold       time.Duration // Default: 2 minutes
    UnavailableThreshold time.Duration // Default: 5 minutes
}
```

### Cache Keys

The collector uses structured cache keys to organize metrics:

**For Allocation metrics** (model-based optimizer):
```
allocation:<modelID>:<namespace>:<accelerator>
```

**For Replica metrics** (saturation analyzer):
```
replicas:<modelID>:<namespace>
```

This structure enables:
- Efficient lookups by model, namespace, or accelerator
- Prefix-based invalidation (e.g., invalidate all metrics for a model)
- Isolation between different metric types

## Background Fetching

### How It Works

Background fetching proactively refreshes metrics to reduce reconciliation latency:

1. **Tracking** - When a VariantAutoscaling resource is reconciled, it's added to the tracked set
2. **Polling** - A background worker polls at configured intervals (default: 30s)
3. **Fetching** - For each tracked resource, fetch fresh metrics from Prometheus
4. **Caching** - Store fetched metrics in cache with TTL
5. **Serving** - Controller reads from cache during reconciliation (no Prometheus query delay)

### Configuration

Enable background fetching via `FetchInterval`:

```go
cacheConfig := &config.CacheConfig{
    Enabled:       true,
    FetchInterval: 30 * time.Second, // Fetch every 30s
    TTL:           30 * time.Second, // Cache for 30s
}

collector := prometheus.NewPrometheusCollectorWithConfig(promAPI, cacheConfig)
```

**Disable background fetching:**
```go
cacheConfig := &config.CacheConfig{
    FetchInterval: 0, // Disable background fetching
}
```

### Tracking Lifecycle

**Add to tracking:**
- Automatically added when `CollectReplicaMetrics()` or `CollectAllocation()` is called
- Last fetch time is recorded to prevent duplicate fetches

**Remove from tracking:**
- Call `StopTrackingVA()` when a VariantAutoscaling resource is deleted
- Cache entries are invalidated

**Automatic cleanup:**
- Stale tracked resources (not fetched in 5+ minutes) are automatically removed

## Implementation Details

### Thread Safety

The collector is designed for concurrent access:

- **Cache** - Thread-safe `sync.RWMutex` protects read/write operations
- **Tracking maps** - `sync.Map` for lock-free concurrent reads
- **K8s client** - Protected by `sync.RWMutex` for hot-swap during initialization

### Retry Logic

Background fetching includes exponential backoff retry:

```go
maxRetries := 3
backoff := wait.Backoff{
    Duration: 1 * time.Second,
    Factor:   2.0,
    Steps:    maxRetries,
}
```

Failed fetches are logged but don't block the reconciliation loop.

### Memory Management

The cache implements automatic cleanup:

- **Periodic cleanup** - Every `CleanupInterval` (default: 1 minute)
- **TTL-based expiration** - Entries older than TTL are removed
- **Manual invalidation** - Call `Invalidate()` or `InvalidateByPrefix()` to remove entries

## Usage Examples

### Basic Usage (Default Configuration)

```go
import (
    "github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/prometheus"
)

// Create collector with defaults (caching enabled, 30s intervals)
collector := prometheus.NewPrometheusCollector(promAPI)

// Set K8s client (required for deployment queries)
collector.SetK8sClient(k8sClient)

// Start background worker
collector.StartBackgroundWorker(ctx)

// Collect metrics (uses cache if available, fetches if not)
replicaMetrics, err := collector.CollectReplicaMetrics(ctx, modelID, namespace, scaleTargetRef)
```

### Custom Configuration

```go
import (
    "github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/config"
    "github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/prometheus"
)

// Configure caching and background fetching
cacheConfig := &config.CacheConfig{
    Enabled:         true,
    TTL:             60 * time.Second,  // Cache for 60s
    CleanupInterval: 2 * time.Minute,   // Cleanup every 2 minutes
    FetchInterval:   45 * time.Second,  // Fetch every 45s
    FreshnessThresholds: config.FreshnessThresholds{
        FreshThreshold:       90 * time.Second,
        StaleThreshold:       3 * time.Minute,
        UnavailableThreshold: 10 * time.Minute,
    },
}

collector := prometheus.NewPrometheusCollectorWithConfig(promAPI, cacheConfig)
collector.SetK8sClient(k8sClient)
collector.StartBackgroundWorker(ctx)
```

### Disable Caching

```go
// Disable caching entirely (always query Prometheus)
cacheConfig := &config.CacheConfig{
    Enabled: false,
}

collector := prometheus.NewPrometheusCollectorWithConfig(promAPI, cacheConfig)
```

### Manual Cache Management

```go
// Invalidate specific metric
collector.InvalidateCache(ctx, modelID, namespace)

// Stop tracking a VA (and invalidate its cache)
collector.StopTrackingVA(ctx, vaNamespace, vaName)

// Check cache statistics
size := collector.GetCacheSize()
```

## Performance Considerations

### Benefits of Caching

- **Reduced Prometheus load** - Fewer queries, especially during high reconciliation rates
- **Lower reconciliation latency** - Cached metrics avoid query roundtrip (50-200ms savings)
- **Improved reliability** - Cache provides metrics even if Prometheus is temporarily unavailable

### Trade-offs

- **Memory usage** - Cache stores metrics in memory (typically <10MB for 100 variants)
- **Staleness risk** - Metrics may be up to `TTL` seconds old
- **Complexity** - Additional moving parts (background worker, cache cleanup)

### Recommendations

**For production:**
- Enable caching with 30-60s TTL
- Enable background fetching with 30-60s interval
- Monitor cache hit rate via logs

**For development:**
- Disable caching to see real-time metrics
- Use shorter TTL (10-15s) for faster iteration

**For high-scale clusters (100+ VAs):**
- Increase fetch interval to 60-90s
- Increase TTL to 60-90s
- Monitor Prometheus query rate

## Monitoring and Debugging

### Log Messages

The collector emits structured logs:

```
INFO  Metrics cache enabled  TTL=30s cleanupInterval=1m
INFO  Started background fetching executor  interval=30s
DEBUG Background fetch succeeded  modelID=llama-8b namespace=default age=2.3s
WARN  Metrics are stale  modelID=llama-8b age=3m12s threshold=2m
```

### Debug Mode

Enable debug logging to see cache operations:

```go
import "github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"

// In controller setup
ctrl.SetLogger(logger.V(logging.DEBUG))
```

### Cache Statistics

Query cache state:

```go
// Get cache size
size := collector.GetCacheSize()

// Check freshness status
status := freshnessThresholds.DetermineStatus(age)
// Returns: "fresh", "stale", or "unavailable"
```

## Future Enhancements

### Planned Features

- **Adaptive TTL** - Adjust TTL based on metric volatility
- **Eviction policies** - LRU eviction for memory-constrained environments
- **Multi-backend caching** - Shared cache across Prometheus and EPP collectors
- **Metrics export** - Expose cache hit rate and freshness as Prometheus metrics

### Extensibility

To implement a custom collector:

1. Implement the `MetricsCollector` interface
2. Use the shared `cache.MetricsCache` interface
3. Follow the same tracking and invalidation patterns
4. See `internal/collector/prometheus/prometheus_collector.go` as reference

## Related Documentation

- [Prometheus Integration](../integrations/prometheus.md) - Prometheus setup and configuration
- [Saturation Analyzer](../saturation-analyzer.md) - How saturation analysis uses metrics
- [Testing Guide](testing.md) - Testing the metrics collector
- [Development Guide](development.md) - Setting up dev environment

## References

- **Code:** `internal/collector/prometheus/prometheus_collector.go`
- **Interface:** `internal/interfaces/metrics_collector.go`
- **Cache:** `internal/collector/cache/cache.go`
- **Config:** `internal/collector/config/config.go`
- **Tests:** `internal/collector/prometheus/*_test.go`
