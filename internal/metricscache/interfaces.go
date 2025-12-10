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

package metricscache

import (
	"time"
)

// Reader provides read-only access to the metrics cache.
// This interface is used by analyzers and the optimizer to query cached metrics.
type Reader interface {
	// GetTimeSeries returns raw time-series data for the specified metric.
	// Returns nil if the metric is not found in the cache.
	GetTimeSeries(metric string, labels map[string]string) *TimeSeries

	// GetAggregated returns a pre-computed aggregated value for the metric.
	// The aggregation is computed over the specified time window.
	GetAggregated(metric string, aggType AggregationType, window time.Duration, groupBy map[string]string) float64

	// GetLatestValue returns the most recent value for a metric with the given labels.
	// Returns 0 if the metric is not found.
	GetLatestValue(metric string, labels map[string]string) float64

	// GetMetricValues returns the latest values for multiple metrics.
	// Key: metric name, Value: map of label set to value.
	GetMetricValues(metricNames []string) map[string]map[string]float64

	// IsStale returns true if the cache has not been updated within the TTL.
	IsStale() bool

	// LastCollectionTime returns the timestamp of the last successful collection.
	LastCollectionTime() time.Time
}

// Writer provides write access to the metrics cache.
// This interface is used by the collector to update cached metrics.
type Writer interface {
	// UpdateMetrics stores a batch of metric values.
	// This is the primary method for sources to update the cache.
	UpdateMetrics(metrics []MetricValue)

	// UpdateTimeSeries stores time-series data for the specified metric.
	UpdateTimeSeries(metric string, labels map[string]string, ts *TimeSeries)

	// UpdateAggregated stores a pre-computed aggregated value.
	UpdateAggregated(metric string, aggType AggregationType, window time.Duration, groupBy map[string]string, value float64)

	// MarkCollectionComplete marks the collection cycle as complete.
	// This updates the last collection timestamp.
	MarkCollectionComplete()

	// Prune removes stale data older than the retention period.
	Prune(retention time.Duration)
}

// ReadWriter combines both read and write access to the cache.
type ReadWriter interface {
	Reader
	Writer
}
