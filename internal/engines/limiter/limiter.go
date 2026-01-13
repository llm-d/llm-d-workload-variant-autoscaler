package limiter

import (
	"context"
	"fmt"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
)

// Limiter is an interface that defines the method for allocating limited GPU resources among variants
type Limiter interface {
	// Allocate allocates limited GPU resources among variants by updating their respective decisions
	Allocate(
		ctx context.Context,
		decisions []interfaces.VariantDecision,
		vaMap map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
		inventory map[string]map[string]collector.AcceleratorModelInfo,
	) error
}

// LimiterStrategy is an enumeration of the different strategies that can be used by the Limiter
type LimiterStrategy int

// enumeration of LimiterStrategy
const (
	TargetStrategy LimiterStrategy = iota
	SaturationStrategy
)

// NewLimiter is a factory that creates a new Limiter based on the provided strategy
func NewLimiter(strategy LimiterStrategy) (Limiter, error) {
	switch strategy {
	case TargetStrategy:
		return NewTargetLimiter(nil)
	case SaturationStrategy:
		return NewSaturationLimiter()
	default:
		return nil, fmt.Errorf("unsupported limiter strategy: %v", strategy)
	}
}
