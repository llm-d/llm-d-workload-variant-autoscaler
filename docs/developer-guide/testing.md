# Testing Guide

Comprehensive guide for testing the Workload-Variant-Autoscaler (WVA).

## Overview

WVA has a multi-layered testing strategy:

1. **Unit Tests** - Fast, isolated tests for individual packages and functions
2. **Integration Tests** - Tests for component interactions within the controller
3. **E2E Tests (Saturation-Based)** - Full system tests with emulated infrastructure on Kind
4. **E2E Tests (OpenShift)** - Real-world tests with actual vLLM deployments on OpenShift

## Unit Tests

### Running Unit Tests

```bash
# Run all unit tests
make test

# Run with coverage report
go test -cover ./...

# Run specific package
go test ./internal/optimizer/...
go test ./pkg/analyzer/...
go test ./internal/collector/prometheus/...

# Run with verbose output
go test -v ./internal/controller/...

# Generate HTML coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

### Unit Test Structure

Unit tests are co-located with the code they test:

```
internal/
├── controller/
│   ├── variantautoscaling_controller.go
│   └── variantautoscaling_controller_test.go
├── optimizer/
│   ├── optimizer.go
│   └── optimizer_test.go
├── collector/
│   ├── cache/
│   │   ├── cache.go
│   │   ├── cache_test.go
│   │   ├── memory_cache.go
│   │   └── noop_cache.go
│   ├── config/
│   │   ├── config.go
│   │   └── config_test.go
│   ├── prometheus/
│   │   ├── prometheus_collector.go
│   │   ├── prometheus_collector_test.go
│   │   ├── background_fetching.go
│   │   ├── background_fetching_test.go
│   │   ├── cache_operations.go
│   │   └── saturation_metrics.go
│   ├── collector.go
│   └── factory.go
└── saturation/
    ├── analyzer.go
    └── analyzer_test.go
```

### Writing Unit Tests

Example unit test structure:

```go
package optimizer_test

import (
    "testing"
    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"
)

func TestOptimizer(t *testing.T) {
    RegisterFailHandler(Fail)
    RunSpecs(t, "Optimizer Suite")
}

var _ = Describe("Optimizer", func() {
    Context("when optimizing single variant", func() {
        It("should calculate optimal replicas", func() {
            // Test implementation
            Expect(result).To(Equal(expected))
        })
    })
})
```

### Unit Test Best Practices

- **Use table-driven tests** for testing multiple scenarios
- **Mock external dependencies** (Kubernetes API, Prometheus, etc.)
- **Test edge cases** (zero values, negative numbers, nil pointers, etc.)
- **Keep tests fast** - unit tests should run in milliseconds
- **Use descriptive test names** - clearly state what is being tested
- **Follow AAA pattern** - Arrange, Act, Assert

## Integration Tests

Integration tests validate component interactions within the controller using envtest.

### Running Integration Tests

```bash
# Run integration tests (included in make test)
make test

# Run only controller integration tests
go test ./internal/controller/... -v
```

### envtest Setup

Integration tests use controller-runtime's envtest, which provides a real Kubernetes API server for testing:

```go
var _ = BeforeSuite(func() {
    testEnv = &envtest.Environment{
        CRDDirectoryPaths: []string{
            filepath.Join("..", "..", "config", "crd", "bases"),
        },
    }
    
    cfg, err := testEnv.Start()
    Expect(err).NotTo(HaveOccurred())
    
    k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
    Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
    Expect(testEnv.Stop()).To(Succeed())
})
```

## End-to-End Tests

WVA provides two E2E test suites for different testing scenarios.

### Saturation-Based E2E Tests (Kind)

**Purpose**: Fast, isolated testing with emulated infrastructure  
**Location**: `test/e2e-saturation-based/`  
**Environment**: Local Kind cluster with GPU emulation  
**Duration**: ~15-25 minutes

#### Quick Start

```bash
# Run all saturation-based E2E tests
make test-e2e

# Run specific test suite
make test-e2e FOCUS="Single VariantAutoscaling"
make test-e2e FOCUS="Multiple VariantAutoscalings"

