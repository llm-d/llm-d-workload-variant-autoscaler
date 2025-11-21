# Capacity Model E2E Tests

This directory contains end-to-end tests specifically for the **capacity-based scaling model**.

## Test Files

### 1. `capacity_config_test.go` - Configuration Validation ⚡ QUICK
**Duration:** ~5 minutes
**Purpose:** Validates capacity-scaling-config ConfigMap and thresholds

**What it tests:**
- ✅ capacity-scaling-config ConfigMap exists
- ✅ Default thresholds are valid and safe for production
- ✅ Controller loaded configuration successfully
- ✅ VariantAutoscaling resources have correct status conditions
- ✅ Capacity analyzer is making decisions
- ✅ No saturation with default settings

**When to run:** First test to run - validates infrastructure

---

### 2. `capacity_scaleup_test.go` - Scale-Up Detection ⏱️ MEDIUM
**Duration:** ~10-15 minutes
**Purpose:** Tests capacity analyzer detects load and triggers scale-up

**Load profile:** Conservative (rate=15, prompts=1500) to avoid over-saturation

**What it tests:**
- ✅ Prometheus has vLLM capacity metrics (KV cache, queue length)
- ✅ Capacity analyzer detects increased load
- ✅ Scale-up recommendation generated when capacity is strained
- ✅ Capacity metrics stay within safe thresholds
- ✅ HPA processes capacity analyzer recommendations
- ✅ Load generation completes successfully

**Safety features:**
- Uses reduced request rate (15 vs 20) to prevent over-saturation
- Shorter test duration (1500 vs 3000 prompts)
- Validates metrics don't exceed dangerous levels

---

### 3. `capacity_scaledown_test.go` - Safe Scale-Down ⏱️ MEDIUM
**Duration:** ~8 minutes
**Purpose:** Tests capacity analyzer allows safe scale-down under no load

**What it tests:**
- ✅ Stable replica count under no load
- ✅ Capacity metrics show low utilization
- ✅ Safe scale-down decisions (maintains minimum replicas)
- ✅ Scale-down safety constraints (gradual, never to zero)
- ✅ HPA actuates recommendations
- ✅ OptimizationReady condition remains True

**Safety features:**
- Monitors for 3 minutes to observe gradual scale-down
- Validates minimum of 1 replica always maintained
- Ensures OptimizationReady condition stays healthy

---

## Prerequisites

### Infrastructure Required

1. **OpenShift/Kubernetes cluster** with:
   - GPU nodes available
   - Network access from test runner

2. **Deployed components:**
   - WVA controller (from `capacity-model` branch)
   - Prometheus Operator
   - vLLM inference deployment
   - HPA v2 configured for external metrics

3. **Configuration:**
   - `capacity-scaling-config` ConfigMap in controller namespace
   - CAPACITY-ONLY mode (default): `WVA_EXPERIMENTAL_PROACTIVE_MODEL` unset or "false"
   - ServiceMonitor for vLLM metrics

### Environment Variables

```bash
export KUBECONFIG=~/.kube/pokprod-config
export CONTROLLER_NAMESPACE=workload-variant-autoscaler-system
export MONITORING_NAMESPACE=openshift-user-workload-monitoring
export LLMD_NAMESPACE=llm-d-inference-scheduling
export GATEWAY_NAME=infra-inference-scheduling-inference-gateway
export MODEL_ID=unsloth/Meta-Llama-3.1-8B
export DEPLOYMENT=ms-inference-scheduling-llm-d-modelservice-decode
```

---

## Running the Tests

### Quick Infrastructure Check (Run This First!)

```bash
# Check infrastructure
make verify-e2e-infrastructure

# Or manually:
kubectl get pods -n $CONTROLLER_NAMESPACE -l app.kubernetes.io/name=workload-variant-autoscaler
kubectl get cm -n $CONTROLLER_NAMESPACE capacity-scaling-config
kubectl get va -n $LLMD_NAMESPACE
kubectl get hpa -n $LLMD_NAMESPACE vllm-deployment-hpa
```

### Run All Capacity Tests

```bash
cd test/e2e-openshift
ginkgo -v --label-filter="capacity" .
```

### Run Individual Tests

**1. Configuration validation (fastest):**
```bash
ginkgo -v --focus="Capacity Model: Configuration Validation" .
```

**2. Scale-up test:**
```bash
ginkgo -v --focus="Capacity Model: Scale-Up Detection" .
```

**3. Scale-down test:**
```bash
ginkgo -v --focus="Capacity Model: Safe Scale-Down" .
```

### Run in Sequence (Recommended)

```bash
# 1. Config validation (5 min)
ginkgo -v --focus="Configuration Validation" .

# 2. Scale-up test (15 min)
ginkgo -v --focus="Scale-Up Detection" .

# 3. Scale-down test (8 min)
ginkgo -v --focus="Safe Scale-Down" .

# Total: ~28 minutes
```

---

## Test Output Interpretation

### Successful Run Example

```
Running Suite: e2e-openshift suite - ...
========================================

Capacity Model: Configuration Validation
  should have capacity-scaling-config ConfigMap present
  ✓ Found capacity-scaling-config ConfigMap with keys: [default]
  should parse and validate default capacity thresholds
  ✓ Parsed default config: kvCacheThreshold=0.80, queueLengthThreshold=5
  ...
  ✓ Capacity analyzer is functioning correctly
  ✓ Default thresholds are appropriate for current load
  ✓ System is operating within safe capacity limits

Ran 6 of 6 Specs in 4.523 seconds
SUCCESS!
```

### What to Look For

**✅ PASS indicators:**
- All specs show green checkmarks
- Status conditions show `True` for MetricsAvailable and OptimizationReady
- No saturation warnings
- Replica counts make sense (scale-up when loaded, stable when idle)

