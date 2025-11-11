# E2E Test Failure Analysis

## Executive Summary

The e2e tests are failing because **the controller is not processing VariantAutoscaling resources**. The `DesiredOptimizedAlloc.NumReplicas` remains at 0 even after 5 minutes of load generation, indicating the controller is skipping these VAs during reconciliation.

## Failing Tests

1. **Single VariantAutoscaling test** - `test/e2e/e2e_test.go:338`
   - Expected: `DesiredOptimizedAlloc.NumReplicas > 0` after load starts
   - Actual: `DesiredOptimizedAlloc.NumReplicas = 0` (timeout after 5 minutes)

2. **Multiple VariantAutoscalings test** - `test/e2e/e2e_test.go:846`
   - Expected: `DesiredOptimizedAlloc.NumReplicas > 1` after load starts
   - Actual: `DesiredOptimizedAlloc.NumReplicas = 0` (timeout after 6 minutes)

## Root Cause: Missing vLLM Metrics

The controller skips VAs when metrics are unavailable. Here's the flow:

### Controller Reconciliation Logic

```go
// variantautoscaling_controller.go:301-320
metricsValidation := collector.ValidateMetricsAvailability(ctx, r.PromAPI, modelName, deploy.Namespace)

if metricsValidation.Available {
    // Process the VA...
} else {
    // Metrics unavailable - skip this VA
    logger.Log.Warnw("Metrics unavailable, skipping optimization for variant", ...)
    continue  // <-- VA is SKIPPED!
}
```

### Metric Validation Requirements

The controller queries Prometheus for:
```promql
vllm:request_success_total{model_name="unsloth/Meta-Llama-3.1-8B",namespace="llm-d-sim"}
```

If no results, it tries without namespace:
```promql
vllm:request_success_total{model_name="unsloth/Meta-Llama-3.1-8B"}
```

**If both queries return no results, the VA is skipped.**

### Required Metrics

The controller expects these vLLM-compatible metrics (from `internal/constants/metrics.go`):

| Metric Name | Purpose |
|------------|---------|
| `vllm:request_success_total` | Calculate arrival rate (requests/minute) |
| `vllm:request_prompt_tokens_sum/count` | Calculate average input tokens |
| `vllm:request_generation_tokens_sum/count` | Calculate average output tokens |
| `vllm:time_to_first_token_seconds_sum/count` | Calculate TTFT latency |
| `vllm:time_per_output_token_seconds_sum/count` | Calculate TPOT latency |

**All metrics must include the `model_name` label.**

## Why Metrics Are Missing

The tests changed from `vllme` deployments to `llm-d-sim` deployments:

### Old Setup (Working)
- **Deployment**: `CreateVllmeDeployment()`
- **Load generation**: Local command with `utils.StartLoadGenerator()`
- **Metrics**: vllme emulator exposed vLLM-compatible metrics

### New Setup (Failing)
- **Deployment**: `CreateLlmdSimDeployment()` using `ghcr.io/llm-d/llm-d-inference-sim:latest`
- **Load generation**: Kubernetes Job with GuideLLM
- **Metrics**: **Unknown if llm-d-sim exposes vLLM-compatible metrics**

### Possible Issues

1. **llm-d-sim doesn't expose vLLM metrics**
   - The image may expose different metrics format
   - Metrics may not include `model_name` label

2. **ServiceMonitor not scraping correctly**
   - Selector mismatch with service labels
   - Wrong port or path configuration

3. **Prometheus not scraping the namespace**
   - ServiceMonitor may not be in the correct namespace
   - Prometheus may not have permissions

4. **No traffic = no metrics**
   - Load generator may not be successfully sending requests
   - Gateway routing issues

## Scale-to-Zero Analysis

### Question: Does scale-to-zero setting affect the tests?

**Answer: NO - scale-to-zero is NOT the cause of this issue.**

### Scale-to-Zero Behavior