# Run specific test case
make test-e2e FOCUS="should scale up when saturation is detected"
```

#### What These Tests Validate

- ✅ Saturation detection via KV cache and queue metrics
- ✅ Scale-up recommendations based on thresholds
- ✅ HPA integration with `inferno_desired_replicas` metric
- ✅ Multi-variant scaling with cost optimization
- ✅ Deployment scaling and replica stabilization

#### Prerequisites

- Docker installed and running
- Kind (installed automatically if missing)
- 8GB+ RAM, 4+ CPU cores recommended
- **No GPU hardware required** - uses emulation

#### Test Environment

The test suite automatically creates:
- Kind cluster with 3 nodes and emulated GPUs
- WVA controller
- Emulated llm-d infrastructure
- Prometheus monitoring stack
- Test deployments with VariantAutoscaling resources

#### Test Configuration

Key configuration constants in `test/e2e-saturation-based/e2e_saturation_test.go`:

```go
// Load parameters
loadRatePerSecond   = 8    // requests/second
inputTokens         = 128  // tokens per request
outputTokens        = 128  // tokens per request

// Saturation thresholds
KvCacheThreshold     = 0.7  // 70% KV-cache utilization
QueueLengthThreshold = 10.0 // 10+ queued requests

// Cost configuration (multi-variant tests)
h100Cost = 50.0  // H100: $50/hr
a100Cost = 30.0  // A100: $30/hr
```

See the [Saturation-Based E2E Tests README](../../test/e2e-saturation-based/README.md) for comprehensive documentation.

### OpenShift E2E Tests

**Purpose**: Real-world validation with actual vLLM deployments  
**Location**: `test/e2e-openshift/`  
**Environment**: OpenShift cluster with GPU hardware  
**Duration**: ~30-45 minutes (depending on cluster and load)

#### Quick Start

```bash
# Set KUBECONFIG to your OpenShift cluster
export KUBECONFIG=/path/to/kubeconfig

# Run all OpenShift E2E tests
make test-e2e-openshift

# Run with custom configuration
make test-e2e-openshift \
  LLMD_NAMESPACE=llmd-stack \
  DEPLOYMENT=my-vllm-deployment \
  REQUEST_RATE=20 \
  NUM_PROMPTS=3000
```

#### What These Tests Validate

- ✅ Real vLLM deployment scaling under actual load
- ✅ ShareGPT dataset load generation
- ✅ Production-like HPA integration
- ✅ Prometheus metrics collection
- ✅ Multi-namespace operation

#### Prerequisites

- OpenShift cluster (OCP 4.12+) with GPU nodes
- `oc` CLI configured and authenticated
- Cluster admin permissions
- Pre-deployed infrastructure:
  - WVA controller
  - llm-d infrastructure
  - Prometheus/Thanos
  - vLLM deployment

#### Test Parameters

Configurable via environment variables or Makefile:

```bash
CONTROLLER_NAMESPACE=workload-variant-autoscaler-system
LLMD_NAMESPACE=llm-d-inference-scheduling
GATEWAY_NAME=infra-inference-scheduling-inference-gateway
MODEL_ID=unsloth/Meta-Llama-3.1-8B
DEPLOYMENT=ms-inference-scheduling-llm-d-modelservice-decode
REQUEST_RATE=20        # Requests per second
NUM_PROMPTS=3000       # Total prompts to generate
```

See the [OpenShift E2E Tests README](../../test/e2e-openshift/README.md) for comprehensive documentation.

## Test Comparison Matrix

| Aspect | Unit Tests | Integration Tests | Saturation E2E (Kind) | OpenShift E2E |
|--------|-----------|-------------------|----------------------|---------------|
| **Speed** | ⚡ Fast (<1min) | 🚀 Fast (1-3min) | 🏃 Medium (15-25min) | 🐢 Slow (30-45min) |
| **Isolation** | ✅ Complete | ⚠️ Partial | ✅ Complete | ❌ Shared cluster |
| **GPU Required** | ❌ No | ❌ No | ❌ No (emulated) | ✅ Yes |
| **Infrastructure** | ❌ None | 🔧 envtest | 🐳 Kind cluster | ☁️ OpenShift cluster |
| **Realism** | ⭐ Low | ⭐⭐ Medium | ⭐⭐⭐ High | ⭐⭐⭐⭐ Production-like |
| **CI-Friendly** | ✅ Yes | ✅ Yes | ✅ Yes | ⚠️ Requires cluster |
| **Local Dev** | ✅ Yes | ✅ Yes | ✅ Yes | ⚠️ Cluster access needed |

## Continuous Integration

### GitHub Actions Workflows

WVA uses GitHub Actions for automated testing:

#### PR Checks Workflow

**File**: `.github/workflows/ci-pr-checks.yaml`

Runs on every pull request:
- Linting (golangci-lint)
- Unit tests
- Build verification
- Code coverage reporting

#### E2E Saturation Tests Workflow

**File**: `.github/workflows/ci-e2e-saturation.yaml` (if exists)

Runs saturation-based E2E tests on Kind:
- Triggered on PR or manual workflow dispatch
- Creates isolated Kind cluster
- Runs full E2E test suite
- Cleans up resources after completion

#### OpenShift E2E Tests Workflow

**File**: `.github/workflows/ci-e2e-openshift.yaml`

Runs OpenShift E2E tests on dedicated cluster:
- Triggered manually or on specific labels
- Deploys PR-specific namespaces
- Runs multi-model tests
- Leaves resources for debugging on failure

### Running CI Tests Locally

#### Simulate PR Checks

```bash
# Run linter
make lint

