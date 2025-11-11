# Answers to Your Questions

## Question 1: Will the script run in CI/CD GitHub Actions?

### ✅ YES - Fully Ready for GitHub Actions!

**What I Fixed:**
- ✅ Removed `set -e` → Now collects all diagnostics even if some fail
- ✅ Removed Python dependency (`python3 -m json.tool`)
- ✅ Removed `jq` dependency (uses grep/wc/cut instead)
- ✅ Added timeouts on all network operations (10 seconds)
- ✅ Added port-forward retry logic
- ✅ Proper cleanup of background processes
- ✅ GitHub Actions color code support

**To Use in CI/CD:**
```yaml
- name: Run Diagnostics on Failure
  if: failure()
  run: ./test/utils/ci_diagnostics.sh
```

**Test Locally First:**
```bash
./test/utils/test_diagnostics_local.sh
```

---

## Question 2: Could the tests fail because of HPA scale-to-zero being disabled?

### ❌ NO - Scale-to-Zero is NOT the Issue!

**Why:**

Scale-to-zero only controls the **minimum** replica count when there's **no load**:

| Setting | MinReplicas | What It Means |
|---------|-------------|---------------|
| `WVA_SCALE_TO_ZERO=false` | 1 | Won't scale below 1 replica |
| `WVA_SCALE_TO_ZERO=true` | 0 | Can scale to 0 replicas |

**Your Issue is Different:**

Your tests are failing on **scale-UP** (with load), not scale-down:
- Tests generate load expecting `DesiredReplicas > 1`
- But `DesiredReplicas` stays at `0` (not 1, not 2 - **zero**)
- This means controller **hasn't calculated anything**
- It's **skipping the VariantAutoscaling entirely**

**If scale-to-zero was the issue, you'd see:**
- ✅ Controller processes the VA
- ✅ `DesiredReplicas` set to at least 1 (the minimum)
- ✅ With load, scales up to 2, 3, etc.

**What you actually see:**
- ❌ Controller skips the VA
- ❌ `DesiredReplicas = 0` (uninitialized)
- ❌ Logs show "Metrics unavailable, skipping optimization"

**Proof:**
```go
// internal/utils/utils.go:275-279
minNumReplicas := 1 // scale to zero is disabled by default
if os.Getenv("WVA_SCALE_TO_ZERO") == "true" {
    minNumReplicas = 0
}
```

This only affects the **lower bound** during optimization, not whether the VA gets processed.

**Controller Logic:**
```go
// variantautoscaling_controller.go:301-320
if metricsValidation.Available {
    // Process VA and calculate replicas
} else {
    logger.Log.Warnw("Metrics unavailable, skipping optimization")
    continue  // ← THIS is why DesiredReplicas = 0
}
```

**Conclusion:** Scale-to-zero configuration has no impact on your failing tests.

---

## Question 3: Could the issue be in model names or namespace?

### ✅ Configuration is CORRECT - Need to Verify llm-d-sim Behavior

**What I Checked:**

| Component | Expected Value | Actual Value | Match? |
|-----------|---------------|--------------|--------|
| Test constant | `unsloth/Meta-Llama-3.1-8B` | `unsloth/Meta-Llama-3.1-8B` | ✅ |
| Service ConfigMap | `unsloth/Meta-Llama-3.1-8B` | `unsloth/Meta-Llama-3.1-8B` | ✅ |
| VA Spec ModelID | `unsloth/Meta-Llama-3.1-8B` | `unsloth/Meta-Llama-3.1-8B` | ✅ |
| Deployment `--model` arg | `unsloth/Meta-Llama-3.1-8B` | `unsloth/Meta-Llama-3.1-8B` | ✅ |
| Namespace | `llm-d-sim` | `llm-d-sim` | ✅ |

**All configuration is correct!** ✅

### The Unknown Factor

**Does llm-d-sim use the `--model` arg to label its Prometheus metrics?**

The deployment passes:
```yaml
Args:
  - "--model"
  - "unsloth/Meta-Llama-3.1-8B"
```

