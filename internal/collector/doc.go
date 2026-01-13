// Package collector provides metrics collection for LLM inference servers.
//
// The collector package implements a pluggable metrics collection system that
// gathers performance and saturation metrics from inference servers via Prometheus.
// It supports caching, background fetching, and multiple backend implementations.
//
// # Architecture
//
// The package uses a factory pattern with the MetricsCollector interface:
//
//	collector := factory.NewMetricsCollector(collectorType, client, promAPI, config)
//	metrics, err := collector.Collect(ctx, deployment, va)
//
// # Supported Backends
//
//   - Prometheus: Default backend using Prometheus API (prometheus/)
//   - EPP: Integration with external performance predictor (future)
//
// # Key Components
//
// MetricsCollector interface (internal/interfaces/metrics_collector.go):
//   - Collect(): Fetch metrics for a deployment
//   - ValidateMetricsAvailability(): Check if metrics exist
//   - SetCache(): Configure caching behavior
//
// Cache implementations (cache/):
//   - MemoryCache: In-memory LRU cache with TTL
//   - NoopCache: No-op implementation for testing
//
// Configuration (config/):
//   - CollectorConfig: Backend selection and settings
//
// # Metrics Collected
//
// The collector gathers comprehensive inference metrics:
//
// Request metrics:
//   - Arrival rate (requests/second)
//   - Average input tokens (prefill length)
//   - Average output tokens (decode length)
//
// Performance metrics:
//   - TTFT: Time To First Token (ms)
//   - ITL: Inter-Token Latency (ms)
//   - Generation throughput (tokens/second)
//
// Saturation metrics:
//   - KV cache utilization (percentage)
//   - Queue depth (pending requests)
//   - GPU memory usage
//   - Batch size utilization
//
// # Caching
//
// The collector supports caching to reduce Prometheus query load:
//
//	cache := cache.NewMemoryCache(1000, 30*time.Second) // size, TTL
//	collector.SetCache(cache)
//
// Cache keys are based on deployment namespace/name and controller instance.
//
// # Background Fetching
//
// The Prometheus collector supports background metric fetching for improved
// performance. Metrics are pre-fetched asynchronously and cached for quick
// access during reconciliation.
//
// Enable via ENABLE_BACKGROUND_FETCHING environment variable:
//
//	ENABLE_BACKGROUND_FETCHING=true
//	BACKGROUND_FETCH_INTERVAL=30s
//
// # Prometheus Queries
//
// The collector uses optimized PromQL queries with proper label selectors:
//
//	# Request rate
//	rate(vllm:request_success_total{namespace="$ns",job="$job"}[5m])
//
//	# KV cache utilization
//	avg_over_time(vllm:gpu_cache_usage_perc{namespace="$ns",job="$job"}[5m])
//
//	# TTFT
//	histogram_quantile(0.95, rate(vllm:time_to_first_token_seconds_bucket[5m]))
//
// Queries support controller instance filtering when CONTROLLER_INSTANCE is set.
//
// # Error Handling
//
// The collector returns structured errors:
//   - ErrMetricsNotAvailable: Metrics don't exist yet (e.g., new deployment)
//   - ErrPrometheusUnavailable: Prometheus API unreachable
//   - ErrQueryFailed: PromQL query syntax or execution error
//
// Callers should handle these gracefully and set appropriate conditions.
//
// # Usage Example
//
//	import (
//		"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
//		"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/config"
//	)
//
//	// Create collector
//	cfg := &config.CollectorConfig{
//		Type: config.CollectorTypePrometheus,
//		PrometheusConfig: &config.PrometheusConfig{
//			EnableBackgroundFetching: true,
//			FetchInterval: 30 * time.Second,
//		},
//	}
//	collector := factory.NewMetricsCollector(cfg.Type, k8sClient, promAPI, cfg)
//
//	// Fetch metrics
//	metrics, err := collector.Collect(ctx, deployment, va)
//	if err != nil {
//		// Handle error
//	}
//
//	// Use metrics for analysis
//	saturation := metrics.KVCacheUtilization
//	queueDepth := metrics.QueueDepth
//
// See also:
//   - internal/interfaces/metrics_collector.go: MetricsCollector interface
//   - internal/collector/prometheus/: Prometheus implementation
//   - docs/integrations/prometheus.md: Prometheus integration guide
package collector
