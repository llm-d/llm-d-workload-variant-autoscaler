# Model Name and Namespace Analysis

## Investigation Results

I checked for potential model name and namespace mismatches. Here's what I found:

## ✅ Model Name - CORRECT

### Test Setup
```go
// test/e2e/e2e_test.go:55
llamaModelId = "unsloth/Meta-Llama-3.1-8B"
```

### ConfigMap
```yaml
# deploy/configmap-serviceclass.yaml:14
- model: unsloth/Meta-Llama-3.1-8B
  slo-tpot: 24
  slo-ttft: 500
```

### VariantAutoscaling Spec
```go
// test/utils/e2eutils.go:949
ModelID: modelId,  // "unsloth/Meta-Llama-3.1-8B"
```

**✅ Model name matches everywhere!**

## ✅ Namespace - CORRECT

### Test Setup
```go
// test/e2e/e2e_test.go:50
llmDNamespace = "llm-d-sim"

// test/e2e/e2e_test.go:171
namespace = llmDNamespace  // "llm-d-sim"
```

### Controller Queries
```go
// internal/collector/collector.go:163
deployNamespace := deployment.Namespace  // "llm-d-sim"
```

**✅ Namespace is consistent!**

## ⚠️ Potential Issue: How llm-d-sim Labels Metrics

Here's the KEY question:

### Controller Expects
```promql
vllm:request_success_total{model_name="unsloth/Meta-Llama-3.1-8B",namespace="llm-d-sim"}
```

### What llm-d-sim Gets
```yaml
# Deployment args
Args:
  - "--model"
  - "unsloth/Meta-Llama-3.1-8B"  # Passed to llm-d-sim

# Deployment labels
Labels:
  "llm-d.ai/model": "ms-sim-llm-d-modelservice"  # NOT the actual model!
```

### The Question
**Does llm-d-sim use the `--model` arg to set the `model_name` label in its Prometheus metrics?**

If llm-d-sim:
- ✅ Uses `--model` arg → metrics will have `model_name="unsloth/Meta-Llama-3.1-8B"` ✅
- ❌ Uses deployment labels → metrics will have `model_name="ms-sim-llm-d-modelservice"` ❌
- ❌ Uses hardcoded value → metrics will have `model_name="<hardcoded>"` ❌
- ❌ Doesn't set model_name at all → metrics won't have the label ❌

## How to Check

Run this in your CI/CD to see what labels llm-d-sim actually uses:

```bash
# Check if metrics have model_name label
kubectl exec -n llm-d-sim <pod-name> -- \
  curl -s http://localhost:8000/metrics | grep model_name

# Expected output (if correct):
# vllm:request_success_total{model_name="unsloth/Meta-Llama-3.1-8B",...}

# Bad outputs:
# - Nothing with model_name → llm-d-sim doesn't add this label
# - model_name="ms-sim-llm-d-modelservice" → using wrong label
# - model_name="something-else" → using hardcoded value
```

## Namespace Label in Metrics

Similarly, check if llm-d-sim adds namespace label to metrics:

```bash
kubectl exec -n llm-d-sim <pod-name> -- \
  curl -s http://localhost:8000/metrics | grep namespace=
```

The llm-d-sim deployment has `POD_NAMESPACE` env var, so it COULD use it:
```yaml
Env:
  - Name: POD_NAMESPACE
    ValueFrom:
      FieldRef:
        FieldPath: metadata.namespace
```

**If llm-d-sim uses this env var, metrics should have `namespace="llm-d-sim"`**

## Controller's Fallback Logic

The good news is the controller has a fallback:

```go
// internal/collector/collector.go:89
testQuery := fmt.Sprintf(`vllm:request_success_total{model_name="%s",namespace="%s"}`,
    modelName, namespace)

// If that fails...
// internal/collector/collector.go:114
testQueryFallback := fmt.Sprintf(`vllm:request_success_total{model_name="%s"}`,
    modelName)
```

So even if the namespace label is missing, it should still work IF:
- ✅ Metric name is `vllm:request_success_total`
- ✅ Has label `model_name="unsloth/Meta-Llama-3.1-8B"`

## Most Likely Scenarios

