# Saturation-Based E2E Tests

This directory contains end-to-end tests for the Workload-Variant-Autoscaler (WVA) operating in saturation-based scaling mode on Kind clusters with emulated GPU infrastructure.

## Overview

These tests validate the saturation-based autoscaling behavior of WVA using emulated vLLM deployments. Unlike the OpenShift E2E tests in `test/e2e-openshift`, these tests run in an isolated Kind cluster with GPU emulation and simulated llm-d infrastructure, making them ideal for development and CI environments without requiring real GPU hardware.

### What is Saturation-Based Scaling?

Saturation-based scaling monitors real-time inference server metrics to detect resource saturation:
- **KV Cache Utilization**: Percentage of KV-cache blocks currently in use
- **Queue Depth**: Number of requests waiting in the inference server queue

When saturation exceeds configured thresholds, WVA recommends scaling up to maintain performance and meet SLO requirements.

## Test Architecture

The test suite creates a complete testing environment:
1. **Kind cluster** with emulated GPU nodes (configurable GPU types and counts)
2. **WVA controller** deployed in `workload-variant-autoscaler-system` namespace
3. **Emulated llm-d infrastructure** in `llm-d-sim` namespace:
   - Gateway for request routing
   - Emulated vLLM deployments (llm-d-sim) with configurable accelerators
   - Prometheus and monitoring stack
4. **HPA** configured to scale based on WVA recommendations via `inferno_desired_replicas` metric
5. **VariantAutoscaling** resources for test model deployments

## Prerequisites

### System Requirements

- **Go** 1.24.0+
- **Docker** 17.03+
- **Kind** (installed automatically by Makefile if missing)
- **kubectl** 1.32.0+
- **Make**

### Machine Requirements

- **CPU**: 4+ cores recommended
- **Memory**: 8GB+ RAM
- **Disk**: 10GB+ free space
- **No GPU Required**: Tests use emulated GPUs

**Works on Mac (Apple Silicon/Intel), Linux, and Windows WSL2** - no physical GPUs needed!

## Test Structure

### Test Files

- **`e2e_saturation_suite_test.go`**: Test suite setup, Kind cluster creation, and infrastructure deployment
- **`e2e_saturation_test.go`**: Test cases validating saturation-based scaling behavior

### Test Suites

#### 1. Single VariantAutoscaling Test Suite

Tests basic saturation-based scaling with a single model variant:

- **Initial State Verification**: Validates ConfigMap, VariantAutoscaling resource, and HPA configuration
- **Pre-Load Replica Verification**: Confirms deployment scales to expected minimum replicas before load
- **Saturation Detection**: Applies synthetic load and verifies WVA detects saturation via KV cache and queue metrics
- **Scale-Up Validation**: Confirms WVA recommends increased replicas and HPA triggers deployment scaling
- **Stabilization**: Validates deployment maintains scaled replica count under continued load

#### 2. Multiple VariantAutoscaling Test Suite

Tests multi-variant saturation-based scaling with cost optimization:

- **Multi-Variant Setup**: Creates two variants (A100 and H100) of the same model with different accelerators and costs
- **Independent Scaling**: Validates each variant scales independently based on its own saturation metrics
- **Cost-Based Preference**: Verifies WVA prefers cheaper variants (A100 @ $30/hr vs H100 @ $50/hr) when both can serve the same model

## Test Configuration

### Constants

```go
// Load generation parameters
loadRatePerSecond   = 8    // requests per second
avgITL              = 10   // average inter-token latency (ms)
avgTTFT             = 150  // average time to first token (ms)
inputTokens         = 128  // tokens per request input
outputTokens        = 128  // tokens per request output

// Saturation thresholds
KvCacheThreshold     = 0.7  // 70% KV-cache utilization triggers scale-up
QueueLengthThreshold = 10.0 // 10+ queued requests triggers scale-up
kvSpareTrigger       = 0.1  // Scale-up when spare KV capacity < 10%
queueSpareTrigger    = 2.0  // Scale-up when queue length > 2 requests

// Cost configuration (for multi-variant tests)
h100Cost = 50.0  // H100 accelerator cost ($/hr)
a100Cost = 30.0  // A100 accelerator cost ($/hr)
```