# Run unit tests
make test

# Build binary
make build

# Build Docker image
make docker-build
```

#### Simulate E2E CI

```bash
# Run saturation-based E2E (matches CI)
make test-e2e

# Run with specific Kubernetes version
export K8S_VERSION=v1.31.0
make test-e2e
```

## Testing Best Practices

### General Guidelines

1. **Write tests first** (TDD approach) - helps design better APIs
2. **Test behavior, not implementation** - tests should survive refactoring
3. **Keep tests independent** - tests should not depend on each other
4. **Use meaningful assertions** - prefer specific matchers over generic equality
5. **Clean up resources** - always clean up in AfterEach/AfterAll blocks
6. **Document complex tests** - add comments explaining non-obvious test logic

### Ginkgo/Gomega Patterns

#### Use Descriptive Test Names

```go
// ✅ Good
It("should recommend scale-up when KV cache exceeds 70% threshold", func() {
    // ...
})

// ❌ Bad
It("should work", func() {
    // ...
})
```

#### Use Eventually for Async Operations

```go
// ✅ Good - waits for condition to become true
Eventually(func(g Gomega) {
    va := &v1alpha1.VariantAutoscaling{}
    err := k8sClient.Get(ctx, key, va)
    g.Expect(err).NotTo(HaveOccurred())
    g.Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 2))
}, timeout, interval).Should(Succeed())

// ❌ Bad - may fail due to timing
va := &v1alpha1.VariantAutoscaling{}
k8sClient.Get(ctx, key, va)
Expect(va.Status.DesiredOptimizedAlloc.NumReplicas).To(BeNumerically(">=", 2))
```

#### Use Consistently for Stable State

```go
// Verify replicas remain stable for 30 seconds
Consistently(func(g Gomega) {
    deploy := &appsv1.Deployment{}
    err := k8sClient.Get(ctx, key, deploy)
    g.Expect(err).NotTo(HaveOccurred())
    g.Expect(*deploy.Spec.Replicas).To(Equal(int32(2)))
}, 30*time.Second, 5*time.Second).Should(Succeed())
```

#### Use Ordered for Sequential Tests

```go
var _ = Describe("Scale-up workflow", Ordered, func() {
    // These tests run in order and share state
    It("should create resources", func() { /* ... */ })
    It("should detect saturation", func() { /* ... */ })
    It("should scale up", func() { /* ... */ })
})
```

### Test Organization

#### Use Contexts for Grouping

```go
var _ = Describe("Optimizer", func() {
    Context("with single variant", func() {
        It("should optimize for cost", func() { /* ... */ })
        It("should meet SLO requirements", func() { /* ... */ })
    })
    
    Context("with multiple variants", func() {
        It("should prefer cheaper variant", func() { /* ... */ })
        It("should distribute load evenly", func() { /* ... */ })
    })
})
```

#### Use BeforeEach/AfterEach for Setup/Teardown

```go
var _ = Describe("Controller", func() {
    var (
        namespace string
        cleanup   func()
    )
    
    BeforeEach(func() {
        namespace = "test-" + randomString()
        // Setup test resources
    })
    
    AfterEach(func() {
        // Clean up test resources
        if cleanup != nil {
            cleanup()
        }
    })
    
    It("should reconcile resources", func() {
        // Test implementation
    })
})
```

## Debugging Tests

### Debugging Unit Tests

```bash
# Run with verbose output
go test -v ./internal/optimizer/...

# Run specific test
go test -v ./internal/optimizer/... -run TestOptimizer/should_optimize

# Enable Ginkgo trace
go test -v ./pkg/analyzer/... -ginkgo.trace

# Run with debugger (delve)
dlv test ./internal/controller/... -- -ginkgo.v
```

### Debugging E2E Tests

#### View Test Logs

```bash
# Saturation-based tests
go test ./test/e2e-saturation-based/ -v -ginkgo.v

