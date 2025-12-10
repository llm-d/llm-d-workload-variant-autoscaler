/*
Copyright 2025 The llm-d Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package collector

import (
	"context"
	"time"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/metricscache"
)

// MetricRequest defines a metric that a consumer needs.
// Consumers (analytics, optimizer) register metric requests at startup.
type MetricRequest struct {
	// Name is the metric name (e.g., "vllm_gpu_cache_usage_perc").
	Name string

	// Labels are required label matchers for the metric.
	// Key: label name, Value: label value (exact match).
	Labels map[string]string
}

// MetricSource is the interface for pluggable metric sources.
// Implementations include PrometheusSource, EPPSource, etc.
//
// At startup, consumers register metrics they need via Register().
// The source stores registered metrics internally and collects them.
type MetricSource interface {
	// Name returns the unique name of this source (e.g., "prometheus", "epp").
	Name() string

	// CollectionInterval returns the actual collection interval for this source.
	// The MetricsCollector uses this to run per-source tickers.
	// Different sources have different latency characteristics:
	// - EPP: 1-5s (fast, low overhead)
	// - Prometheus: 15-30s (slower, aggregated queries)
	// - Kubernetes API: 30-60s (rate-limited)
	CollectionInterval() time.Duration

	// Register attempts to register a metric with this source.
	// Returns true if this source can provide the metric, false otherwise.
	// If true, the source stores the metric internally for collection.
	// Called once at startup for each metric consumers need.
	Register(metric MetricRequest) bool

	// Collect returns current values for all registered metrics.
	// Only collects metrics that were successfully registered via Register().
	Collect(ctx context.Context) ([]metricscache.MetricValue, error)
}