### Kind Cluster Configuration

```go
maximumAvailableGPUs = 4    // GPUs per node
numNodes             = 3    // Worker nodes in cluster
gpuTypes             = "mix" // GPU types: "mix", "uniform", or specific type
```

## Running the Tests

### Run All Saturation-Based E2E Tests (Default)

```bash
make test-e2e
```

This runs all saturation-based E2E tests by default with focus on "Saturation Mode" test suites.

### Run Specific Test Suite

```bash
# Run only single VariantAutoscaling tests
make test-e2e FOCUS="Single VariantAutoscaling"

# Run only multiple VariantAutoscaling tests
make test-e2e FOCUS="Multiple VariantAutoscalings"
```

### Run Specific Test Case

```bash
# Run specific test by name
make test-e2e FOCUS="should scale up when saturation is detected"

# Run cost-based preference test
make test-e2e FOCUS="should prefer cheaper A100 variant"
```

### Skip Specific Tests

```bash
make test-e2e SKIP="Multiple VariantAutoscalings"
```

### Run with Go Test Directly

```bash
# All tests with verbose output
go test ./test/e2e-saturation-based/ -v -ginkgo.v -timeout 30m

# With custom focus
go test ./test/e2e-saturation-based/ -v -ginkgo.v -timeout 30m -ginkgo.focus="Single VariantAutoscaling"

# Skip infrastructure setup if already deployed (for iterative development)
export CERT_MANAGER_INSTALL_SKIP=true
go test ./test/e2e-saturation-based/ -v -ginkgo.v -timeout 30m
```

### Run with Custom Configuration

```bash
# Set custom Kubernetes version
export K8S_VERSION=v1.31.0
make test-e2e

# Use custom controller image
export IMG=ghcr.io/llm-d-incubation/workload-variant-autoscaler:dev
make test-e2e
```

## Test Flow

### Single VariantAutoscaling Test Flow

1. **BeforeSuite**: Build controller image, create Kind cluster, deploy infrastructure
2. **ConfigMap Validation**: Verify saturation-scaling ConfigMap exists with default thresholds
3. **Resource Creation**: Create VariantAutoscaling resource for test deployment
4. **HPA Validation**: Verify HPA is configured with `inferno_desired_replicas` external metric
5. **Initial State**: Wait for deployment to reach minimum replica count (typically 1)
6. **Load Generation**: Start load generation job with configurable request rate
7. **Saturation Detection**: Monitor VariantAutoscaling status for saturation indicators:
   - KV cache utilization > threshold
   - Queue depth > threshold
8. **Scale-Up Recommendation**: Verify WVA recommends increased replica count
9. **HPA Trigger**: Confirm HPA reads updated metric and desires more replicas
10. **Deployment Scaling**: Validate deployment scales to recommended replica count
11. **Stabilization**: Ensure replicas remain stable under continued load
12. **Cleanup**: Stop load generation and verify graceful scale-down (optional)

### Multiple VariantAutoscaling Test Flow

Extends single variant test with:
- **Parallel Variant Setup**: Deploy A100 and H100 variants simultaneously
- **Independent Monitoring**: Track each variant's saturation metrics separately
- **Cost-Based Validation**: Generate load and verify cheaper A100 variant scales up first
- **Multi-Variant Stability**: Confirm both variants can scale independently without interference

## Expected Results

### Successful Single Variant Test

```
Building controller image...
Creating Kind cluster with 3 nodes and 4 GPUs per node...
Deploying WVA and llm-d infrastructure...
✓ Saturation ConfigMap created with default thresholds
✓ VariantAutoscaling resource created
✓ HPA configured with inferno_desired_replicas metric
✓ Deployment scaled to 1 replica (initial state)
✓ Load generation started (8 req/s)
✓ Saturation detected (KV cache: 75%, Queue: 12)
✓ WVA recommended 2 replicas
✓ HPA triggered scale-up
✓ Deployment scaled to 2 replicas
✓ Replicas stable under load
```

