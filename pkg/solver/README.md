# Solver Package

The `solver` package provides optimization algorithms for resource allocation decisions in the Workload-Variant-Autoscaler.

## Overview

The solver package implements algorithms that determine optimal resource allocations for inference workloads. Given system constraints, model requirements, and available accelerators, the solver calculates the best allocation strategy to minimize cost while meeting SLO requirements.

## Architecture

```
┌─────────────────┐
│   System State  │ (Models, Accelerators, Constraints)
└────────┬────────┘
         │
         ↓
┌─────────────────┐
│  Optimizer      │ (High-level orchestration)
└────────┬────────┘
         │
         ↓
┌─────────────────┐
│  Solver         │ (Allocation algorithms)
└────────┬────────┘
         │
         ↓
┌─────────────────┐
│  Allocations    │ (Optimized assignments)
└─────────────────┘
```

## Key Components

### Optimizer (`optimizer.go`)

High-level interface for optimization operations.

```go
type Optimizer interface {
    Optimize(system *core.System) ([]*core.Allocation, error)
}
```

**Implementations:**
- **GreedyOptimizer**: Fast greedy allocation strategy
- **CostOptimizer**: Cost-minimization focused (future)
- **PerformanceOptimizer**: Performance-focused (future)

### Solver (`solver.go`)

Core solving logic for allocation problems.

```go
type Solver struct {
    Config SolverConfig
}

type SolverConfig struct {
    Unlimited        bool              // Ignore capacity constraints
    SaturationPolicy SaturationPolicy  // Policy when capacity saturated
}
```

**Key methods:**
```go
// Solve computes optimal allocations for all models
func (s *Solver) Solve(system *core.System) ([]*core.Allocation, error)

// ValidateAllocation checks if allocation meets requirements
func (s *Solver) ValidateAllocation(alloc *core.Allocation) error
```

### Greedy Algorithm (`greedy.go`)

Fast, priority-based allocation algorithm.

```go
type GreedySolver struct {
    config GreedyConfig
}
```

**Algorithm:**
1. Sort models by priority (highest first)
2. For each model:
   - Calculate minimum replicas to meet SLO
   - Find lowest-cost accelerator that works
   - Allocate resources
   - Check capacity constraints
3. Return allocations

**Time complexity**: O(n × m) where n=models, m=accelerators

## Configuration

### Solver Modes

#### Unlimited Mode (Default)

Allocates resources without capacity constraints. Pods may be Pending if cluster capacity is exceeded.

```go
solver := &Solver{
    Config: SolverConfig{
        Unlimited: true,
    },
}
```

**Use case**: Cloud environments with cluster autoscaler

#### Limited Mode

Respects cluster capacity constraints with saturation policies.

```go
solver := &Solver{
    Config: SolverConfig{
        Unlimited: false,
        SaturationPolicy: SaturationPolicyPriorityRoundRobin,
    },
}
```

**Use case**: Fixed-capacity on-premise clusters

### Saturation Policies

When capacity is exhausted in limited mode:

- **SaturationPolicyNone**: No additional allocation beyond SLO requirements
- **SaturationPolicyPriorityExhaustive**: Allocate exhaustively by priority
- **SaturationPolicyPriorityRoundRobin**: Round-robin within priority groups (recommended)
- **SaturationPolicyRoundRobin**: Round-robin across all models

## Usage Examples

### Basic Optimization

```go
import (
    "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/solver"
    "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/core"
)

// Create system state
system := &core.System{
    Models: []*core.Model{
        {Name: "llama-8b", ServiceClass: premium, Load: heavyLoad},
        {Name: "llama-70b", ServiceClass: standard, Load: lightLoad},
    },
    Accelerators: []*core.Accelerator{
        {Name: "L40S", UnitCost: 10.0, Available: 8},
        {Name: "H100", UnitCost: 25.0, Available: 4},
    },
}

// Create optimizer
optimizer := solver.NewGreedyOptimizer(solver.GreedyConfig{
    Unlimited: true,
})

// Run optimization
allocations, err := optimizer.Optimize(system)
if err != nil {
    log.Fatalf("Optimization failed: %v", err)
}

// Process results
for _, alloc := range allocations {
    log.Printf("Model %s: %d x %s (cost: %.2f)",
        alloc.Model.Name,
        alloc.Replicas,
        alloc.Accelerator.Name,
        alloc.Cost)
}
```

### Limited Mode with Capacity Constraints

```go
// Configure limited mode
optimizer := solver.NewGreedyOptimizer(solver.GreedyConfig{
    Unlimited: false,
    SaturationPolicy: solver.SaturationPolicyPriorityRoundRobin,
})

// System with limited capacity
system := &core.System{
    Models: models,
    Accelerators: []*core.Accelerator{
        {Name: "L40S", UnitCost: 10.0, Available: 4}, // Limited capacity
    },
}

allocations, err := optimizer.Optimize(system)
// Some models may receive fewer replicas due to capacity constraints
```

### Custom Optimizer Implementation

```go
type CustomOptimizer struct {
    solver *solver.Solver
}

func (o *CustomOptimizer) Optimize(system *core.System) ([]*core.Allocation, error) {
    // Custom pre-processing
    models := o.preprocessModels(system.Models)
    
    // Use base solver
    allocations, err := o.solver.Solve(system)
    if err != nil {
        return nil, err
    }
    
    // Custom post-processing
    return o.postprocessAllocations(allocations), nil
}
```

