// Package optimizer provides global optimization for VariantAutoscaling resources.
//
// The optimizer package implements multi-variant optimization logic that allocates
// resources across multiple model variants to meet SLOs while minimizing cost.
// It integrates with the pkg/solver package for constraint-based optimization.
//
// # Architecture
//
// The optimizer operates in two modes:
//
//  1. Capacity mode: Simple saturation-based scaling (current default)
//     - Each variant is optimized independently based on saturation thresholds
//     - No global resource constraints
//     - Fast and predictable scaling behavior
//
//  2. Hybrid mode: Multi-variant optimization with capacity constraints (experimental)
//     - Uses queueing theory models (M/M/1, M/G/1) for performance prediction
//     - Global optimization across all variants
//     - Considers cluster capacity and cost constraints
//     - Requires integration with llm-d infrastructure
//
// # Capacity Mode (Default)
//
// Capacity mode is the current production mode. Each VariantAutoscaling resource
// is analyzed independently using saturation metrics:
//
//	desired_replicas = ceil(current_replicas * saturation / target_saturation)
//
// Where saturation is measured by:
//   - KV cache utilization (primary signal)
//   - Queue depth (secondary signal)
//   - Request rate trends
//
// Capacity mode is enabled when wva.experimentalHybridOptimization=off (default).
//
// # Hybrid Mode (Experimental)
//
// Hybrid mode enables global multi-variant optimization:
//
//	optimize:
//	  minimize: total_cost
//	  subject to:
//	    - TTFT[variant_i] <= SLO_TTFT[variant_i]  for all i
//	    - ITL[variant_i] <= SLO_ITL[variant_i]    for all i
//	    - sum(replicas[i] * gpu_count[i]) <= cluster_capacity
//
// Enable via Helm:
//
//	helm upgrade wva ./charts/workload-variant-autoscaler \
//	  --set wva.experimentalHybridOptimization=on
//
// Requirements:
//   - llm-d infrastructure deployed
//   - Model parameters configured in VariantAutoscaling.spec.modelProfile
//   - Accelerator cost data in ConfigMap
//
// Note: Hybrid mode is experimental and not recommended for production use.
// See docs/design/modeling-optimization.md for algorithm details.
//
// # VariantAutoscalingsEngine
//
// The main optimization engine:
//
//	type VariantAutoscalingsEngine struct {
//	    manager *infernoManager.Manager  // Solver integration
//	    system  *inferno.System          // Resource model
//	}
//
// Usage:
//
//	engine := optimizer.NewVariantAutoscalingsEngine(manager, system)
//	optimizedAllocations, err := engine.Optimize(ctx, vaList, analysis)
//
// # Optimization Flow
//
//  1. Collect metrics for all VariantAutoscaling resources
//  2. Run model analysis (queueing theory models)
//  3. Build optimization problem (objective + constraints)
//  4. Solve using greedy or constraint solver
//  5. Return optimized allocations (replicas per variant)
//
// # OptimizedAlloc
//
// The result of optimization:
//
//	type OptimizedAlloc struct {
//	    Accelerator string    // GPU type (e.g., "H100")
//	    NumReplicas int       // Desired replica count
//	    LastRunTime metav1.Time
//	}
//
// This is stored in VariantAutoscaling.status.desiredOptimizedAlloc and
// exposed as wva_desired_replicas metric for HPA/KEDA consumption.
//
// # Cost Optimization
//
// The optimizer considers cost via:
//
//  1. VariantAutoscaling.spec.variantCost: Per-replica cost weight
//  2. Accelerator unit costs: From wva-configmap-accelerator-costs ConfigMap
//
// Example cost calculation:
//
//	total_cost = sum(replicas[i] * variant_cost[i] * accelerator_cost[type[i]])
//
// Higher variantCost values reduce replica allocation (favor cost over latency).
//
// # Integration with Solver
//
// The optimizer uses pkg/solver for constraint-based optimization:
//
//	import "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/solver"
//
//	system := core.NewSystem()
//	manager := manager.NewManager(system)
//	solver := solver.NewGreedySolver()
//	manager.SetSolver(solver)
//
// See pkg/solver/README.md for solver algorithm details.
//
// # Error Handling
//
// Common errors:
//   - ErrNoFeasibleSolution: No allocation satisfies all constraints
//   - ErrInsufficientCapacity: Cluster lacks required GPU resources
//   - ErrModelParametersMissing: Required model profile data not provided
//
// The controller sets OptimizationSuccessful condition based on result.
//
// # Usage Example
//
//	// Capacity mode (default)
//	engine := optimizer.NewVariantAutoscalingsEngine(nil, nil)
//	allocations, err := engine.Optimize(ctx, vaList, analysisResults)
//
//	// Hybrid mode
//	system := core.NewSystem()
//	manager := manager.NewManager(system)
//	engine := optimizer.NewVariantAutoscalingsEngine(manager, system)
//	allocations, err := engine.Optimize(ctx, vaList, analysisResults)
//
// See also:
//   - pkg/solver: Optimization algorithms
//   - pkg/core: Resource and allocation models
//   - internal/engines/saturation: Capacity-mode analyzer
//   - docs/design/modeling-optimization.md: Optimization algorithm documentation
//   - docs/saturation-scaling-config.md: Saturation-based scaling details
package optimizer
