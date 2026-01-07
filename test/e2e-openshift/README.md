# OpenShift E2E Tests

This directory contains end-to-end tests for the Workload-Variant-Autoscaler on OpenShift clusters with real vLLM deployments.

## Overview

These tests validate the autoscaling behavior of the Workload-Variant-Autoscaler integrated with HPA on OpenShift using real workloads. Unlike the emulated tests in `test/e2e`, these tests run against actual vLLM deployments with the llm-d infrastructure.

## Prerequisites

### Infrastructure Requirements

The tests assume the following infrastructure is already deployed on the OpenShift cluster:

1. **Workload-Variant-Autoscaler** controller running in `workload-variant-autoscaler-system` namespace
2. **llm-d infrastructure** deployed in `llm-d-inference-scheduling` namespace:
   - Gateway (infra-inference-scheduling-inference-gateway)
   - Inference Scheduler (GAIE)
   - vLLM deployment (ms-inference-scheduling-llm-d-modelservice-decode)
3. **Prometheus** and **Thanos** for metrics collection
4. **Prometheus Adapter** for exposing external metrics to HPA
5. **HPA** configured to read `inferno_desired_replicas` metric
6. **VariantAutoscaling** resource created for the vLLM deployment

### Environment Setup

1. Set `KUBECONFIG` to point to your OpenShift cluster:
   ```bash
   export KUBECONFIG=/path/to/your/kubeconfig
   ```

2. Verify you have access to the cluster:
   ```bash
   oc whoami
   oc get nodes
   ```

3. Verify the infrastructure is running:
   ```bash
   # Check WVA controller
   oc get pods -n workload-variant-autoscaler-system
   
   # Check llm-d infrastructure
   oc get pods -n llm-d-inference-scheduling
   
   # Check Prometheus Adapter
   oc get pods -n openshift-user-workload-monitoring | grep prometheus-adapter
   
   # Check HPA
   oc get hpa -n llm-d-inference-scheduling
   
   # Check VariantAutoscaling
   oc get variantautoscaling -n llm-d-inference-scheduling
   ```

## Test Structure

### Test Files

- **`e2e_suite_test.go`**: Test suite setup and infrastructure verification
- **`sharegpt_scaleup_test.go`**: ShareGPT load generation and scale-up validation test

### Test Flow

The `sharegpt_scaleup_test.go` test performs the following steps:

1. **Initial State Verification**
   - Records initial replica count
   - Records initial VariantAutoscaling optimization state
   - Verifies HPA configuration
   - Verifies external metrics API accessibility

2. **Load Generation**
   - Creates a Kubernetes Job that runs vLLM benchmark with ShareGPT dataset
   - Downloads ShareGPT dataset from HuggingFace
   - Generates load at 20 requests/second with 3000 prompts
   - Verifies the job pod is running

3. **Scale-Up Detection**
   - Monitors VariantAutoscaling for increased replica recommendation
   - Verifies WVA detects the increased load
   - Expects optimization to recommend at least 2 replicas

4. **HPA Scale-Up Trigger**
   - Monitors HPA for metric processing
   - Verifies HPA reads the updated `inferno_desired_replicas` metric
   - Confirms HPA desires more replicas

5. **Deployment Scaling**
   - Monitors the vLLM deployment for actual scale-up
   - Verifies at least 2 replicas become ready
   - Confirms deployment maintains scaled state under load

6. **Job Completion**
   - Waits for the load generation job to complete successfully
   - Verifies all requests were processed

7. **Cleanup**
   - Removes the load generation job
   - Reports final scaling results

## Running the Tests

### Run All OpenShift E2E Tests

##### Default Arguments:


```bash
CONTROLLER_NAMESPACE = workload-variant-autoscaler-system
MONITORING_NAMESPACE = openshift-user-workload-monitoring
LLMD_NAMESPACE       = llm-d-inference-scheduling
GATEWAY_NAME         = infra-inference-scheduling-inference-gateway
MODEL_ID             = unsloth/Meta-Llama-3.1-8B
DEPLOYMENT           = ms-inference-scheduling-llm-d-modelservice-decode
REQUEST_RATE         = 20
NUM_PROMPTS          = 3000
```


#### Example 1: Using Default Arguments


```bash
make test-e2e-openshift
```


#### Example 2: Using Custom Arguments

