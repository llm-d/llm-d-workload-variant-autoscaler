package registration

import (
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/source"
)

// Query name constants for type-safe query references.
const (
	// Saturation queries (per-pod peak metrics over time windows)
	QueryKvCacheUsage = "kv_cache_usage"
	QueryQueueLength  = "queue_length"
)

// RegisterSaturationQueries registers queries used by the saturation analyzer.
func RegisterSaturationQueries(sourceRegistry *source.SourceRegistry) {
	registry := sourceRegistry.Get("prometheus").QueryList()

	// KV cache usage per pod (peak over last minute)
	// Uses max_over_time to catch saturation events between scrapes
	registry.MustRegister(source.QueryTemplate{
		Name:        QueryKvCacheUsage,
		Type:        source.QueryTypePromQL,
		Template:    `max by (pod) (max_over_time(vllm:kv_cache_usage_perc{namespace="{{.namespace}}",model_name="{{.modelID}}"}[1m]))`,
		Params:      []string{source.ParamNamespace, source.ParamModelID},
		Description: "Peak KV cache utilization per pod (0.0-1.0) over last minute",
	})

	// Queue length per pod (peak over last minute)
	// Uses max_over_time to catch burst traffic
	registry.MustRegister(source.QueryTemplate{
		Name:        QueryQueueLength,
		Type:        source.QueryTypePromQL,
		Template:    `max by (pod) (max_over_time(vllm:num_requests_waiting{namespace="{{.namespace}}",model_name="{{.modelID}}"}[1m]))`,
		Params:      []string{source.ParamNamespace, source.ParamModelID},
		Description: "Peak queue length per pod over last minute",
	})

}
