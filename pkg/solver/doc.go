// Package solver provides optimization algorithms for resource allocation.
//
// The solver package implements constraint-based optimization for allocating
// GPU resources across multiple LLM inference workloads. It solves the problem
// of minimizing cost while meeting Service Level Objectives (SLOs).
//
// # Problem Formulation
//
// Given:
//   - N model variants with different resource requirements
//   - K accelerator types (H100, A100, L40S, etc.)
//   - Limited cluster capacity
//   - Per-variant SLOs (TTFT, ITL, throughput)
//   - Cost model for each accelerator type
//
// Find:
//   - Optimal replica count for each variant
//   - Accelerator type assignment for each variant
//
// Objective:
//   - Minimize total cost = Σ(replicas[i] × cost[type[i]])
//
// Subject to:
//   - TTFT[i] ≤ TTFT_SLO[i] for all variants i
//   - ITL[i] ≤ ITL_SLO[i] for all variants i
//   - Σ(replicas[i] × gpu_count[i]) ≤ cluster_capacity
//   - replicas[i] ≥ 0 for all variants i
//
// # Solver Implementations
//
// The package provides two solver types:
//
//  1. GreedySolver: Fast heuristic-based allocation
//     - Iteratively assigns resources to variants
//     - Prioritizes variants by urgency (current performance vs. SLO)
//     - O(N × K) time complexity
//     - Finds good solutions quickly, may not be globally optimal
//
//  2. Solver: Incremental optimization with capacity constraints
//     - Considers current allocation and makes incremental changes
//     - Respects cluster capacity limits
//     - Handles allocation differences (scale-up/scale-down)
//     - More conservative than greedy solver
//
// # Solver Interface
//
//	type OptimizerInterface interface {
//	    Solve() error
//	    GetSolution() *Solution
//	}
//
// Both solvers implement this interface for interchangeable use.
//
// # Usage Modes
//
// The solver operates in two modes:
//
//  1. Unlimited Mode (default):
//     - Each variant optimized independently
//     - No global capacity constraint
//     - Simpler, faster, more predictable
//     - Current production mode
//
//  2. Limited Mode (experimental):
//     - Global optimization across all variants
//     - Respects cluster capacity
//     - Requires integration with llm-d infrastructure
//     - Handles degraded mode operations
//
// # GreedySolver Algorithm
//
// The greedy solver uses an urgency-based heuristic:
//
//  1. Calculate urgency for each variant:
//     urgency[i] = max(
//       ttft_violation[i] / ttft_slo[i],
//       itl_violation[i] / itl_slo[i]
//     )
//
//  2. Sort variants by urgency (descending)
//
//  3. For each variant (highest urgency first):
//     a. Find cheapest accelerator that meets SLO
//     b. Calculate minimum replicas needed
//     c. Allocate if capacity available
//     d. Skip if no feasible allocation
//
//  4. Return allocation solution
//
// Time complexity: O(N × K × M) where:
//   - N = number of variants
//   - K = number of accelerator types
//   - M = max replicas per variant
//
// # Solution Format
//
//	type Solution struct {
//	    Spec       map[string]*Allocation  // Variant ID → Allocation
//	    TotalCost  float64
//	    Feasible   bool
//	}
//
//	type Allocation struct {
//	    Accelerator string
//	    NumReplicas int
//	    MaxBatch    int
//	}
//
// # Cost Model
//
// Costs are configured via ConfigMaps:
//
//	# wva-configmap-accelerator-costs
//	accelerator_costs:
//	  H100: 3.0
//	  A100: 2.0
//	  L40S: 1.0
//
// Variant-specific cost weights:
//
//	apiVersion: llmd.ai/v1alpha1
//	kind: VariantAutoscaling
//	spec:
//	  variantCost: "1.5"  # Multiplier on base accelerator cost
//
// Total cost calculation:
//
//	cost[variant] = replicas × accelerator_cost × variant_cost
//
// # Performance Prediction
//
// The solver uses queueing theory models from pkg/analyzer:
//
//	analyzer := analyzer.NewQueueAnalyzer(modelParams)
//	metrics, err := analyzer.Analyze(load, allocation)
//
//	if metrics.TTFT <= slo.TTFT && metrics.ITL <= slo.ITL {
//	    // Allocation meets SLO
//	}
//
// Models supported:
//   - M/M/1: Exponential inter-arrival and service times
//   - M/M/1/k: Finite queue capacity
//   - M/G/1: General service time distribution
//
// See pkg/analyzer/README.md for model details.
//
// # Allocation Differences
//
// The Solver tracks allocation changes:
//
//	type AllocationDiff struct {
//	    PrevAllocation *Allocation
//	    NewAllocation  *Allocation
//	    DiffReplicas   int  // Positive = scale up, negative = scale down
//	}
//
// This enables:
//   - Gradual scale-up/scale-down
//   - Change tracking for metrics
//   - Rollback on failure
//
// # Error Handling
//
// Common solver errors:
//
//	ErrNoFeasibleSolution:      No allocation satisfies all SLOs
//	ErrInsufficientCapacity:    Cluster lacks required resources
//	ErrInvalidConfiguration:    Model parameters missing or invalid
//	ErrPerformancePrediction:   Queueing model analysis failed
//
// These are returned to the controller and reflected in VariantAutoscaling conditions.
//
// # Integration Example
//
//	import (
//	    "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/solver"
//	    "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/core"
//	    "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"
//	)
//
//	// Create system model
//	system := core.NewSystem()
//
//	// Add models with SLOs
//	system.AddModel("llama-8b", core.ModelParams{
//	    SLO_TTFT: 1000,  // ms
//	    SLO_ITL: 10,     // ms
//	})
//
//	// Create solver
//	optimizerSpec := &config.OptimizerSpec{
//	    Mode: config.ModeUnlimited,
//	}
//	solver := solver.NewGreedySolver(system, optimizerSpec)
//
//	// Solve
//	if err := solver.Solve(); err != nil {
//	    // Handle error
//	}
//
//	// Get solution
//	solution := solver.GetSolution()
//	for variantID, allocation := range solution.Spec {
//	    fmt.Printf("%s: %d × %s\n", variantID, allocation.NumReplicas, allocation.Accelerator)
//	}
//
// # Benchmarking
//
// Solver performance characteristics:
//
//	GreedySolver:
//	  - Small problems (N≤10): <1ms
//	  - Medium problems (N≤50): <10ms
//	  - Large problems (N≤100): <100ms
//
//	Solver:
//	  - Incremental changes: <5ms
//	  - Full re-optimization: ~50ms
//
// # Configuration
//
// Solver behavior is configured via OptimizerSpec:
//
//	type OptimizerSpec struct {
//	    Mode           string   // "unlimited" or "limited"
//	    MaxReplicas    int      // Per-variant replica limit
//	    AllowScaleDown bool     // Enable scale-down
//	    CostWeight     float64  // Cost vs. performance tradeoff
//	}
//
// See also:
//   - pkg/core: Resource and allocation models
//   - pkg/analyzer: Queueing theory models for performance prediction
//   - internal/optimizer: Integration with WVA controller
//   - docs/design/modeling-optimization.md: Algorithm design documentation
package solver
