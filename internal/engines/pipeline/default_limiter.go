package pipeline

import (
	"context"
	"fmt"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
)

// DefaultLimiter combines an Inventory with an AllocationAlgorithm to constrain
// scaling decisions based on resource availability.
//
// The limiter follows the pipeline pattern:
//  1. Refresh inventory to get latest resource limits from cluster
//  2. Calculate current GPU usage from decisions
//  3. Create allocator with available resources
//  4. Run allocation algorithm to distribute resources
//  5. Update decision metadata (WasLimited, LimitedBy, DecisionSteps)
type DefaultLimiter struct {
	name      string
	inventory Inventory
	algorithm AllocationAlgorithm
}

// NewDefaultLimiter creates a limiter that combines inventory tracking with
// an allocation algorithm.
func NewDefaultLimiter(name string, inventory Inventory, algorithm AllocationAlgorithm) *DefaultLimiter {
	return &DefaultLimiter{
		name:      name,
		inventory: inventory,
		algorithm: algorithm,
	}
}

// Name returns the limiter identifier for logging/metrics.
func (l *DefaultLimiter) Name() string {
	return l.name
}

// Limit applies resource constraints to scaling decisions.
// Modifies decisions in place - may reduce TargetReplicas based on available resources.
func (l *DefaultLimiter) Limit(ctx context.Context, decisions []*interfaces.VariantDecision) error {
	if len(decisions) == 0 {
		return nil
	}

	// Step 1: Refresh inventory to get latest limits from cluster
	if err := l.inventory.Refresh(ctx); err != nil {
		return fmt.Errorf("failed to refresh inventory: %w", err)
	}

	// Step 2: Calculate current GPU usage from decisions
	usedByType := l.calculateUsedGPUs(decisions)
	l.inventory.SetUsed(usedByType)

	// Step 3: Create allocator with available resources
	allocator := l.inventory.CreateAllocator(ctx)

	// Step 4: Run allocation algorithm to distribute resources
	if err := l.algorithm.Allocate(ctx, decisions, allocator); err != nil {
		return fmt.Errorf("allocation algorithm failed: %w", err)
	}

	// Step 5: Update decision metadata
	l.updateDecisionMetadata(decisions)

	return nil
}

// calculateUsedGPUs computes current GPU usage per accelerator type.
// Uses CurrentReplicas * GPUsPerReplica for each decision.
func (l *DefaultLimiter) calculateUsedGPUs(decisions []*interfaces.VariantDecision) map[string]int {
	usedByType := make(map[string]int)
	for _, d := range decisions {
		if d.AcceleratorName == "" {
			continue
		}
		usedByType[d.AcceleratorName] += d.CurrentReplicas * d.GPUsPerReplica
	}
	return usedByType
}

// updateDecisionMetadata sets LimitedBy and adds DecisionSteps.
// Note: WasLimited is set by the algorithm during allocation.
func (l *DefaultLimiter) updateDecisionMetadata(decisions []*interfaces.VariantDecision) {
	for _, d := range decisions {
		// If the algorithm marked the decision as limited, set LimitedBy
		if d.WasLimited {
			d.LimitedBy = l.name
		}

		// Add decision step for observability
		reason := l.buildStepReason(d)
		d.AddDecisionStep(l.name, reason, d.WasLimited)
	}
}

// buildStepReason creates a human-readable reason for the decision step.
func (l *DefaultLimiter) buildStepReason(d *interfaces.VariantDecision) string {
	replicaChange := d.TargetReplicas - d.CurrentReplicas

	if replicaChange <= 0 {
		return fmt.Sprintf("no scale-up (target=%d, current=%d)", d.TargetReplicas, d.CurrentReplicas)
	}
	if d.WasLimited {
		return fmt.Sprintf("limited: allocated %d GPUs for +%d replicas", d.GPUsAllocated, replicaChange)
	}
	return fmt.Sprintf("allocated %d GPUs for +%d replicas", d.GPUsAllocated, replicaChange)
}

// Ensure DefaultLimiter implements Limiter interface
var _ Limiter = (*DefaultLimiter)(nil)
