package limiter

import (
	"context"
	"fmt"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
)

// SaturationLimiter implements the Limiter interface using a saturation strategy
type SaturationLimiter struct {
}

// SaturationLimiterConfig holds the configuration for the SaturationLimiter
type SaturationLimiterConfig struct {
	LimiterConfig
}

// Allocate allocates limited GPU resources among variants by updating their respective decisions based on a saturation strategy
func NewSaturationLimiter(config *SaturationLimiterConfig) (*SaturationLimiter, error) {
	if config == nil {
		return nil, fmt.Errorf("saturation limiter config cannot be nil")
	}
	return &SaturationLimiter{}, nil
}

// TODO: Implement the saturation strategy for allocating limited GPU resources among variants.
// This involves calculating the saturation level of each variant and adjusting their decisions accordingly.

// Allocate allocates limited GPU resources among variants by updating their respective decisions based on a saturation strategy
func (l *SaturationLimiter) Allocate(
	ctx context.Context,
	decisions []interfaces.VariantDecision,
	inventory map[string]map[string]collector.AcceleratorModelInfo,
) error {
	return nil
}
