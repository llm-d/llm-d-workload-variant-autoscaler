package limiter

import (
	"context"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
)

// SaturationLimiter is a struct that implements the Limiter interface using a saturation strategy
type SaturationLimiter struct {
}

// Allocate allocates limited GPU resources among variants by updating their respective decisions based on a saturation strategy
func NewSaturationLimiter() (*SaturationLimiter, error) {
	return &SaturationLimiter{}, nil
}

// TODO: Implement the saturation strategy for allocating limited GPU resources among variants.
// This involves calculating the saturation level of each variant and adjusting their decisions accordingly.

// Allocate allocates limited GPU resources among variants by updating their respective decisions based on a saturation strategy
func (l *SaturationLimiter) Allocate(
	ctx context.Context,
	decisions *[]interfaces.VariantDecision,
	vaMap map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	inventory map[string]map[string]collector.AcceleratorModelInfo,
) error {
	return nil
}