```bash
make test-e2e-openshift \
LLMD_NAMESPACE=llmd-stack \
DEPLOYMENT=unsloth--00171c6f-a-3-1-8b-decode \
GATEWAY_NAME=infra-llmd-inference-gateway \
REQUEST_RATE=20 \
NUM_PROMPTS=3000
```

#### Example 3: Using GO Directly Using Default Arguments
```bash
go test ./test/e2e-openshift/... -v -ginkgo.v -timeout 30m
```

#### Example 4: Using GO Directly Using Custom Arguments
```bash
export LLMD_NAMESPACE=llmd-stack
export DEPLOYMENT=unsloth--00171c6f-a-3-1-8b-decode
export GATEWAY_NAME=infra-llmd-inference-gateway
export REQUEST_RATE=8
export NUM_PROMPTS=2000
go test ./test/e2e-openshift/... -v -ginkgo.v -timeout 30m
```

### Run Specific Test
```bash
go test ./test/e2e-openshift/... -v -ginkgo.v -timeout 30m -ginkgo.focus="ShareGPT Scale-Up Test"
```

### Run with Custom Timeouts

```bash
go test ./test/e2e-openshift/... -v -ginkgo.v -timeout 45m
```

## Test Parameters

### Load Generation Configuration

The test uses configurable constants defined in `sharegpt_scaleup_test.go`:

```go
const (
    numLoadWorkers     = 10    // Number of parallel load generation workers
    requestsPerWorker  = 500   // Requests each worker sends
    batchSize          = 50    // Concurrent requests per batch
    curlTimeoutSeconds = 180   // Timeout for each curl request
    maxTokens          = 400   // Max tokens for completion requests
    batchSleepDuration = "0.1" // Sleep duration between batches to control rate
)
```

#### Key Configuration Details

- **`maxTokens` (400)**: Increased from 150 to generate longer outputs that keep the GPU KV cache occupied longer. This ensures the saturation engine detects load and triggers scale-up reliably, especially with fast GPUs like A100s where shorter outputs may complete before metrics are collected.

- **`curlTimeoutSeconds` (180)**: Increased from 120s to accommodate the longer response times resulting from increased `maxTokens`.

- **`numLoadWorkers` (10)**: Number of parallel load generation workers that run concurrently to simulate realistic traffic patterns.

- **`batchSize` (50)**: Number of concurrent requests sent per batch by each worker, controlling burst characteristics.

### Modifying Test Parameters

You can modify the load generation parameters by editing the function call in `sharegpt_scaleup_test.go`:

```go
job := createShareGPTJob(jobName, llmDNamespace, 20, 3000)
//                                               ^^  ^^^^
//                                               |    |
//                                               |    +--- Number of prompts
//                                               +-------- Request rate (req/s)
```

### Recommended Load Profiles

- **Light load** (should stay at 1 replica): `requestRate: 8, numPrompts: 2000`
- **Medium load** (should scale to 2 replicas): `requestRate: 20, numPrompts: 3000`
- **Heavy load** (may scale to 3+ replicas): `requestRate: 40, numPrompts: 5000`

> **Note**: The `maxTokens` and `curlTimeoutSeconds` constants are tuned for reliable scale-up detection with A100 GPUs. If testing with different GPU types or model configurations, you may need to adjust these values to ensure load is sustained long enough for the saturation engine to detect and recommend scaling.

## Expected Results

A successful test run should show:

```
Infrastructure verification complete
Load generation job is running
WVA detected load and recommended 2 replicas (up from 1)
HPA triggered scale-up
Deployment scaled to 2 replicas (up from 1)
Deployment maintained 2 replicas under load
Load generation job completed successfully
Test completed - scaled from 1 to 2 replicas
```

## Monitoring CI Runs

When the CI e2e tests are running on OpenShift, you can monitor the cluster resources using the following commands. Replace `<PR_NUMBER>` with your actual PR number:

```bash
# Watch WVA controller pods
oc get pods,deploy -n llm-d-autoscaler-pr-<PR_NUMBER>

# Watch Model A (primary namespace) - pods, deployments, VAs, and HPAs
oc get pods,deploy,va,hpa -n llm-d-inference-scheduler-pr-<PR_NUMBER>

# Watch Model B (secondary namespace) - pods, deployments, VAs, and HPAs
oc get pods,deploy,va,hpa -n llm-d-inference-scheduler-pr-<PR_NUMBER>-b

# Watch all VAs across namespaces with detailed status
oc get va -A -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,OPTIMIZED:.status.desiredOptimizedAlloc.numReplicas,REPLICAS:.status.currentAlloc.numReplicas,RATE:.status.currentAlloc.load.arrivalRate'

# Watch load generation jobs
oc get jobs -n llm-d-inference-scheduler-pr-<PR_NUMBER>
oc get jobs -n llm-d-inference-scheduler-pr-<PR_NUMBER>-b
```

