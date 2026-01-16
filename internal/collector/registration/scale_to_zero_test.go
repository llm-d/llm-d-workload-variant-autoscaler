package registration

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/source"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/source/prometheus"
)

// mockPrometheusAPI implements promv1.API for testing
type mockPrometheusAPI struct {
	queryFunc func(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error)
}

func (m *mockPrometheusAPI) Query(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, query, ts, opts...)
	}
	return nil, nil, nil
}

// Implement remaining API methods as no-ops for interface compliance
func (m *mockPrometheusAPI) AlertManagers(ctx context.Context) (v1.AlertManagersResult, error) {
	return v1.AlertManagersResult{}, nil
}
func (m *mockPrometheusAPI) Alerts(ctx context.Context) (v1.AlertsResult, error) {
	return v1.AlertsResult{}, nil
}
func (m *mockPrometheusAPI) Buildinfo(ctx context.Context) (v1.BuildinfoResult, error) {
	return v1.BuildinfoResult{}, nil
}
func (m *mockPrometheusAPI) CleanTombstones(ctx context.Context) error { return nil }
func (m *mockPrometheusAPI) Config(ctx context.Context) (v1.ConfigResult, error) {
	return v1.ConfigResult{}, nil
}
func (m *mockPrometheusAPI) DeleteSeries(ctx context.Context, matches []string, startTime, endTime time.Time) error {
	return nil
}
func (m *mockPrometheusAPI) Flags(ctx context.Context) (v1.FlagsResult, error) {
	return v1.FlagsResult{}, nil
}
func (m *mockPrometheusAPI) LabelNames(ctx context.Context, matches []string, startTime, endTime time.Time, opts ...v1.Option) ([]string, v1.Warnings, error) {
	return nil, nil, nil
}
func (m *mockPrometheusAPI) LabelValues(ctx context.Context, label string, matches []string, startTime, endTime time.Time, opts ...v1.Option) (model.LabelValues, v1.Warnings, error) {
	return nil, nil, nil
}
func (m *mockPrometheusAPI) Metadata(ctx context.Context, metric, limit string) (map[string][]v1.Metadata, error) {
	return nil, nil
}
func (m *mockPrometheusAPI) QueryExemplars(ctx context.Context, query string, startTime, endTime time.Time) ([]v1.ExemplarQueryResult, error) {
	return nil, nil
}
func (m *mockPrometheusAPI) QueryRange(ctx context.Context, query string, r v1.Range, opts ...v1.Option) (model.Value, v1.Warnings, error) {
	return nil, nil, nil
}
func (m *mockPrometheusAPI) Rules(ctx context.Context) (v1.RulesResult, error) {
	return v1.RulesResult{}, nil
}
func (m *mockPrometheusAPI) Runtimeinfo(ctx context.Context) (v1.RuntimeinfoResult, error) {
	return v1.RuntimeinfoResult{}, nil
}
func (m *mockPrometheusAPI) Series(ctx context.Context, matches []string, startTime, endTime time.Time, opts ...v1.Option) ([]model.LabelSet, v1.Warnings, error) {
	return nil, nil, nil
}
func (m *mockPrometheusAPI) Snapshot(ctx context.Context, skipHead bool) (v1.SnapshotResult, error) {
	return v1.SnapshotResult{}, nil
}
func (m *mockPrometheusAPI) Targets(ctx context.Context) (v1.TargetsResult, error) {
	return v1.TargetsResult{}, nil
}
func (m *mockPrometheusAPI) TargetsMetadata(ctx context.Context, matchTarget, metric, limit string) ([]v1.MetricMetadata, error) {
	return nil, nil
}
func (m *mockPrometheusAPI) TSDB(ctx context.Context, opts ...v1.Option) (v1.TSDBResult, error) {
	return v1.TSDBResult{}, nil
}
func (m *mockPrometheusAPI) WalReplay(ctx context.Context) (v1.WalReplayStatus, error) {
	return v1.WalReplayStatus{}, nil
}

