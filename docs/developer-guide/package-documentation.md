# Package Documentation

This document provides an overview of the Go packages in the Workload-Variant-Autoscaler codebase.

## Package Organization

WVA follows standard Go project layout:

```
workload-variant-autoscaler/
├── api/              # Kubernetes API types (CRDs)
├── cmd/              # Main application entry points
├── internal/         # Private application code
│   ├── actuator/     # Metric publishing and actuation
│   ├── collector/    # Metrics collection from Prometheus
│   ├── controller/   # Kubernetes controller logic
│   ├── engines/      # Scaling engines (saturation, model-based)
│   ├── optimizer/    # Optimization algorithms
│   └── saturation/   # Saturation analysis
├── pkg/              # Public library code
│   ├── analyzer/     # Queue theory models
│   ├── config/       # Configuration management
│   ├── core/         # Core domain models
│   ├── manager/      # High-level orchestration
│   └── solver/       # Allocation optimization
└── test/             # End-to-end tests
```

## API Layer (`api/`)

### `api/v1alpha1`

Defines the Kubernetes Custom Resource Definitions (CRDs) for WVA.

**Key Types:**
- `VariantAutoscaling` - Main CRD for configuring autoscaling
- `VariantAutoscalingSpec` - Desired state specification
- `VariantAutoscalingStatus` - Current observed state
- `Allocation` - Resource allocation details
- `LoadProfile` - Workload characteristics

**Example:**
```go
import llmdv1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"

// Create a VariantAutoscaling resource
va := &llmdv1alpha1.VariantAutoscaling{
    Spec: llmdv1alpha1.VariantAutoscalingSpec{
        ModelID: "meta-llama/Llama-3.1-8B",
        ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
            APIVersion: "apps/v1",
            Kind:       "Deployment",
            Name:       "llama-server",
        },
    },
}
```

**References:**
- [CRD Reference](user-guide/crd-reference.md)
- [API Types Source](../api/v1alpha1/variantautoscaling_types.go)

## Internal Packages (`internal/`)

Internal packages contain implementation details not exposed as public API.

### `internal/controller`

Implements the Kubernetes controller reconciliation loop.

**Key Components:**
- `VariantAutoscalingReconciler` - Main controller reconciler
- Watches VariantAutoscaling, Deployment, and Pod resources
- Handles RBAC, webhooks, and event processing

**Responsibilities:**
1. Detect changes to VariantAutoscaling resources
2. Collect current state from cluster and Prometheus
3. Trigger optimization engine
4. Update VariantAutoscaling status
5. Emit metrics for external autoscalers

**References:**
- [Controller Behavior](design/controller-behavior.md)
- [Controller Source](../internal/controller/variantautoscaling_controller.go)

### `internal/collector`

Collects metrics from Prometheus for optimization.

**Key Components:**
- `Collector` interface - Abstract metrics collection
- `PrometheusCollector` - Prometheus-specific implementation
- Background fetching and caching

**Collected Metrics:**
- Request arrival rate
- KV cache utilization
- Queue depth
- TTFT and ITL latencies
- Input/output token counts

**Configuration:**
```go
import "github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/config"

cfg := &config.CollectorConfig{
    PrometheusURL: "https://prometheus:9090",
    QueryInterval: 30 * time.Second,
    CacheTTL:      60 * time.Second,
}
```

**References:**
- [Prometheus Integration](integrations/prometheus.md)
- [Collector Source](../internal/collector/)

### `internal/engines`

Contains different scaling engines that implement optimization strategies.

#### `internal/engines/saturation`

**Saturation-based scaling engine** (current default).

**Key Features:**
- Analyzes KV cache utilization and queue depth
- Scales based on saturation thresholds
- Provides slack capacity to prevent overload

**Algorithm:**
1. Check if any replica is saturated (KV cache > threshold OR queue depth > threshold)
2. If saturated, scale up by adding replicas
3. If all replicas have significant slack, scale down

**Configuration:**
```yaml
kvCacheUtilizationThreshold: 0.85
queueDepthThreshold: 10
minSlackReplicas: 1
```