But deployment labels have:
```yaml
Labels:
  "llm-d.ai/model": "ms-sim-llm-d-modelservice"  # Different!
```

**The Question:** When llm-d-sim exposes metrics, what does it use for `model_name` label?

### Three Possible Scenarios

#### Scenario A: Uses `--model` arg (CORRECT) ✅
```promql
vllm:request_success_total{model_name="unsloth/Meta-Llama-3.1-8B",...}
```
**Result:** Controller finds metrics ✅

#### Scenario B: Uses deployment label (WRONG) ❌
```promql
vllm:request_success_total{model_name="ms-sim-llm-d-modelservice",...}
```
**Result:** Controller can't find metrics (model name mismatch) ❌

#### Scenario C: Doesn't expose vLLM metrics (WRONG) ❌
```
http_requests_total{...}
go_memstats_alloc_bytes{...}
# No vllm:* metrics at all
```
**Result:** Controller can't find metrics (no vLLM metrics) ❌

### How to Check

**Quick Check (one command):**
```bash
kubectl exec -n llm-d-sim \
  $(kubectl get pod -n llm-d-sim -o name | head -1 | cut -d'/' -f2) -- \
  sh -c 'curl -s http://localhost:8000/metrics | grep -E "vllm.*model_name"'
```

**What to look for:**

✅ **Good:** `vllm:request_success_total{model_name="unsloth/Meta-Llama-3.1-8B",...}`

❌ **Bad 1:** `vllm:request_success_total{model_name="ms-sim-llm-d-modelservice",...}`

❌ **Bad 2:** No output (no vLLM metrics)

### Enhanced Diagnostic Script

I updated `ci_diagnostics.sh` to check:
1. If vLLM metrics exist
2. If `model_name` label exists
3. **What value `model_name` has** ← NEW!
4. If `namespace` label exists
5. **What value `namespace` has** ← NEW!

It will specifically detect if llm-d-sim is using the deployment label instead of the `--model` arg!

**Run it:**
```bash
./test/utils/ci_diagnostics.sh
```

**Look at Section 3 - it will say:**
- ✅ "Model name looks correct (contains 'Meta-Llama')"
- ❌ "Model name is 'ms-sim-llm-d-modelservice' (deployment label, not actual model!)"
- ❌ "No model_name label found in metrics!"

---

## Summary of Findings

### ✅ Confirmed Correct:
1. **Model name configuration** - Matches everywhere
2. **Namespace configuration** - Consistent everywhere
3. **Service ConfigMap** - Has the correct model
4. **Deployment args** - Passes correct model to pod
5. **VariantAutoscaling spec** - References correct model

### ❓ Still Unknown:
1. **Does llm-d-sim expose vLLM metrics?** - Need to check
2. **What labels does llm-d-sim use?** - Need to check
3. **Does it use `--model` arg or deployment label?** - Need to check

### 🔍 How to Find Out:
1. Run: `./test/utils/ci_diagnostics.sh`
2. Look at Section 3 output
3. Check the "model_name value" subsection

---

## Next Steps

1. **Run the diagnostic script** (it now checks model_name/namespace)
   ```bash
   ./test/utils/ci_diagnostics.sh
   ```

2. **Check Section 3** - Look for:
   - "Checking model_name value:"
   - "Checking namespace label in metrics:"

3. **Based on results:**
   - If vLLM metrics missing → llm-d-sim doesn't expose them
   - If model_name wrong → llm-d-sim using deployment label
   - If model_name missing → llm-d-sim not adding label

4. **Implement fix** (see `E2E_TEST_FAILURE_ANALYSIS.md`)

---

## Quick Answers

| Your Question | Short Answer |
|---------------|--------------|
| Will script run in CI/CD? | ✅ YES - fully compatible now |
| Is it scale-to-zero? | ❌ NO - that's for scale-down minimum |
| Is it model name mismatch? | ✅ Config is correct; need to verify llm-d-sim |
| Is it namespace issue? | ✅ Namespace is correct everywhere |

**Bottom line:** Your configuration is perfect. The issue is whether llm-d-sim exposes the right metrics with the right labels. The diagnostic script will tell you exactly what's wrong.