# OpenShift tests
go test ./test/e2e-openshift/ -v -ginkgo.v -timeout 45m
```

#### Access Test Cluster

```bash
# For Kind E2E tests
export KUBECONFIG=$(kind get kubeconfig-path --name wva-e2e)
kubectl get pods -A
kubectl logs -n workload-variant-autoscaler-system deployment/workload-variant-autoscaler-controller-manager

# For OpenShift E2E tests
oc get pods -A
oc logs -n workload-variant-autoscaler-system deployment/workload-variant-autoscaler-controller-manager
```

#### Keep Cluster Alive After Failure

```bash
# Saturation-based tests - manually inspect after failure
make test-e2e
# On failure, cluster remains available
export KUBECONFIG=/tmp/wva-e2e-*/kubeconfig
kubectl get all -A

# Clean up manually when done
kind delete cluster --name wva-e2e
```

### Common Test Failures

#### Test Times Out

**Symptoms**: Test hangs or exceeds timeout

**Possible causes**:
- Controller stuck in reconciliation loop
- HPA not reading metrics
- Prometheus not scraping metrics
- Resource quotas preventing pod creation

**Debugging steps**:
```bash
kubectl get events -A --sort-by='.lastTimestamp'
kubectl describe va -n <namespace>
kubectl logs -n workload-variant-autoscaler-system deployment/workload-variant-autoscaler-controller-manager
```

#### Metrics Not Available

**Symptoms**: External metrics API returns empty or error

**Possible causes**:
- Prometheus adapter not running
- Metrics not being scraped
- Incorrect metric labels or selectors

**Debugging steps**:
```bash
# Check external metrics API
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/inferno_desired_replicas" | jq

# Check Prometheus
kubectl port-forward -n workload-variant-autoscaler-monitoring svc/prometheus-operated 9090:9090
# Query: inferno_desired_replicas{variant_name="..."}
```

#### Deployment Not Scaling

**Symptoms**: HPA shows desired replicas but deployment doesn't scale

**Possible causes**:
- Resource constraints (CPU/memory/GPU)
- Node capacity exceeded
- PDB preventing scale-up
- Deployment controller issues

**Debugging steps**:
```bash
kubectl describe hpa -n <namespace>
kubectl describe deploy -n <namespace>
kubectl get events -n <namespace> --sort-by='.lastTimestamp'
kubectl top nodes
```

## Performance Testing

### Load Testing

For load testing, use the provided load generation jobs:

```bash
# Low load (should stay at 1 replica)
REQUEST_RATE=8 NUM_PROMPTS=2000 make test-e2e-openshift

# Medium load (should scale to 2 replicas)
REQUEST_RATE=20 NUM_PROMPTS=3000 make test-e2e-openshift

# Heavy load (may scale to 3+ replicas)
REQUEST_RATE=40 NUM_PROMPTS=5000 make test-e2e-openshift
```

### Stress Testing

Test system behavior under extreme conditions:
- High request rates (50+ req/s)
- Long-running load (30+ minutes)
- Rapid load changes
- Multiple concurrent variants

## Test Coverage Goals

Current coverage targets:
- **Unit tests**: 70%+ code coverage
- **Integration tests**: All controller operations
- **E2E tests**: Critical user workflows

### Checking Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...

# View summary
go tool cover -func=coverage.out

# Generate HTML report
go tool cover -html=coverage.out -o coverage.html

# View in browser
open coverage.html  # macOS
xdg-open coverage.html  # Linux
```

## Contributing Tests

When contributing, please ensure:

1. ✅ **All new code has unit tests** - aim for 70%+ coverage
2. ✅ **Critical paths have integration tests** - especially controller logic
3. ✅ **New features have E2E tests** - validate end-to-end behavior
4. ✅ **Tests are documented** - explain what is being tested and why
5. ✅ **Tests follow naming conventions** - use descriptive names
6. ✅ **Tests clean up resources** - no resource leaks in tests
7. ✅ **Tests pass locally before pushing** - run `make test` and `make test-e2e`

## Related Documentation

- [Development Guide](development.md) - Development environment setup
- [Saturation-Based E2E Tests README](../../test/e2e-saturation-based/README.md) - Kind E2E tests
- [OpenShift E2E Tests README](../../test/e2e-openshift/README.md) - OpenShift E2E tests
- [Contributing Guide](../../CONTRIBUTING.md) - Contribution guidelines
