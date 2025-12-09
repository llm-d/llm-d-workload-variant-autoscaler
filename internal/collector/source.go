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
)

// SourceHealthStatus represents the health status of a metric source.
type SourceHealthStatus string

const (
	// SourceHealthy indicates the source is responding normally.
	SourceHealthy SourceHealthStatus = "healthy"

	// SourceDegraded indicates the source is responding but with issues.
	SourceDegraded SourceHealthStatus = "degraded"

	// SourceUnhealthy indicates the source is not responding or erroring.
	SourceUnhealthy SourceHealthStatus = "unhealthy"
)

// SourceHealth contains health information about a metric source.
type SourceHealth struct {
	// Status is the current health status of the source.
	Status SourceHealthStatus

	// LastCheck is the time of the last health check.
	LastCheck time.Time

	// LastSuccess is the time of the last successful query.
	LastSuccess time.Time

	// ConsecutiveFailures is the number of consecutive failed queries.
	ConsecutiveFailures int

	// Message provides additional context about the health status.
	Message string
}

// MetricSource is the interface for pluggable metric sources.
// Implementations include PrometheusSource, KubernetesSource, DirectScrapeSource.
type MetricSource interface {
	// Name returns the unique name of this source (e.g., "prometheus", "kubernetes").
	Name() string

	// SupportedCategories returns the metric categories this source can provide.
	SupportedCategories() []MetricCategory

	// Query performs a range query and returns time-series data.
	// The returned TimeSeries contains data points between start and end.
	Query(ctx context.Context, spec MetricSpec, start, end time.Time) (*TimeSeries, error)

	// QueryInstant performs a point-in-time query and returns a single value.
	QueryInstant(ctx context.Context, spec MetricSpec) (*MetricValue, error)

	// Health returns the current health status of this source.
	Health(ctx context.Context) SourceHealth

	// Close releases any resources held by this source.
	Close() error
}
