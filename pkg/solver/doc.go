// Package solver implements optimization algorithms for resource allocation in WVA.
//
// The solver package contains algorithms that determine optimal replica counts and
// GPU allocations for inference servers to meet SLO requirements while minimizing cost.
//
// Key Components:
//
//   - Optimizer: High-level optimization interface and orchestration
//   - Greedy: Fast greedy allocation algorithm for capacity planning
//   - Solver: Abstract solver interface for extensibility
//
// Optimization Strategy:
//
// The greedy algorithm uses a capacity-based approach:
//  1. Analyze current server saturation (KV cache, queue depth)
//  2. Calculate slack capacity in existing replicas
//  3. Determine minimal replica count to meet demand
//  4. Apply scaling policies (min/max bounds, step size)
//
// Example usage:
//
//	// Create optimizer with system state
//	opt := solver.NewOptimizer(system, config)
//
//	// Run optimization
//	result, err := opt.Optimize(ctx)
//	if err != nil {
//	    log.Error(err, "optimization failed")
//	    return err
//	}
//
//	// Apply results
//	for _, allocation := range result.Allocations {
//	    log.Info("scaling recommendation",
//	        "model", allocation.Model.ID,
//	        "current", allocation.CurrentReplicas,
//	        "desired", allocation.DesiredReplicas)
//	}
//
// The solver is designed to be:
//   - Fast: Sub-second optimization for typical workloads
//   - Deterministic: Same inputs produce same outputs
//   - Observable: Rich logging and metrics
//   - Extensible: Interface-based for future algorithms
//
// Future algorithms may include:
//   - Linear programming for global optimization
//   - Machine learning-based predictive scaling
//   - Cost-aware multi-objective optimization
package solver