### Allocation Validation

```go
// Validate single allocation
solver := &solver.Solver{}
err := solver.ValidateAllocation(allocation)
if err != nil {
    log.Printf("Invalid allocation: %v", err)
}

// Validate all allocations
for _, alloc := range allocations {
    if err := solver.ValidateAllocation(alloc); err != nil {
        log.Printf("Allocation for %s failed validation: %v", 
            alloc.Model.Name, err)
    }
}
```

## Algorithm Details

### Greedy Algorithm Flow

```
1. Sort models by priority (descending)
   ↓
2. For each model:
   ├─ Query analyzer for minimum replicas needed
   ├─ For each accelerator (sorted by cost):
   │  ├─ Check if accelerator can meet SLO
   │  ├─ Calculate required replicas
   │  └─ Check capacity availability
   ├─ Select lowest-cost valid option
   └─ Create allocation
   ↓
3. Return allocations sorted by priority
```

### Cost Calculation

```go
// Per-allocation cost
cost = replicas × accelerator.UnitCost × timeWindow

// Total system cost
totalCost = Σ allocation.Cost
```

### Capacity Accounting

```go
// Track remaining capacity
for _, alloc := range allocations {
    accelerator := alloc.Accelerator
    accelerator.Available -= alloc.Replicas
    
    if accelerator.Available < 0 {
        // Handle capacity overflow
    }
}
```

## Performance Characteristics

### Greedy Optimizer

- **Time Complexity**: O(n × m × log(n))
  - n = number of models
  - m = number of accelerator types
  - log(n) for priority sorting

- **Space Complexity**: O(n + m)
  - Linear in number of models and accelerators

- **Typical Performance**: <10ms for 100 models, 10 accelerator types

### Optimization Goals

1. **Primary**: Meet all SLO requirements
2. **Secondary**: Minimize total cost
3. **Tertiary**: Respect capacity constraints
4. **Quaternary**: Honor priority ordering

## Integration with Analyzer

The solver integrates with the analyzer package for capacity calculations:

```go
import "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/analyzer"

// Solver uses analyzer to determine minimum replicas
analyzer := analyzer.NewQueueAnalyzer(config)

for _, model := range models {
    // Analyzer calculates minimum replicas to meet SLO
    minReplicas := analyzer.SizeForSLO(
        model.Load,
        model.ServiceClass,
        server,
    )
    
    // Solver uses this in allocation decisions
    allocation.Replicas = minReplicas
}
```

## Testing

The package includes comprehensive tests:

```bash
# Run all solver tests
go test ./pkg/solver/...

# Run with verbose output
go test -v ./pkg/solver/...

# Run specific test
go test ./pkg/solver -run TestGreedySolver

# Run benchmarks
go test ./pkg/solver -bench=.
```

### Test Coverage

- Unit tests for each algorithm
- Integration tests with analyzer package
- Edge cases (empty systems, no capacity, etc.)
- Performance benchmarks

## Extending the Solver

### Adding a New Optimization Algorithm

1. **Implement Optimizer interface**:
```go
type MyOptimizer struct {
    config MyConfig
}

func (o *MyOptimizer) Optimize(system *core.System) ([]*core.Allocation, error) {
    // Your algorithm here
}
```

2. **Add tests**:
```go
func TestMyOptimizer(t *testing.T) {
    // Test cases
}
```

3. **Document algorithm**:
   - Time/space complexity
   - Optimization goals
   - Use cases

### Adding a New Saturation Policy

```go
// Define policy constant
const SaturationPolicyMyPolicy = "MyPolicy"

// Implement in solver
func (s *Solver) applySaturationPolicy(/* params */) {
    switch s.Config.SaturationPolicy {
    case SaturationPolicyMyPolicy:
        // Your policy logic
    // ... existing cases
    }
}
```

## Troubleshooting

### No Valid Allocations Found

**Cause**: No accelerator can meet SLO requirements

**Solution**:
- Check accelerator performance parameters (alpha, beta)
- Verify SLO targets are achievable
- Increase available accelerator types
- Relax SLO constraints

### Capacity Exhausted

**Cause**: Limited mode with insufficient capacity

**Solution**:
- Increase available accelerators
- Switch to unlimited mode
- Adjust saturation policy
- Reduce number of models or load

### High Optimization Time

**Cause**: Large number of models or accelerators

**Solution**:
- Profile with `go test -bench=. -cpuprofile=cpu.prof`
- Consider batching optimizations
- Use faster algorithms for large systems

## Related Documentation

- [Core Package](../core/README.md) - Data types used by solver
- [Analyzer Package](../analyzer/README.md) - Capacity calculations
- [Design Documentation](../../docs/design/modeling-optimization.md)

## Future Enhancements

Planned improvements:

1. **Advanced algorithms**:
   - Linear programming solver
   - Genetic algorithm for complex constraints
   - Multi-objective optimization

2. **Performance**:
   - Caching of analyzer results
   - Parallel optimization for independent models
   - Incremental optimization for small changes

3. **Constraints**:
   - Placement constraints (affinity/anti-affinity)
   - Cost budgets
   - Custom constraints

## Contributing

When contributing to the solver package:

1. Maintain algorithm performance characteristics
2. Add comprehensive tests for new algorithms
3. Document complexity and use cases
4. Ensure backward compatibility
5. Update this README with examples

---

**Questions?** See [Developer Guide](../../docs/developer-guide/development.md) or open an issue.