From `config/manager/configmap.yaml`:
```yaml
WVA_SCALE_TO_ZERO: "false"  # Default setting
```

From `internal/utils/utils.go:275-279`:
```go
minNumReplicas := 1 // scale to zero is disabled by default
if os.Getenv("WVA_SCALE_TO_ZERO") == "true" {
    minNumReplicas = 0
}
```

### What Scale-to-Zero Controls

| Setting | MinimumReplicas | Behavior |
|---------|----------------|----------|
| `false` (default) | 1 | With no load, scales down to 1 replica |
| `true` | 0 | With no load, scales down to 0 replicas |

### Why Scale-to-Zero is NOT the Issue

1. **Scale-to-zero only affects scale-DOWN behavior**
   - It sets the minimum number of replicas when there's no load
   - It does NOT prevent the controller from processing VAs

2. **The test is failing on scale-UP**
   - Test generates load expecting replicas > 1
   - `DesiredOptimizedAlloc.NumReplicas = 0` means controller hasn't calculated anything
   - If controller processed the VA, we'd see at least MinimumReplicas (1)

3. **The VA is being skipped entirely**
   - `DesiredOptimizedAlloc.NumReplicas = 0` is the uninitialized state
   - Controller logs would show "Metrics unavailable, skipping optimization"
   - Scale-to-zero doesn't cause controller to skip VAs

### Expected Behavior with Scale-to-Zero Disabled

If the controller was working properly:
- **Initial state**: CurrentAlloc.NumReplicas = 1 (Deployment has 1 replica)
- **With load**: DesiredOptimizedAlloc.NumReplicas > 1 (scale up)
- **Without load**: DesiredOptimizedAlloc.NumReplicas = 1 (scale down to MinimumReplicas)

## Diagnostic Steps for CI/CD

I've created a comprehensive diagnostic script: `test/utils/ci_diagnostics.sh`

### How to Use

Run in your CI/CD pipeline after test failure:

```bash
# Make executable
chmod +x test/utils/ci_diagnostics.sh

# Run diagnostics
./test/utils/ci_diagnostics.sh

# Or run with kubectl context already set
KUBECONFIG=~/.kube/config ./test/utils/ci_diagnostics.sh
```

### What It Checks

1. ✓ Namespaces exist
2. ✓ llm-d-sim pods are running
3. ✓ Metrics endpoint accessible on pods
4. ✓ vLLM metrics present in response
5. ✓ model_name label in metrics
6. ✓ Services exist and configured
7. ✓ ServiceMonitors exist and configured
8. ✓ Prometheus is running
9. ✓ Prometheus can query vLLM metrics
10. ✓ VariantAutoscaling resources created
11. ✓ VariantAutoscaling status updated
12. ✓ Controller logs for errors
13. ✓ Model in service-classes-config

### Key Checks

The script specifically looks for:
- "Metrics unavailable" in controller logs
- vLLM metrics format: `vllm:*`
- model_name label in metrics
- Prometheus query results
- DesiredOptimizedAlloc.NumReplicas value

## Recommended Fixes

### Priority 1: Verify llm-d-sim Metrics

1. **Check if llm-d-sim exposes vLLM metrics:**
   ```bash
   kubectl exec -n llm-d-sim <pod-name> -- curl http://localhost:8000/metrics | grep vllm
   ```

2. **Check for model_name label:**
   ```bash
   kubectl exec -n llm-d-sim <pod-name> -- curl http://localhost:8000/metrics | grep model_name
   ```

3. **If metrics don't exist:**
   - Check llm-d-sim documentation/source
   - Verify the image version supports vLLM metrics
   - Consider switching back to vllme emulator for tests
   - Or modify llm-d-sim to expose vLLM-compatible metrics

### Priority 2: Verify ServiceMonitor Configuration

1. **Check ServiceMonitor exists:**
   ```bash
   kubectl get servicemonitor -n workload-variant-autoscaler-monitoring
   ```