### Successful Multiple Variant Test

```
✓ A100 variant created (cost: $30/hr)
✓ H100 variant created (cost: $50/hr)
✓ Both variants at 1 replica each
✓ Load applied to gateway
✓ A100 variant scaled to 2 replicas (preferred due to lower cost)
✓ H100 variant remains at 1 replica
✓ Independent scaling validated
```

## Test Timeouts

The test suite uses the following timeouts:
- **Image build**: 5 minutes
- **Kind cluster creation**: 3 minutes
- **Infrastructure deployment**: 10 minutes
- **Pod readiness**: 2 minutes per component
- **Saturation detection**: 3-5 minutes
- **Scale-up completion**: 3 minutes
- **Overall test timeout**: 30 minutes (configurable with `-timeout` flag)

## Monitoring Test Execution

### Watch Resources During Test

```bash
# Get KUBECONFIG from test output
export KUBECONFIG=/tmp/wva-e2e-XXXXXX/kubeconfig

# Watch WVA controller pods
kubectl get pods -n workload-variant-autoscaler-system -w

# Watch test deployments
kubectl get pods,deploy,va,hpa -n llm-d-sim -w

# Watch VariantAutoscaling status
kubectl get va -n llm-d-sim -o yaml

# Watch HPA metrics
kubectl describe hpa -n llm-d-sim

# Check controller logs
kubectl logs -n workload-variant-autoscaler-system deployment/workload-variant-autoscaler-controller-manager -f

# Query external metrics API
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/llm-d-sim/inferno_desired_replicas" | jq
```

### Prometheus Metrics

Access Prometheus UI (port-forward required):
```bash
kubectl port-forward -n workload-variant-autoscaler-monitoring svc/prometheus-operated 9090:9090
# Open browser to http://localhost:9090
```

Key metrics to monitor:
- `inferno_desired_replicas{variant_name="..."}` - WVA replica recommendations
- `vllm_cache_utilization{variant_name="..."}` - KV cache utilization
- `vllm_queue_length{variant_name="..."}` - Request queue depth

## Troubleshooting

### Test Fails: Kind Cluster Creation

```bash
# Check Docker is running
docker ps

# Check Kind is installed
kind version

# Manually create cluster for debugging
make create-kind-cluster
export KUBECONFIG=$(kind get kubeconfig-path --name wva-e2e)
kubectl get nodes
```

### Test Fails: Image Build

```bash
# Build image manually
make docker-build IMG=ghcr.io/llm-d-incubation/workload-variant-autoscaler:0.0.1-test

# Verify image exists
docker images | grep workload-variant-autoscaler
```

### Test Fails: Infrastructure Deployment

```bash
# Check deployment logs
kubectl logs -n workload-variant-autoscaler-system deployment/workload-variant-autoscaler-controller-manager

# Check all pods are running
kubectl get pods -A

# Check ConfigMaps
kubectl get cm -n workload-variant-autoscaler-system

# Manually deploy infrastructure for debugging
make deploy-wva-emulated-on-kind
```

### Test Fails: Saturation Not Detected

```bash
# Check VariantAutoscaling status
kubectl get va -n llm-d-sim -o yaml

# Verify metrics are being collected
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/llm-d-sim/inferno_desired_replicas"

# Check load generation job
kubectl get jobs -n llm-d-sim
kubectl logs -n llm-d-sim job/<job-name>

# Verify vLLM emulator is serving metrics
kubectl port-forward -n llm-d-sim svc/<variant-service> 8000:8000
curl http://localhost:8000/metrics
```

### Test Hangs or Times Out

- **Check controller logs** for errors or stuck reconciliation loops
- **Verify HPA** is able to read external metrics
- **Confirm Prometheus** is scraping metrics from vLLM emulator
- **Increase timeout** if running on slower hardware: `-timeout 45m`

### Clean Up Failed Test

```bash
# Delete Kind cluster
kind delete cluster --name wva-e2e

# Or use Makefile target
make delete-kind-cluster
```

