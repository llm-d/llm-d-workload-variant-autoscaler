// Package collector provides metrics collection from Prometheus for WVA decision-making.
//
// The collector package implements the MetricsCollector interface and retrieves
// vLLM server metrics from Prometheus for use in autoscaling decisions.
//
// Key Components:
//
//   - Collector: Main metrics collection interface
//   - PrometheusCollector: Prometheus-specific implementation
//   - Cache: Metrics caching layer for performance
//   - BackgroundFetcher: Asynchronous metrics prefetching
//
// Collected Metrics:
//
//   - GPU cache utilization (vllm:gpu_cache_usage_perc)
//   - Queue depth (vllm:num_requests_waiting)
//   - Running requests (vllm:num_requests_running)
//   - Request rates and token metrics
//   - Server health and availability
//
// Architecture:
//
// The collector uses a two-tier caching strategy:
//  1. In-memory cache with TTL (default: 30s)
//  2. Background fetching to warm cache before reconciliation
//  3. Fallback to direct Prometheus queries on cache miss
//
// Example usage:
//
//	// Create Prometheus collector
//	collector, err := collector.NewPrometheusCollector(
//	    ctx,
//	    prometheusURL,
//	    prometheusConfig,
//	    cache,
//	)
//	if err != nil {
//	    return err
//	}
//	defer collector.Close()
//
//	// Collect metrics for a variant
//	metrics, err := collector.CollectMetrics(ctx, variantName, namespace)
//	if err != nil {
//	    log.Error(err, "failed to collect metrics")
//	    return err
//	}
//
//	log.Info("metrics collected",
//	    "variant", variantName,
//	    "cacheUtilization", metrics.CacheUtilization,
//	    "queueDepth", metrics.QueueDepth,
//	    "timestamp", metrics.Timestamp)
//
// Background Fetching:
//
// The collector supports background fetching to reduce reconciliation latency:
//
//	// Start background fetcher
//	fetcher := collector.StartBackgroundFetching(ctx, reconcileInterval)
//
//	// Register variants to prefetch
//	fetcher.Register(variantName, namespace)
//
//	// Metrics are automatically cached before reconciliation
//	// Subsequent CollectMetrics() calls use cached data
//
// Metrics Health:
//
// The collector tracks metrics health and freshness:
//   - Metrics age (time since last scrape)
//   - Scrape success rate
//   - Query errors and retries
//   - Stale metric detection (>5 minutes)
//
// The collector is designed to be:
//   - Performant with caching and background fetching
//   - Resilient to Prometheus unavailability
//   - Observable with detailed logging and metrics
//   - Testable with mock implementations
package collector