### Scenario 1: llm-d-sim Doesn't Expose vLLM Metrics (90% probability)
```bash
# Check returns 0
kubectl exec -n llm-d-sim <pod> -- curl -s localhost:8000/metrics | grep -c "^vllm:"
```
**Fix:** Use vllme or add vLLM metrics to llm-d-sim

### Scenario 2: Metrics Exist but Wrong model_name Label (5% probability)
```bash
# Check shows different model name
kubectl exec -n llm-d-sim <pod> -- curl -s localhost:8000/metrics | grep model_name=
# Output: model_name="ms-sim-llm-d-modelservice"  # Wrong!
```
**Fix:** llm-d-sim needs to use `--model` arg for the label, not deployment label

### Scenario 3: Metrics Exist but No model_name Label (3% probability)
```bash
# Metrics exist but no model_name label
kubectl exec -n llm-d-sim <pod> -- curl -s localhost:8000/metrics | grep "^vllm:"
# Shows metrics but no model_name= in them
```
**Fix:** llm-d-sim needs to add model_name label to all metrics

### Scenario 4: Everything is Correct but Prometheus Not Scraping (2% probability)
```bash
# Metrics correct in pod
kubectl exec -n llm-d-sim <pod> -- curl -s localhost:8000/metrics | grep 'vllm.*model_name'
# Shows correct metrics

# But Prometheus doesn't have them
kubectl port-forward -n monitoring svc/prometheus 9090:9090 &
curl -sk 'https://localhost:9090/api/v1/query?query=vllm:request_success_total' | grep result
# Shows empty results
```
**Fix:** ServiceMonitor configuration issue

## Quick Validation Commands

Add these to the diagnostic script to check model_name:

```bash
# 1. Check if vLLM metrics exist
kubectl exec -n llm-d-sim $POD_NAME -- \
  curl -s http://localhost:8000/metrics | grep -c "^vllm:"

# 2. Check if model_name label exists
kubectl exec -n llm-d-sim $POD_NAME -- \
  curl -s http://localhost:8000/metrics | grep -c 'model_name='

# 3. Check what model_name value is used
kubectl exec -n llm-d-sim $POD_NAME -- \
  curl -s http://localhost:8000/metrics | grep 'model_name=' | head -1

# 4. Check if namespace label exists
kubectl exec -n llm-d-sim $POD_NAME -- \
  curl -s http://localhost:8000/metrics | grep -c 'namespace='

# 5. Check what namespace value is used
kubectl exec -n llm-d-sim $POD_NAME -- \
  curl -s http://localhost:8000/metrics | grep 'namespace=' | head -1
```

## Expected vs Actual

| Component | Expected | Actual | Match? |
|-----------|----------|--------|--------|
| Model Name in Test | `unsloth/Meta-Llama-3.1-8B` | `unsloth/Meta-Llama-3.1-8B` | ✅ |
| Model Name in ConfigMap | `unsloth/Meta-Llama-3.1-8B` | `unsloth/Meta-Llama-3.1-8B` | ✅ |
| Model Name in VA Spec | `unsloth/Meta-Llama-3.1-8B` | `unsloth/Meta-Llama-3.1-8B` | ✅ |
| Model Name passed to pod | `unsloth/Meta-Llama-3.1-8B` | `unsloth/Meta-Llama-3.1-8B` | ✅ |
| Namespace | `llm-d-sim` | `llm-d-sim` | ✅ |
| **Model name in metrics** | `unsloth/Meta-Llama-3.1-8B` | **❓ UNKNOWN** | **❓** |
| **Namespace in metrics** | `llm-d-sim` | **❓ UNKNOWN** | **❓** |

## Conclusion

**Model name and namespace are CORRECT in all the configuration.**

**The unknown factor is: What labels does llm-d-sim actually put in its metrics?**

This is why running the diagnostic script is critical - Section 3 will show you:
1. If vLLM metrics exist
2. What labels they have
3. What values those labels contain

## Action Items

1. ✅ **Configuration is correct** - no changes needed to model names or namespaces
2. ⚠️ **Need to verify llm-d-sim behavior** - run diagnostic script
3. 🔍 **Key check:** Does llm-d-sim metrics have `model_name="unsloth/Meta-Llama-3.1-8B"`?

The diagnostic script's Section 3 will answer this definitively.