**References:**
- [Saturation Scaling Config](saturation-scaling-config.md)
- [Saturation Analyzer](saturation-analyzer.md)
- [Engine Source](../internal/engines/saturation/)

#### `internal/engines/model`

**Model-based scaling engine** (queuing theory approach).

Uses mathematical models (M/M/1, M/M/1/k) to predict latency and throughput.

**Status:** Currently disabled in favor of saturation-based approach.

**References:**
- [Modeling & Optimization](design/modeling-optimization.md)
- [Queue Analyzer](../pkg/analyzer/)

### `internal/actuator`

Publishes optimization results as Prometheus metrics and updates CRD status.

**Key Responsibilities:**
1. Emit `wva_desired_replicas` metric
2. Update VariantAutoscaling status
3. Record optimization events

**Published Metrics:**
```
wva_desired_replicas{deployment="my-server",namespace="default"} 3.0
wva_optimization_timestamp_seconds 1704812400
```

**References:**
- [Metrics & Health Monitoring](metrics-health-monitoring.md)
- [Actuator Source](../internal/actuator/)

### `internal/optimizer`

Coordinates optimization across multiple models and service classes.

**Responsibilities:**
- Load system configuration (accelerators, models, service classes)
- Invoke appropriate scaling engine
- Handle multi-model optimization (when enabled)

**References:**
- [Optimizer Source](../internal/optimizer/)

### `internal/saturation`

Saturation analysis utilities and calculations.

**Key Functions:**
- `AnalyzeSaturation()` - Determine if replicas are saturated
- `CalculateSlackCapacity()` - Compute available headroom
- `RecommendScaling()` - Generate scaling recommendations

**References:**
- [Saturation Analyzer](saturation-analyzer.md)
- [Analyzer Source](../internal/saturation/)

### `internal/utils`

Common utilities for:
- TLS certificate handling
- Prometheus transport
- Allocation calculations
- Variant management

## Public Library Packages (`pkg/`)

Public packages can be imported by external tools.

### `pkg/analyzer`

**Queue theory models for inference server analysis.**

**Supported Models:**
- `MM1KModel` - M/M/1/K finite queue (bounded queue size)
- `MM1ModelStateDependent` - M/M/1 with state-dependent service rate
- `QueueAnalyzer` - High-level analysis interface

**Use Cases:**
- Performance prediction
- Capacity planning
- SLO validation

**Example:**
```go
import "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/analyzer"

qa := analyzer.NewQueueAnalyzer(
    maxBatchSize,
    maxQueueLength,
    prefillTimePerToken,
    decodeTimePerToken,
)

metrics, err := qa.Analyze(
    requestRate,
    avgInputTokens,
    avgOutputTokens,
)
// metrics.AvgWaitTime, metrics.TTFT, metrics.ITL
```

**References:**
- [Queue Analyzer README](../pkg/analyzer/README.md)
- [Parameter Estimation](tutorials/parameter-estimation.md)

### `pkg/config`

Configuration data structures and defaults.

**Key Types:**
- `AcceleratorSpec` - GPU/accelerator specifications
- `ModelSpec` - Inference model characteristics
- `ServiceClassSpec` - SLO requirements
- `OptimizerSpec` - Optimization parameters

**Example:**
```go
import "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"

spec := &config.AcceleratorSpec{
    Name:     "L40S",
    Memory:   48 * 1024, // MB
    UnitCost: 1.28,
    Power: config.PowerProfile{
        Idle: 50,
        Full: 350,
    },
}
```

### `pkg/core`

Core domain models representing the system.

**Key Types:**
- `Accelerator` - GPU/accelerator instance
- `Model` - Inference model
- `Server` - Inference server instance
- `ServiceClass` - Service level objectives
- `Allocation` - Resource allocation to a server
- `System` - Complete system state

**Example:**
```go
import "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/core"

server := core.NewServer("llama-8b", model, serviceClass)
alloc := core.NewAllocation(accelerator, numReplicas, maxBatch)
server.SetAllocation(alloc)
```

**References:**
- [Core Types Source](../pkg/core/)

### `pkg/solver`

Optimization solvers for resource allocation.

**Key Components:**
- `Solver` - Main solver interface
- `GreedySolver` - Greedy allocation algorithm
- `Optimizer` - High-level optimization orchestration