var _ = Describe("RegisterScaleToZeroQueries", func() {
	var (
		ctx      context.Context
		registry *source.SourceRegistry
		mockAPI  *mockPrometheusAPI
	)

	BeforeEach(func() {
		ctx = context.Background()
		registry = source.NewSourceRegistry()
		mockAPI = &mockPrometheusAPI{}
	})

	Context("when prometheus source is registered", func() {
		BeforeEach(func() {
			metricsSource := prometheus.NewPrometheusSource(ctx, mockAPI, prometheus.DefaultPrometheusSourceConfig())
			err := registry.Register("prometheus", metricsSource)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should register the model request count query", func() {
			RegisterScaleToZeroQueries(registry)

			metricsSource := registry.Get("prometheus")
			Expect(metricsSource).NotTo(BeNil())

			query := metricsSource.QueryList().Get(QueryModelRequestCount)
			Expect(query).NotTo(BeNil())
			Expect(query.Name).To(Equal(QueryModelRequestCount))
			Expect(query.Type).To(Equal(source.QueryTypePromQL))
		})
	})

	Context("when prometheus source is not registered", func() {
		It("should not panic", func() {
			Expect(func() {
				RegisterScaleToZeroQueries(registry)
			}).NotTo(Panic())
		})
	})
})

var _ = Describe("CollectModelRequestCount", func() {
	var (
		ctx           context.Context
		registry      *source.SourceRegistry
		mockAPI       *mockPrometheusAPI
		metricsSource source.MetricsSource
	)

	BeforeEach(func() {
		ctx = context.Background()
		registry = source.NewSourceRegistry()
	})

	Context("when metrics are available", func() {
		BeforeEach(func() {
			mockAPI = &mockPrometheusAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error) {
					return &model.Scalar{
						Value:     model.SampleValue(100.5),
						Timestamp: model.TimeFromUnix(time.Now().Unix()),
					}, nil, nil
				},
			}
			metricsSource = prometheus.NewPrometheusSource(ctx, mockAPI, prometheus.DefaultPrometheusSourceConfig())
			err := registry.Register("prometheus", metricsSource)
			Expect(err).NotTo(HaveOccurred())
			RegisterScaleToZeroQueries(registry)
		})

		It("should return the request count", func() {
			count, err := CollectModelRequestCount(ctx, metricsSource, "my-model", "default", 10*time.Minute)

			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(100.5))
		})
	})

	Context("when no metrics are available", func() {
		BeforeEach(func() {
			mockAPI = &mockPrometheusAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error) {
					return model.Vector{}, nil, nil
				},
			}
			metricsSource = prometheus.NewPrometheusSource(ctx, mockAPI, prometheus.DefaultPrometheusSourceConfig())
			err := registry.Register("prometheus", metricsSource)
			Expect(err).NotTo(HaveOccurred())
			RegisterScaleToZeroQueries(registry)
		})

		It("should return 0", func() {
			count, err := CollectModelRequestCount(ctx, metricsSource, "my-model", "default", 10*time.Minute)

			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(0.0))
		})
	})

	Context("when query returns an error", func() {
		BeforeEach(func() {
			mockAPI = &mockPrometheusAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error) {
					return nil, nil, context.DeadlineExceeded
				},
			}
			metricsSource = prometheus.NewPrometheusSource(ctx, mockAPI, prometheus.DefaultPrometheusSourceConfig())
			err := registry.Register("prometheus", metricsSource)
			Expect(err).NotTo(HaveOccurred())
			RegisterScaleToZeroQueries(registry)
		})

		It("should return 0 without error", func() {
			count, err := CollectModelRequestCount(ctx, metricsSource, "my-model", "default", 10*time.Minute)

			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(0.0))
		})
	})

	Context("query parameter formatting", func() {
		var capturedQuery string

		BeforeEach(func() {
			mockAPI = &mockPrometheusAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error) {
					capturedQuery = query
					return &model.Scalar{
						Value:     model.SampleValue(50),
						Timestamp: model.TimeFromUnix(time.Now().Unix()),
					}, nil, nil
				},
			}
			metricsSource = prometheus.NewPrometheusSource(ctx, mockAPI, prometheus.DefaultPrometheusSourceConfig())
			err := registry.Register("prometheus", metricsSource)
			Expect(err).NotTo(HaveOccurred())
			RegisterScaleToZeroQueries(registry)
		})

		It("should format retention period correctly in query", func() {
			_, _ = CollectModelRequestCount(ctx, metricsSource, "test-model", "test-ns", 15*time.Minute)

			Expect(capturedQuery).NotTo(BeEmpty())
			Expect(capturedQuery).To(ContainSubstring("[15m]"))
			Expect(capturedQuery).To(ContainSubstring(`model_name="test-model"`))
			Expect(capturedQuery).To(ContainSubstring(`namespace="test-ns"`))
		})
	})
})
