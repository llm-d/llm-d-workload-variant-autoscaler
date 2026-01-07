# Core Package

The `core` package provides fundamental domain models and business logic for the Workload-Variant-Autoscaler's capacity planning and resource allocation system.

## Overview

This package implements the core concepts used in optimization and resource allocation:

- **Accelerators**: GPU and hardware resource abstractions
- **Service Classes**: Quality of service levels and SLO definitions
- **Models**: LLM model configurations and characteristics
- **Servers**: Inference server representations and configurations
- **Allocations**: Resource assignment decisions
- **Systems**: Complete system state and capacity management

## Key Components

### Accelerator (`accelerator.go`)

Represents compute accelerators (GPUs) with cost and capability information.

```go
type Accelerator struct {
    Name      string    // GPU type (e.g., "L40S", "H100")
    UnitCost  float64   // Cost per unit time
    Available int       // Number of available units
}
```

**Use cases:**
- Cost-aware resource allocation
- Accelerator capability matching
- Capacity tracking and planning

### ServiceClass (`serviceclass.go`)

Defines SLO requirements and QoS levels for inference workloads.

```go
type ServiceClass struct {
    Name     string
    Priority int       // Higher priority gets preference
    TTFT     float64   // Time To First Token target (ms)
    ITL      float64   // Inter-Token Latency target (ms)
    TPS      float64   // Tokens Per Second target
}
```

**Use cases:**
- SLO validation and enforcement
- Priority-based resource allocation
- Performance target specification

### Model (`model.go`)

Encapsulates LLM model characteristics and requirements.

```go
type Model struct {
    Name         string
    ServiceClass *ServiceClass
    Load         LoadProfile
    MinReplicas  int
    MaxReplicas  int
}

type LoadProfile struct {
    ArrivalRate     float64  // Requests per second
    AvgInputTokens  float64  // Average prompt length
    AvgOutputTokens float64  // Average completion length
}
```

**Use cases:**
- Model-specific capacity planning
- Load characterization
- Scaling boundary enforcement

### Server (`server.go`)

Represents inference server instances with performance characteristics.

```go
type Server struct {
    Name        string
    Accelerator *Accelerator
    Model       *Model
    MaxBatch    int
    // Performance parameters for queueing model
    Alpha       float64  // Base ITL
    Beta        float64  // ITL per batch size
}
```

**Use cases:**
- Server capacity calculation
- Performance modeling
- Batch size optimization

### Allocation (`allocation.go`)

Represents resource assignment decisions from the optimizer.

```go
type Allocation struct {
    Model       *Model
    Accelerator *Accelerator
    Replicas    int
    MaxBatch    int
    Cost        float64
    // Performance metrics
    TTFT        float64
    ITL         float64
    TPS         float64
}
```

**Use cases:**
- Optimization results representation
- Cost tracking
- Performance validation

### System (`system.go`)

Manages complete system state including all models, servers, and allocations.

```go
type System struct {
    Models       []*Model
    Accelerators []*Accelerator
    Servers      []*Server
    Allocations  []*Allocation
}
```

**Use cases:**
- Global resource management
- System-wide optimization
- Capacity accounting

## Usage Examples

### Creating Accelerator Definitions

```go
import "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/core"

// Define available accelerators
l40s := &core.Accelerator{
    Name:      "L40S",
    UnitCost:  10.0,
    Available: 8,
}

h100 := &core.Accelerator{
    Name:      "H100",
    UnitCost:  25.0,
    Available: 4,
}
```

### Defining Service Classes

```go
// Premium service class with strict SLOs
premium := &core.ServiceClass{
    Name:     "premium",
    Priority: 100,
    TTFT:     100.0,  // 100ms max TTFT
    ITL:      50.0,   // 50ms max ITL
    TPS:      100.0,  // 100 tokens/sec min
}

// Standard service class with relaxed SLOs
standard := &core.ServiceClass{
    Name:     "standard",
    Priority: 50,
    TTFT:     500.0,
    ITL:      100.0,
    TPS:      50.0,
}
```

### Creating Model Configurations

```go
// Model with load profile
llama8b := &core.Model{
    Name:         "meta-llama/Llama-3.1-8B",
    ServiceClass: premium,
    Load: core.LoadProfile{
        ArrivalRate:     10.0,  // 10 req/sec
        AvgInputTokens:  512,
        AvgOutputTokens: 128,
    },
    MinReplicas: 1,
    MaxReplicas: 10,
}
```

### Building Server Configurations

```go
// Server configuration for Llama-8B on L40S
server := &core.Server{
    Name:        "llama8b-l40s",
    Accelerator: l40s,
    Model:       llama8b,
    MaxBatch:    32,
    Alpha:       2.5,   // Base ITL (ms)
    Beta:        0.8,   // ITL slope (ms per batch)
}
```

### Creating and Evaluating Allocations

```go
// Create allocation
allocation := &core.Allocation{
    Model:       llama8b,
    Accelerator: l40s,
    Replicas:    3,
    MaxBatch:    32,
}

// Calculate cost
cost := allocation.CalculateCost()

// Validate against SLO
if allocation.TTFT <= llama8b.ServiceClass.TTFT {
    // Allocation meets SLO
}
```

### System-Level Management

```go
// Initialize system
system := &core.System{
    Models:       []*core.Model{llama8b, llama70b},
    Accelerators: []*core.Accelerator{l40s, h100},
}

// Add servers
system.AddServer(server1)
system.AddServer(server2)

// Track allocations
system.Allocations = append(system.Allocations, allocation)

// Calculate total cost
totalCost := system.TotalCost()

// Check capacity constraints
hasCapacity := system.HasAvailableCapacity()
```

## Design Principles

1. **Immutability**: Core types are designed to be immutable where possible
2. **Composability**: Types compose naturally for complex scenarios
3. **Type Safety**: Strong typing prevents invalid configurations
4. **Clarity**: Clear naming and simple interfaces
5. **Testability**: Easy to create test fixtures and mock objects

## Integration Points

The core package is used by:

- **pkg/solver**: Optimization algorithms consume core types
- **pkg/analyzer**: Queue analyzers work with Server and Model types
- **internal/optimizer**: Orchestrates core type manipulation
- **internal/controller**: Converts Kubernetes CRs to core types

## Testing

The package includes comprehensive unit tests. Run tests with:

```bash
go test ./pkg/core/...
```

See `*_test.go` files for usage examples and test patterns.

## Related Documentation

- [Solver Package](../solver/README.md) - Optimization algorithms
- [Analyzer Package](../analyzer/README.md) - Queueing models
- [Design Documentation](../../docs/design/modeling-optimization.md)

## Contributing

When adding new core types:

1. Keep types simple and focused
2. Add comprehensive unit tests
3. Document public APIs with godoc comments
4. Update this README with usage examples
5. Ensure backward compatibility or document breaking changes