2. **Verify selector matches service labels:**
   ```bash
   kubectl get servicemonitor -n workload-variant-autoscaler-monitoring <name> -o yaml
   kubectl get svc -n llm-d-sim -o yaml
   ```

3. **Check Prometheus targets:**
   ```bash
   kubectl port-forward -n workload-variant-autoscaler-monitoring svc/kube-prometheus-stack-prometheus 9090:9090
   # Visit http://localhost:9090/targets
   ```

### Priority 3: Verify Load Generation

1. **Check load generator job:**
   ```bash
   kubectl get jobs -n llm-d-sim
   kubectl logs -n llm-d-sim job/<job-name>
   ```

2. **Verify requests reaching llm-d-sim:**
   ```bash
   kubectl logs -n llm-d-sim <pod-name> | grep -i request
   ```

3. **Check gateway routing:**
   ```bash
   kubectl get svc -n llm-d-sim infra-sim-inference-gateway-istio
   ```

### Priority 4: Add Debug Logging

Temporarily increase verbosity in CI/CD:

```yaml
# In test setup
env:
  - name: LOG_LEVEL
    value: "debug"
```

Then check controller logs for detailed metric queries.

## Alternative Solutions

### Option A: Use vllme Instead of llm-d-sim

If llm-d-sim doesn't provide vLLM metrics, revert to vllme:

```go
// In test setup
deployment := utils.CreateVllmeDeployment(namespace, deployName, modelName, appLabel)
```

This was the original working setup in upstream/main.

### Option B: Mock Metrics for Testing

Create a sidecar container that exposes mock vLLM metrics:

```yaml
containers:
- name: llm-d-sim
  # ... existing config
- name: metrics-mock
  image: prom/pushgateway
  # Expose mock vLLM metrics
```

### Option C: Modify llm-d-sim

Add vLLM-compatible metrics exporter to llm-d-sim:
- Fork the llm-d-sim repository
- Add metrics endpoint that mimics vLLM format
- Include model_name label in all metrics

### Option D: Make Controller Metrics-Optional (Not Recommended)

Modify controller to allow processing VAs without metrics:
- Set default values when metrics unavailable
- Skip optimization but still set ownerReferences
- Only emit warnings, don't skip VA

**Not recommended** because optimization requires metrics to make scaling decisions.

## Testing the Fix

After implementing fixes:

1. **Run diagnostic script first:**
   ```bash
   ./test/utils/ci_diagnostics.sh
   ```

2. **Verify metrics are available:**
   - Check section 3 (Metrics Endpoint)
   - Check section 7 (Prometheus Query)

3. **Check controller logs:**
   ```bash
   kubectl logs -n workload-variant-autoscaler-system -l app.kubernetes.io/name=workload-variant-autoscaler
   ```
   - Should NOT see "Metrics unavailable"
   - Should see "Optimization completed successfully"

4. **Verify VA status:**
   ```bash
   kubectl get variantautoscaling -n llm-d-sim -o yaml
   ```
   - `status.desiredOptimizedAlloc.numReplicas` should be > 0
   - `status.conditions` should have `MetricsAvailable=True`

5. **Run e2e tests:**
   ```bash
   make test-e2e
   ```

## Conclusion

The e2e test failures are caused by **missing vLLM metrics**, not by scale-to-zero configuration or controller logic issues. The controller is working as designed - it correctly skips VAs when metrics are unavailable to prevent making scaling decisions without data.

The fix requires ensuring llm-d-sim pods expose vLLM-compatible metrics that Prometheus can scrape, or switching back to a deployment type that does expose these metrics (like vllme).

## Next Steps

1. Run `test/utils/ci_diagnostics.sh` in CI/CD to confirm the root cause
2. Check llm-d-sim documentation/source for metrics support
3. Implement one of the recommended fixes (Priority 1-4)
4. Verify fix with diagnostic script before running full tests
5. Update test documentation with metrics requirements
