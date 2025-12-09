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
	"fmt"
	"sort"
	"strings"
	"time"
)

// AggregationType defines supported aggregation functions.
type AggregationType string

const (
	// Basic aggregations
	AggSum   AggregationType = "sum"
	AggAvg   AggregationType = "avg"
	AggMax   AggregationType = "max"
	AggMin   AggregationType = "min"
	AggCount AggregationType = "count"

	// Percentile aggregations
	AggP50 AggregationType = "p50"
	AggP90 AggregationType = "p90"
	AggP95 AggregationType = "p95"
	AggP99 AggregationType = "p99"

	// Rate and delta aggregations
	AggRate  AggregationType = "rate"
	AggDelta AggregationType = "delta"
	AggLast  AggregationType = "last"
)

// DataPoint represents a single time-series data point.
type DataPoint struct {
	// Timestamp is when this data point was recorded.
	Timestamp time.Time

	// Value is the metric value at this timestamp.
	Value float64
}

// TimeSeries represents a sequence of data points over time.
// Note: This type is not thread-safe. Concurrency control should be
// handled by the containing cache layer.
type TimeSeries struct {
	// Metric is the name of the metric.
	Metric string

	// Labels are the label key-value pairs identifying this time series.
	Labels map[string]string

	// Points are the data points in chronological order.
	Points []DataPoint
}

// NewTimeSeries creates a new TimeSeries with the given metric name and labels.
func NewTimeSeries(metric string, labels map[string]string) *TimeSeries {
	return &TimeSeries{
		Metric: metric,
		Labels: labels,
		Points: make([]DataPoint, 0),
	}
}

// AddPoint adds a data point to the time series.
func (ts *TimeSeries) AddPoint(timestamp time.Time, value float64) {
	ts.Points = append(ts.Points, DataPoint{
		Timestamp: timestamp,
		Value:     value,
	})
}

// Latest returns the most recent data point, or nil if empty.
func (ts *TimeSeries) Latest() *DataPoint {
	if len(ts.Points) == 0 {
		return nil
	}
	return &ts.Points[len(ts.Points)-1]
}

// LatestValue returns the most recent value, or 0 if empty.
func (ts *TimeSeries) LatestValue() float64 {
	if len(ts.Points) == 0 {
		return 0
	}
	return ts.Points[len(ts.Points)-1].Value
}

// InWindow returns data points within the specified time window.
// Points are returned where: now - window <= timestamp <= now.
func (ts *TimeSeries) InWindow(window time.Duration) []DataPoint {
	now := time.Now()
	cutoff := now.Add(-window)

	var result []DataPoint
	for _, p := range ts.Points {
		if !p.Timestamp.Before(cutoff) && !p.Timestamp.After(now) {
			result = append(result, p)
		}
	}
	return result
}

// Prune removes data points older than the specified retention period.
func (ts *TimeSeries) Prune(retention time.Duration) {
	cutoff := time.Now().Add(-retention)

	var kept []DataPoint
	for _, p := range ts.Points {
		if !p.Timestamp.Before(cutoff) {
			kept = append(kept, p)
		}
	}
	ts.Points = kept
}

// LabelSetKey returns a string key representing the label set.
// This is used for map keys where labels need to be compared.
func (ts *TimeSeries) LabelSetKey() string {
	return LabelSetToKey(ts.Labels)
}

// LabelSetToKey converts a label map to a deterministic string key.
func LabelSetToKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build key string
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%s", k, labels[k])
	}
	return strings.Join(parts, ",")
}

// MetricValue represents a single metric value at a point in time.
type MetricValue struct {
	// Metric is the name of the metric.
	Metric string

	// Labels are the label key-value pairs identifying this metric.
	Labels map[string]string

	// Value is the metric value.
	Value float64

	// Timestamp is when this value was recorded.
	Timestamp time.Time
}

// TimeSeriesBuffer is a bounded buffer for storing time-series data.
// It automatically prunes old data to stay within the retention period.
// Note: This type is not thread-safe. Concurrency control should be
// handled by the containing cache layer.
type TimeSeriesBuffer struct {
	// Series is the underlying time series.
	Series *TimeSeries

	// Retention is how long to keep data points.
	Retention time.Duration

	// MaxPoints is the maximum number of points to store (0 = unlimited).
	MaxPoints int
}

// NewTimeSeriesBuffer creates a new buffer with the specified retention.
func NewTimeSeriesBuffer(metric string, labels map[string]string, retention time.Duration) *TimeSeriesBuffer {
	return &TimeSeriesBuffer{
		Series:    NewTimeSeries(metric, labels),
		Retention: retention,
		MaxPoints: 0, // unlimited by default
	}
}

// Add adds a data point and prunes old data.
func (b *TimeSeriesBuffer) Add(timestamp time.Time, value float64) {
	b.Series.AddPoint(timestamp, value)
	b.prune()
}

// prune removes old data points based on retention and max points.
func (b *TimeSeriesBuffer) prune() {
	// Prune by retention
	if b.Retention > 0 {
		b.Series.Prune(b.Retention)
	}

	// Prune by max points (keep most recent)
	if b.MaxPoints > 0 && len(b.Series.Points) > b.MaxPoints {
		b.Series.Points = b.Series.Points[len(b.Series.Points)-b.MaxPoints:]
	}
}

// GetTimeSeries returns the underlying time series.
func (b *TimeSeriesBuffer) GetTimeSeries() *TimeSeries {
	return b.Series
}

// AggregatedValue represents a pre-computed aggregated metric value.
type AggregatedValue struct {
	// Value is the aggregated value.
	Value float64

	// AggType is the type of aggregation applied.
	AggType AggregationType

	// Window is the time window over which aggregation was computed.
	Window time.Duration

	// GroupBy is the label set used for grouping.
	GroupBy map[string]string

	// ComputedAt is when this aggregation was computed.
	ComputedAt time.Time

	// PointCount is the number of data points used in aggregation.
	PointCount int
}

// AggregationKey generates a unique key for storing aggregated values.
func AggregationKey(metric string, aggType AggregationType, window time.Duration, groupBy map[string]string) string {
	return fmt.Sprintf("%s:%s:%s:%s", metric, aggType, window, LabelSetToKey(groupBy))
}

// StandardWindows are the default time windows for aggregation.
var StandardWindows = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
}

// StandardAggregations are the default aggregation types computed.
var StandardAggregations = []AggregationType{
	AggSum,
	AggAvg,
	AggMax,
	AggMin,
	AggP95,
	AggP99,
	AggRate,
	AggLast,
}
