# Diagnostic Script - Quick Start Guide

## TL;DR

Your e2e tests are failing because **the controller can't find vLLM metrics**. Use this diagnostic script to confirm:

```bash
chmod +x test/utils/ci_diagnostics.sh
./test/utils/ci_diagnostics.sh
```

## What to Look For

### 🔴 Critical Failure Indicator

Look for this in section 3 of the output:

```
========================================
3. Checking Metrics Endpoint on llm-d-sim Pods
========================================
✗ No vLLM metrics found! Controller expects vllm:* metrics
```

**This confirms llm-d-sim doesn't expose the metrics the controller needs.**

### ✅ What Success Looks Like

```
========================================
3. Checking Metrics Endpoint on llm-d-sim Pods
========================================
✓ Found 10 vLLM metric lines
✓ Found model_name label in metrics
```

## Quick Test

Before using in CI/CD, test it locally:

```bash
# Quick local test
./test/utils/test_diagnostics_local.sh
```

This verifies:
- ✅ kubectl is installed
- ✅ Cluster is accessible
- ✅ Script runs without errors

## Using in GitHub Actions

### Option 1: Simple (add to existing workflow)

```yaml
- name: Run E2E Tests
  run: make test-e2e
  continue-on-error: true
  id: tests

- name: Run Diagnostics
  if: steps.tests.outcome == 'failure'
  run: |
    chmod +x test/utils/ci_diagnostics.sh
    ./test/utils/ci_diagnostics.sh
```

### Option 2: Complete (copy ready-made workflow)

Use the example workflow:
```bash
cp .github/workflows/e2e-diagnostics-example.yaml .github/workflows/e2e-tests.yaml
```

Then commit and push.

## Reading the Output

### The Script Checks 11 Things:

1. **Namespaces** - Do they exist?
2. **llm-d-sim Pods** - Are they running?
3. **Metrics Endpoint** - ⭐ **MOST IMPORTANT** - Are vLLM metrics exposed?
4. **Services** - Are they configured?
5. **ServiceMonitors** - Do they exist?
6. **Prometheus** - Is it running?
7. **Prometheus Query** - Can it find vLLM metrics?
8. **VariantAutoscaling** - Status and conditions
9. **Controller Logs** - Any "Metrics unavailable" errors?
10. **ConfigMaps** - Is model in service-classes-config?
11. **Summary** - Overall assessment

### Priority Order (Check These First)

1. ⭐ **Section 3** - Metrics endpoint (if fails here, everything else will fail)
2. 📊 **Section 7** - Prometheus query (confirms metrics not reaching Prometheus)
3. 📝 **Section 9** - Controller logs (confirms controller is skipping VAs)
4. 🎯 **Section 8** - VA status (confirms DesiredReplicas = 0)

## Common Findings & Fixes

### Finding: No vLLM metrics in pod

```
✗ No vLLM metrics found! Controller expects vllm:* metrics

Available metrics (first 10):
http_requests_total
go_memstats_alloc_bytes
...
```

**Fix:** llm-d-sim doesn't expose vLLM metrics. Options:
1. Switch back to vllme deployment
2. Add vLLM metrics exporter to llm-d-sim
3. Use different load test setup

### Finding: ServiceMonitor not scraping

```
⚠ No llm-d ServiceMonitors found in workload-variant-autoscaler-monitoring
```

**Fix:** Check test setup - ServiceMonitor should be created in BeforeAll

### Finding: Prometheus returns no results

```
⚠ Prometheus query successful but returned no results
```

**Fix:** Even if metrics exist in pod, Prometheus isn't scraping them. Check:
- ServiceMonitor selector matches service labels
- ServiceMonitor is in monitoring namespace
- Prometheus has RBAC to scrape llm-d-sim namespace

### Finding: Model not in ConfigMap

```
✗ Model 'unsloth/Meta-Llama-3.1-8B' NOT found in service-classes-config!
```

**Fix:** Add model to ConfigMap (already fixed in commit eae0801)

## Interpreting Results

### Scenario A: Everything Up to Section 3 Passes

```
✓ Namespace llm-d-sim exists
✓ Found 1 pod(s) in namespace llm-d-sim
✓ 1 pod(s) in Running state
✓ Metrics endpoint is accessible
✓ Found 10 vLLM metric lines  ← Good!
✓ Found model_name label in metrics
```

**Status:** 🟢 Metrics are available
**Next:** Check sections 7-9 to see why controller isn't using them

### Scenario B: Section 3 Fails (MOST LIKELY)

```
✓ Namespace llm-d-sim exists
✓ Found 1 pod(s) in namespace llm-d-sim
✓ 1 pod(s) in Running state
✓ Metrics endpoint is accessible
✗ No vLLM metrics found!  ← Problem!
```

**Status:** 🔴 Root cause identified
**Fix:** llm-d-sim doesn't expose vLLM metrics

### Scenario C: Section 7 Fails

```
✓ Section 3 passes (metrics in pod)
⚠ Section 7: Prometheus returns no results
```

**Status:** 🟡 Metrics exist but Prometheus can't see them
**Fix:** ServiceMonitor configuration issue

## One-Liner Verification

To quickly verify the issue without running the full script:

```bash
# Check if llm-d-sim has vLLM metrics
kubectl exec -n llm-d-sim \
  $(kubectl get pod -n llm-d-sim -o name | head -1) -- \
  curl -s http://localhost:8000/metrics | grep -c "^vllm:"
```

**Expected:** Number > 0
**Actual (failing):** 0

## Dependencies

The script requires only standard tools available in GitHub Actions:
- ✅ `kubectl` - Kubernetes CLI
- ✅ `curl` - HTTP client
- ✅ `grep`, `wc`, `head`, `cut` - Text processing
- ✅ Bash 4.0+

**No Python, no jq, no special tools required!**

## Troubleshooting the Script Itself

If the script fails to run:

```bash
# Check permissions
ls -la test/utils/ci_diagnostics.sh
# Should show: -rwxr-xr-x

# Make executable if needed
chmod +x test/utils/ci_diagnostics.sh

# Check bash version (need 4.0+)
bash --version

# Test kubectl access
kubectl get nodes

# Run with explicit bash
bash test/utils/ci_diagnostics.sh
```

## Next Steps After Running

1. **Review Section 3** - This is the smoking gun
2. **Save the output** - You'll need it for debugging
3. **Check the analysis doc** - See `E2E_TEST_FAILURE_ANALYSIS.md` for detailed fixes
4. **Implement fix** - See "Recommended Fixes" section
5. **Re-run diagnostics** - Verify fix before running full tests

## Getting Help

If the diagnostic output is unclear:

1. Save full output to file:
   ```bash
   ./test/utils/ci_diagnostics.sh > diagnostics.txt 2>&1
   ```

2. Look at these specific sections:
   - Section 3: Line starting with "Checking for vLLM metrics"
   - Section 7: Line starting with "Prometheus query"
   - Section 9: Lines containing "Metrics unavailable"

3. Share the output with your team

## Success Criteria

You know it's fixed when:
- ✅ Section 3 shows vLLM metrics found
- ✅ Section 7 shows Prometheus query returns results
- ✅ Section 8 shows DesiredReplicas > 0
- ✅ Section 9 shows no "Metrics unavailable" errors
- ✅ E2E tests pass!
