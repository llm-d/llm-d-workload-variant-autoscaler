package limiter

import (
	"context"
	"fmt"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
)

// Limiter is an interface that defines the method for allocating limited GPU resources among variants
type Limiter interface {
	// Allocate allocates limited GPU resources among variants by updating their respective decisions
	Allocate(
		ctx context.Context,
		decisions []interfaces.VariantDecision,
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

// LimiterConfig holds the configuration for the base Limiter
type LimiterConfig struct {
}

// NewLimiter is a factory that creates a new Limiter based on the provided strategy
func NewLimiter(strategy LimiterStrategy) (Limiter, error) {
	switch strategy {
	case TargetStrategy:
		return NewTargetLimiter(&TargetLimiterConfig{})
	case SaturationStrategy:
		return NewSaturationLimiter(&SaturationLimiterConfig{})
	default:
		return nil, fmt.Errorf("unsupported limiter strategy: %v", strategy)
	}
}