**Algorithm:**
1. Evaluate current allocations
2. Identify servers violating SLOs or over-provisioned
3. Compute minimal-cost allocation meeting all SLOs
4. Generate allocation changes

**Example:**
```go
import "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/solver"

s := solver.NewSolver(optimizerSpec)
err := s.Solve()
// Access optimized allocations
```

**References:**
- [Greedy Solver Source](../pkg/solver/greedy.go)

### `pkg/manager`

High-level orchestration and workflow management.

**Responsibilities:**
- Initialize system components
- Coordinate analyzer, optimizer, and solver
- Manage lifecycle

**References:**
- [Manager Source](../pkg/manager/)

## Command-Line Interface (`cmd/`)

### `cmd/main.go`

Main entry point for the WVA controller.

**Initialization:**
1. Parse command-line flags
2. Set up Kubernetes client
3. Initialize controller manager
4. Register reconcilers
5. Start metrics server
6. Run controller loop

**Flags:**
```
--metrics-bind-address      Metrics server address (default :8443)
--health-probe-bind-address Health probe address (default :8081)
--leader-election           Enable leader election
--zap-log-level            Logging verbosity (0=info, 1=debug, 2=trace)
```

**References:**
- [Main Source](../cmd/main.go)

## Testing (`test/`)

End-to-end test suites.

### `test/e2e`

Standard E2E tests for basic functionality.

### `test/e2e-saturation-based`

Saturation-based scaling E2E tests.

### `test/e2e-openshift`

OpenShift-specific E2E tests.

**References:**
- [Testing Guide](developer-guide/testing.md)
- [E2E README](../test/e2e-saturation-based/README.md)

## Common Patterns

### Adding a New Metric

1. **Define Prometheus query** in `internal/collector/prometheus/`
2. **Add metric to collector interface** in `internal/collector/collector.go`
3. **Update saturation analyzer** in `internal/saturation/analyzer.go`
4. **Use in engine** in `internal/engines/saturation/engine.go`

### Adding a New Scaling Engine

1. **Implement engine interface** in `internal/engines/<name>/engine.go`
2. **Register in optimizer** in `internal/optimizer/optimizer.go`
3. **Add configuration** in ConfigMap
4. **Add tests** in `internal/engines/<name>/engine_test.go`

### Extending the CRD

1. **Update types** in `api/v1alpha1/variantautoscaling_types.go`
2. **Add kubebuilder markers** for validation
3. **Generate CRD**: `make manifests`
4. **Update Helm chart**: `charts/workload-variant-autoscaler/crds/`
5. **Update documentation**: `docs/user-guide/crd-reference.md`

## Development Workflow

### Building

```bash
# Build binary
make build

# Build Docker image
make docker-build IMG=myregistry/wva:latest
```

### Testing

```bash
# Unit tests
make test

# E2E tests
make test-e2e

# Specific package
go test ./internal/collector/...
```

### Code Generation

```bash
# Generate CRDs and RBAC
make manifests

# Generate deepcopy methods
make generate
```

### Running Locally

```bash
# Install CRDs
make install

# Run controller locally
make run
```

**References:**
- [Development Guide](developer-guide/development.md)
- [Contributing](../CONTRIBUTING.md)

## Dependencies

Key external dependencies:

- **controller-runtime** - Kubernetes controller framework
- **client-go** - Kubernetes client library
- **prometheus/client_golang** - Prometheus client
- **logr** - Structured logging
- **ginkgo/gomega** - Testing framework

## Code Style

Follow standard Go conventions:
- `gofmt` for formatting
- `golangci-lint` for linting
- Exported types and functions have doc comments
- Tests use table-driven approach

## Additional Resources

- [Go Documentation](https://pkg.go.dev/github.com/llm-d-incubation/workload-variant-autoscaler)
- [Kubebuilder Book](https://book.kubebuilder.io/)
- [controller-runtime](https://pkg.go.dev/sigs.k8s.io/controller-runtime)

---

**Contributing:** Help improve this documentation by adding examples and clarifications! See [CONTRIBUTING.md](../CONTRIBUTING.md).
