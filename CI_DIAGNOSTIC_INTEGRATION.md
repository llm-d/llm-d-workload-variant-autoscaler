# Integrating Diagnostics into CI/CD Pipeline

## ✅ CI/CD Ready

The diagnostic script has been optimized for GitHub Actions:
- ✅ No Python dependencies (removed `json.tool`)
- ✅ Better error handling (`set +e` to collect all diagnostics)
- ✅ Improved port-forwarding with retries
- ✅ Color output works in GitHub Actions logs
- ✅ Timeouts on all network operations
- ✅ No `jq` required

## Quick Integration

Add this step to your GitHub Actions workflow after e2e test failures:

```yaml
- name: Run E2E Diagnostics on Failure
  if: failure()
  run: |
    chmod +x test/utils/ci_diagnostics.sh
    ./test/utils/ci_diagnostics.sh
  continue-on-error: true
```

## Complete Example

```yaml
name: E2E Tests with Diagnostics

on: [push, pull_request]

jobs:
  e2e-test:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Run E2E Tests
        id: e2e_tests
        run: make test-e2e
        continue-on-error: true

      - name: Run Diagnostics on Failure
        if: steps.e2e_tests.outcome == 'failure'
        run: |
          echo "::group::E2E Test Diagnostics"
          chmod +x test/utils/ci_diagnostics.sh
          ./test/utils/ci_diagnostics.sh
          echo "::endgroup::"

      - name: Collect Controller Logs
        if: steps.e2e_tests.outcome == 'failure'
        run: |
          echo "::group::Controller Logs"
          kubectl logs -n workload-variant-autoscaler-system \
            -l app.kubernetes.io/name=workload-variant-autoscaler \
            --tail=100
          echo "::endgroup::"

      - name: Collect Prometheus Status
        if: steps.e2e_tests.outcome == 'failure'
        run: |
          echo "::group::Prometheus Targets"
          kubectl port-forward -n workload-variant-autoscaler-monitoring \
            svc/kube-prometheus-stack-prometheus 9090:9090 &
          sleep 5
          curl -sk 'https://localhost:9090/api/v1/targets' | jq '.data.activeTargets[] | select(.labels.app | contains("llm-d"))'
          echo "::endgroup::"

      - name: Fail if tests failed
        if: steps.e2e_tests.outcome == 'failure'
        run: exit 1
```

## Manual Run During Development

```bash
# After test failure, run diagnostics
./test/utils/ci_diagnostics.sh

# Check specific pod metrics
kubectl exec -n llm-d-sim <pod-name> -- curl -s http://localhost:8000/metrics | grep vllm

# Query Prometheus directly
kubectl port-forward -n workload-variant-autoscaler-monitoring svc/kube-prometheus-stack-prometheus 9090:9090 &
curl -sk 'https://localhost:9090/api/v1/query?query=vllm:request_success_total' | jq .

# Check controller logs for specific VA
kubectl logs -n workload-variant-autoscaler-system \
  -l app.kubernetes.io/name=workload-variant-autoscaler \
  | grep "llm-d-sim-deployment"
```

## Key Diagnostic Outputs to Check

### 1. Metrics Endpoint (Most Critical)

Look for this output in the diagnostic script:
```
========================================
3. Checking Metrics Endpoint on llm-d-sim Pods
========================================
✓ Testing metrics endpoint on pod: llm-d-sim-deployment-xxx
✓ Metrics endpoint is accessible

Checking for vLLM metrics:
✗ No vLLM metrics found! Controller expects vllm:* metrics
```

**If you see this**: llm-d-sim doesn't expose vLLM metrics. This is the root cause.

### 2. Prometheus Query Results

Look for:
```
========================================
7. Querying Prometheus for vLLM Metrics
========================================
⚠ Prometheus query successful but returned no results

This means Prometheus is not scraping vLLM metrics from llm-d-sim pods
```

**If you see this**: Prometheus can't find vLLM metrics, confirming the issue.

### 3. VariantAutoscaling Status

Look for:
```
--- llm-d-sim-deployment ---
Current Replicas: 1
Desired Replicas: 0
✗ DesiredOptimizedAlloc.NumReplicas is 0 - Controller hasn't processed this VA!
```

**If you see this**: Controller skipped the VA due to missing metrics.

### 4. Controller Logs

Look for:
```
Checking for 'Metrics unavailable' errors:
✗ Found 5 'Metrics unavailable' log entries

Metrics unavailable, skipping optimization for variant llm-d-sim-deployment
```

**If you see this**: Confirms controller is skipping due to no metrics.

## Quick Fixes Reference

| Issue Found | Quick Fix | Command |
|-------------|-----------|---------|
| No vLLM metrics in pod | Check llm-d-sim version/config | `kubectl describe pod -n llm-d-sim` |
| ServiceMonitor missing | Check if created by test | `kubectl get servicemonitor -A` |
| Prometheus not scraping | Check targets page | Port-forward and check `/targets` |
| Load not being generated | Check job logs | `kubectl logs -n llm-d-sim job/<name>` |
| Model not in ConfigMap | Add to service-classes-config | `kubectl edit cm -n workload-variant-autoscaler-system` |

## Expected Successful Output

When everything is working, you should see:

```
========================================
3. Checking Metrics Endpoint on llm-d-sim Pods
========================================
✓ Found 10 vLLM metric lines
✓ Found model_name label in metrics

========================================
7. Querying Prometheus for vLLM Metrics
========================================
✓ Prometheus query successful

========================================
8. Checking VariantAutoscaling Resources
========================================
--- llm-d-sim-deployment ---
Current Replicas: 1
Desired Replicas: 2
✓ VA has been processed by controller

========================================
9. Checking Controller Logs
========================================
✓ No 'Metrics unavailable' errors in recent logs
```

## Adding to Makefile

```makefile
.PHONY: test-e2e-debug
test-e2e-debug: ## Run e2e tests with diagnostics on failure
	@echo "Running e2e tests with diagnostics..."
	$(MAKE) test-e2e || (./test/utils/ci_diagnostics.sh && exit 1)

.PHONY: diagnose-e2e
diagnose-e2e: ## Run diagnostics on current cluster
	@./test/utils/ci_diagnostics.sh
```

Then use:
```bash
make test-e2e-debug  # Run tests with auto-diagnostics
make diagnose-e2e    # Just run diagnostics
```