**❌ FAIL indicators:**
- Red X's on specs
- MetricsAvailable=False (check Prometheus/ServiceMonitor)
- OptimizationReady=False (check capacity analyzer logs)
- Saturation warnings (load too high for test environment)
- Job failures (check vLLM gateway availability)

---

## Troubleshooting

### Test Fails: "capacity-scaling-config ConfigMap should exist"

**Problem:** ConfigMap not found
**Solution:**
```bash
# Create default ConfigMap
kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: capacity-scaling-config
  namespace: $CONTROLLER_NAMESPACE
data:
  default: |
    kvCacheThreshold: 0.80
    queueLengthThreshold: 5
    kvSpareTrigger: 0.1
    queueSpareTrigger: 3
EOF
```

---

### Test Fails: "MetricsAvailable should be True"

**Problem:** vLLM metrics not available in Prometheus
**Diagnosis:**
```bash
# Check ServiceMonitor
kubectl get servicemonitor -n $LLMD_NAMESPACE

# Check Prometheus can scrape
kubectl logs -n $MONITORING_NAMESPACE <prometheus-pod> | grep vllm

# Query Prometheus directly
kubectl port-forward -n $MONITORING_NAMESPACE svc/prometheus 9090:9090
# Visit http://localhost:9090 and query: vllm:kv_cache_usage_perc
```

**Solution:**
- Verify vLLM deployment has metrics endpoint enabled
- Check ServiceMonitor selector matches vLLM service labels
- Verify network policies allow Prometheus scraping

---

### Test Fails: "Job should have succeeded"

**Problem:** Load generation job failed
**Diagnosis:**
```bash
# Check job status
kubectl get job -n $LLMD_NAMESPACE vllm-bench-capacity-scaleup-e2e

# Check pod logs
kubectl logs -n $LLMD_NAMESPACE -l job-name=vllm-bench-capacity-scaleup-e2e
```

**Common causes:**
- Gateway not accessible (check service/route)
- Model not loaded in vLLM
- Network policy blocking traffic
- Dataset download failed

---

### Test Fails: Instances Getting Saturated

**Problem:** Load is too high for current capacity, causing saturation
**Symptoms:**
- KV cache usage >90%
- Queue length >10
- 503 errors in job logs

**Solutions:**

1. **Reduce test load:**
   ```go
   // In capacity_scaleup_test.go, reduce:
   testRequestRate = 10  // was 15
   testNumPrompts = 1000 // was 1500
   ```

2. **Increase initial replicas:**
   ```bash
   kubectl scale deployment -n $LLMD_NAMESPACE $DEPLOYMENT --replicas=2
   ```

3. **Adjust capacity thresholds** (make more conservative):
   ```yaml
   # In capacity-scaling-config ConfigMap
   default: |
     kvCacheThreshold: 0.70  # was 0.80
     queueLengthThreshold: 3  # was 5
   ```

---

## Continuous Integration

### GitHub Actions Example

```yaml
# .github/workflows/e2e-capacity.yml
name: E2E Capacity Tests
on:
  pull_request:
    branches: [capacity-model, main]

jobs:
  e2e-capacity:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Setup Kubeconfig
        run: |
          echo "${{ secrets.POKPROD_KUBECONFIG }}" > /tmp/kubeconfig
          export KUBECONFIG=/tmp/kubeconfig

      - name: Run Config Validation
        run: |
          cd test/e2e-openshift
          ginkgo -v --focus="Configuration Validation" .

      - name: Run Capacity Tests
        run: |
          cd test/e2e-openshift
          ginkgo -v --focus="Capacity Model" .
        timeout-minutes: 30
```

---

## Test Maintenance

### Adjusting for Different Environments

**Small clusters (limited GPUs):**
```go
// Reduce load
testRequestRate = 10
testNumPrompts = 800
```

**Large clusters (many GPUs):**
```go
// Can use original values or increase
testRequestRate = 20
testNumPrompts = 3000
```

### Updating Thresholds

If default thresholds change in `capacity-scaling-config`, update test expectations:

```go
// In capacity_config_test.go
Expect(defaultConfig.KvCacheThreshold).To(BeNumerically(">=", 0.70),
    "KV cache threshold should be >= 70% for production safety")
```

---

## Test Coverage Summary

| Feature | Config Test | Scale-Up Test | Scale-Down Test |
|---------|-------------|---------------|-----------------|
| ConfigMap validation | ✅ | - | - |
| Threshold safety | ✅ | - | - |
| Status conditions | ✅ | ✅ | ✅ |
| Prometheus metrics | - | ✅ | ✅ |
| Capacity detection | - | ✅ | - |
| Scale-up recommendation | - | ✅ | - |
| Safe scale-down | - | - | ✅ |
| HPA integration | - | ✅ | ✅ |
| No saturation | ✅ | ✅ | - |
| Load generation | - | ✅ | - |

**Total test duration:** ~28 minutes
**Total test cases:** ~18 specs

---

## Next Steps After Passing

1. ✅ All capacity tests passing
2. → Run existing proactive model test: `sharegpt_scaleup_test.go` (if HYBRID mode needed)
3. → Deploy to pre-production for soak testing
4. → Monitor production metrics for 2 weeks
5. → Adjust thresholds based on real traffic patterns

---

## Related Documentation

- Main test plan: `E2E_CAPACITY_MODEL_TEST_PLAN.md`
- User guide: `docs/user-guide/configuration.md`
- CRD reference: `docs/user-guide/crd-reference.md`
- Contributing: `CONTRIBUTING.md`

---

**Last Updated:** 2025-11-21
**Test Suite Version:** 1.0
**Compatible with:** WVA capacity-model branch
