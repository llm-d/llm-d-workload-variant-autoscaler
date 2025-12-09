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

// Package collector provides pluggable metrics collection for the autoscaler.
package collector

import (
	"time"
)

// MetricCategory represents the category of metrics being collected.
// Each category corresponds to a different source or domain of metrics.
type MetricCategory string

const (
	// CategoryScheduler represents metrics from the inference scheduler.
	// Includes: requests, queue length, routing, latency metrics.
	CategoryScheduler MetricCategory = "scheduler"

	// CategoryVLLM represents metrics from vLLM inference replicas.
	// Includes: KV cache usage, running/waiting requests, tokens, latency.
	CategoryVLLM MetricCategory = "vllm"

	// CategoryGPU represents cluster-level GPU availability metrics.
	// Includes: total, available, and allocated GPUs by type.
	CategoryGPU MetricCategory = "gpu"
)

// MetricSpec defines what metric to query and how to query it.
type MetricSpec struct {
	// Name is the metric name (e.g., "vllm:gpu_cache_usage_perc").
	Name string

	// Category is the metric category (scheduler, vllm, gpu).
	Category MetricCategory

	// Query is the raw query string (e.g., PromQL for Prometheus source).
	// If empty, the source will construct the query from Name and Labels.
	Query string

	// Labels are the label matchers for the query.
	// Key: label name, Value: label value (exact match).
	Labels map[string]string

	// LabelMatchers are regex-based label matchers.
	// Key: label name, Value: regex pattern.
	LabelMatchers map[string]string

	// GroupBy specifies labels to group results by.
	GroupBy []string
}

// MetricsCacheReader provides read-only access to the metrics cache.
// This interface is used by analyzers and the optimizer to query cached metrics.
type MetricsCacheReader interface {
	// GetTimeSeries returns raw time-series data for the specified metric.
	// Returns nil if the metric is not found in the cache.
	GetTimeSeries(metric string, labels map[string]string) *TimeSeries

	// GetAggregated returns a pre-computed aggregated value for the metric.
	// The aggregation is computed over the specified time window.
	GetAggregated(metric string, aggType AggregationType, window time.Duration, groupBy map[string]string) float64

	// GetSnapshot returns an immutable snapshot of all cached metrics.
	// This provides a consistent view of metrics at a point in time.
	GetSnapshot() *MetricsSnapshot

	// GetMetricValues returns the latest values for multiple metrics.
	// Key: metric name, Value: map of label set to value.
	GetMetricValues(metricNames []string) map[string]map[string]float64

	// IsStale returns true if the cache has not been updated within the TTL.
	// The TTL is configured in the cache implementation (e.g., MetricsCache constructor).
	IsStale() bool

	// LastCollectionTime returns the timestamp of the last successful collection.
	LastCollectionTime() time.Time
}

// MetricsCacheWriter provides write access to the metrics cache.
// This interface is used by the collector to update cached metrics.
type MetricsCacheWriter interface {
	// UpdateTimeSeries stores time-series data for the specified metric.
	UpdateTimeSeries(metric string, labels map[string]string, ts *TimeSeries)

	// UpdateAggregated stores a pre-computed aggregated value.
	UpdateAggregated(metric string, aggType AggregationType, window time.Duration, groupBy map[string]string, value float64)

	// MarkCollectionComplete marks the collection cycle as complete.
	// This updates the last collection timestamp and refreshes the snapshot.
	MarkCollectionComplete()

	// Prune removes stale data older than the retention period.
	Prune(retention time.Duration)
}

// MetricsSnapshot is an immutable point-in-time view of all cached metrics.
// Used to provide consistent reads across multiple metrics.
type MetricsSnapshot struct {
	// Timestamp is when this snapshot was created.
	Timestamp time.Time

	// Metrics contains all metric values at the snapshot time.
	// Key: metric name, Value: map of label set (as string) to value.
	Metrics map[string]map[string]float64

	// TimeSeries contains raw time-series data if available.
	// Key: metric name, Value: map of label set (as string) to TimeSeries.
	TimeSeries map[string]map[string]*TimeSeries

	// Aggregations contains pre-computed aggregations.
	// Key: aggregation key (metric:aggType:window:groupBy), Value: aggregated value.
	Aggregations map[string]float64
}