## Environment Variables

- **`CERT_MANAGER_INSTALL_SKIP=true`**: Skip cert-manager installation (if already present)
- **`K8S_VERSION`**: Kubernetes version for Kind cluster (default: v1.31.0)
- **`IMG`**: Custom controller image to test (default: auto-built test image)
- **`FOCUS`**: Ginkgo test focus pattern
- **`SKIP`**: Ginkgo test skip pattern
- **`KUBECONFIG`**: Use specific kubeconfig (default: Kind generates temporary config)

## CI Integration

These tests are designed for CI environments:
- **No GPU hardware required** - uses emulated GPUs
- **Isolated Kind cluster** - no cluster state conflicts
- **Automatic cleanup** - Kind cluster deleted after tests
- **Parallel-safe** - each test run uses unique cluster name
- **Fast execution** - typical runtime 15-25 minutes

## Development Workflow

### Iterative Testing

For faster iteration during development:

```bash
# 1. Create cluster once
make create-kind-cluster
export KUBECONFIG=$(kind get kubeconfig-path --name wva-e2e)

# 2. Deploy infrastructure once
make deploy-wva-emulated-on-kind

# 3. Run tests multiple times
export CERT_MANAGER_INSTALL_SKIP=true
go test ./test/e2e-saturation-based/ -v -ginkgo.v -ginkgo.focus="Single VariantAutoscaling"

# 4. Clean up when done
kind delete cluster --name wva-e2e
```

### Debugging Failed Tests

```bash
# Run with increased verbosity
go test ./test/e2e-saturation-based/ -v -ginkgo.v -ginkgo.trace -timeout 45m

# Keep cluster alive after failure for inspection
go test ./test/e2e-saturation-based/ -v -ginkgo.v -ginkgo.fail-fast

# Inspect resources in the cluster
export KUBECONFIG=$(kind get kubeconfig-path --name wva-e2e)
kubectl get all -A
kubectl describe va -n llm-d-sim
kubectl logs -n workload-variant-autoscaler-system deployment/workload-variant-autoscaler-controller-manager
```

## Test Coverage

Current test coverage includes:

### Saturation Detection
- ✅ KV cache utilization threshold detection
- ✅ Queue depth threshold detection
- ✅ Combined saturation signal handling
- ✅ Spare capacity calculation

### Scaling Behavior
- ✅ Scale-up from 1 to 2+ replicas
- ✅ HPA integration with external metrics
- ✅ Deployment scaling validation
- ✅ Replica stabilization under load

### Multi-Variant Scenarios
- ✅ Independent scaling of multiple variants
- ✅ Cost-based scaling preference
- ✅ Concurrent variant management
- ⏸️ Load balancing across variants (pending)

### Edge Cases
- ✅ Initial state with no load
- ✅ Rapid load changes
- ⏸️ Scale-down after load removal (pending)
- ⏸️ Zero-to-one scaling (pending)

## Contributing

When adding new saturation-based tests:

1. **Follow Ginkgo patterns**: Use `Describe`, `It`, `BeforeEach`, `AfterEach`, `Ordered` appropriately
2. **Use descriptive names**: Test names should clearly describe the expected behavior
3. **Add appropriate timeouts**: Use `Eventually` with reasonable timeouts and poll intervals
4. **Clean up resources**: Ensure resources are deleted in `AfterEach` or `AfterAll` blocks
5. **Document test parameters**: Add constants and comments explaining configuration choices
6. **Test both success and failure paths**: Include negative test cases where appropriate
7. **Update this README**: Document new test suites and expected behavior

## Related Documentation

- [OpenShift E2E Tests](../e2e-openshift/README.md) - Real cluster testing with vLLM
- [Development Guide](../../docs/developer-guide/development.md) - Development environment setup
- [Testing Guide](../../docs/developer-guide/testing.md) - Comprehensive testing documentation
- [Saturation Scaling Configuration](../../docs/saturation-scaling-config.md) - Algorithm details
- [HPA Integration](../../docs/integrations/hpa-integration.md) - HPA configuration