### CI Cleanup Behavior

The CI workflow handles cleanup as follows:
- **Before tests**: All PR-specific namespaces are cleaned up to ensure a fresh start
- **After successful tests**: Resources are cleaned up automatically
- **After failed tests**: GPU workloads are automatically scaled down to free GPUs for other PRs, while all other resources (VariantAutoscaling, HPA, controller logs, events) are preserved for debugging

#### Failure Handling: Smart Resource Management

When E2E tests fail, the CI workflow automatically:
1. **Scales down GPU workloads** (decode deployments) to 0 replicas
2. **Preserves debugging resources**:
   - VariantAutoscaling CRs and their status
   - HPA configuration and events
   - Controller pods and logs
   - Gateway services
   - Prometheus metrics (if still available)

This approach ensures:
- ✅ GPUs are immediately available for other PRs
- ✅ Full debugging capability via VA status, HPA events, and controller logs
- ✅ No manual intervention needed to free cluster resources
- ✅ Failed test namespaces can be investigated without blocking others

To manually trigger a run with cleanup disabled after success, use `SKIP_CLEANUP=true`.

## Multi-Model Testing

The CI tests deploy 2 models in 2 separate namespaces with 1 shared WVA controller:

- **Model A**: Full llm-d stack in `llm-d-inference-scheduler-pr-<PR>`
- **Model B**: Full llm-d stack in `llm-d-inference-scheduler-pr-<PR>-b`
- **Shared WVA**: Single controller in `llm-d-autoscaler-pr-<PR>` managing both

This validates:
1. Multi-namespace WVA controller operation
2. Independent scaling for each model
3. Namespace isolation for metrics and HPA

## Troubleshooting

### Test Fails: Infrastructure Not Ready

If the BeforeSuite fails, verify all infrastructure components are deployed:

```bash
# Check all namespaces
oc get pods -n workload-variant-autoscaler-system
oc get pods -n llm-d-inference-scheduling
oc get pods -n openshift-user-workload-monitoring | grep prometheus-adapter

# Check custom resources
oc get variantautoscaling -n llm-d-inference-scheduling
oc get hpa -n llm-d-inference-scheduling
```

### Test Fails: External Metrics Not Available

```bash
# Check Prometheus Adapter logs
oc logs -n openshift-user-workload-monitoring deployment/prometheus-adapter

# Query external metrics API directly
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/llm-d-inference-scheduling/inferno_desired_replicas" | jq
```

### Test Fails: No Scale-Up Detected

```bash
# Check WVA controller logs
oc logs -n workload-variant-autoscaler-system deployment/workload-variant-autoscaler-controller-manager | grep inference-scheduling

# Check VariantAutoscaling status
oc get variantautoscaling -n llm-d-inference-scheduling -o yaml

# Check HPA status
oc describe hpa vllm-deployment-hpa -n llm-d-inference-scheduling
```

### Job Fails to Complete

```bash
# Check job status
oc get job vllm-bench-sharegpt-e2e -n llm-d-inference-scheduling

# Check job pod logs
oc logs -n llm-d-inference-scheduling job/vllm-bench-sharegpt-e2e

# Check if gateway is accessible
oc get svc -n llm-d-inference-scheduling | grep gateway
```

## Test Timeouts

The test uses the following timeouts:

- Infrastructure verification: 2-5 minutes
- Job pod startup: 3 minutes
- Scale-up detection: 3 minutes
- HPA trigger: 3 minutes
- Deployment scaling: 5 minutes
- Job completion: 10 minutes
- Overall test timeout: 30 minutes (configurable)

## Contributing

When adding new tests:

1. Follow the Ginkgo/Gomega testing patterns
2. Use descriptive test names with `It("should ...")` format
3. Add appropriate timeouts with `Eventually` and `Consistently`
4. Clean up resources in `AfterAll` blocks
5. Log progress with `fmt.Fprintf(GinkgoWriter, ...)`
6. Document expected behavior and test parameters

