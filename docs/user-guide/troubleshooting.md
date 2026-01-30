# Troubleshooting Guide

This guide helps you diagnose and resolve common issues with Workload-Variant-Autoscaler (WVA).

## Table of Contents

- [Installation Issues](#installation-issues)
- [Controller Not Starting](#controller-not-starting)
- [Scaling Not Working](#scaling-not-working)
- [Metrics Issues](#metrics-issues)
- [Performance Problems](#performance-problems)
- [CRD and API Issues](#crd-and-api-issues)
- [Integration Issues](#integration-issues)
- [Getting Help](#getting-help)

---

## Installation Issues

### Helm Installation Fails

**Symptom:** `helm install` command fails with errors.

**Common Causes:**

1. **CRDs already exist from previous installation:**
   ```bash
   # Check for existing CRDs
   kubectl get crd variantautoscalings.llmd.ai
   
   # If exists, delete and reinstall
   kubectl delete crd variantautoscalings.llmd.ai
   helm install workload-variant-autoscaler ./charts/workload-variant-autoscaler \
     --namespace workload-variant-autoscaler-system \
     --create-namespace
   ```

2. **Namespace already exists:**
   ```bash
   # Use --create-namespace flag or create manually
   kubectl create namespace workload-variant-autoscaler-system
   ```

3. **Insufficient permissions:**
   ```bash
   # Verify you have cluster-admin privileges
   kubectl auth can-i '*' '*' --all-namespaces
   ```

### CRD Update Issues During Upgrade

**Symptom:** After upgrading, VariantAutoscaling resources show validation errors.

**Solution:** Helm doesn't auto-update CRDs. Manually apply them first:

```bash
# Apply updated CRDs
kubectl apply -f charts/workload-variant-autoscaler/crds/

# Verify CRD version
kubectl get crd variantautoscalings.llmd.ai -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties}' | jq 'keys'

# Then upgrade Helm release
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system
```

---

## Controller Not Starting

### Pod Stuck in CrashLoopBackOff

**Symptom:** Controller pod keeps restarting.

**Diagnosis:**

```bash
# Check pod status
kubectl get pods -n workload-variant-autoscaler-system

# View logs
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager

# Check events
kubectl get events -n workload-variant-autoscaler-system --sort-by='.lastTimestamp'
```

**Common Causes:**

1. **Prometheus connection issues:**
   - Verify Prometheus URL in ConfigMap
   - Check TLS certificate if using HTTPS
   - Test connectivity: `kubectl exec -it <pod> -- curl <prometheus-url>`

2. **Invalid configuration:**
   ```bash
   # Check ConfigMap
   kubectl get configmap -n workload-variant-autoscaler-system wva-config -o yaml
   ```

3. **RBAC issues:**
   ```bash
   # Verify ServiceAccount and ClusterRole bindings
   kubectl get clusterrolebinding | grep workload-variant-autoscaler
   kubectl describe clusterrolebinding workload-variant-autoscaler-wva-clusterrolebinding
   ```

### ImagePullBackOff

**Symptom:** Pod cannot pull container image.

**Solution:**

```bash
# Check image pull secrets
kubectl get pods -n workload-variant-autoscaler-system -o jsonpath='{.items[*].spec.imagePullSecrets}'

# Verify image exists
docker pull quay.io/llm-d/workload-variant-autoscaler:latest

# Update image or add pull secret in values.yaml
```

---

## Scaling Not Working

### Deployment Not Scaling Despite VA Resource

**Symptom:** VariantAutoscaling resource exists, but deployment replica count doesn't change.

**Diagnosis Steps:**

1. **Check VA status:**
   ```bash
   kubectl get variantautoscaling -A -o yaml
   kubectl describe variantautoscaling <name> -n <namespace>
   ```

2. **Verify HPA/KEDA is configured:**
   ```bash
   # Check for HPA
   kubectl get hpa -A
   
   # Check HPA configuration
   kubectl describe hpa <hpa-name> -n <namespace>
   
   # Or check for KEDA ScaledObject
   kubectl get scaledobject -A
   ```

3. **Check metrics availability:**
   ```bash
   # Verify WVA metrics in Prometheus
   kubectl port-forward -n monitoring svc/prometheus 9090:9090
   # Then query: wva_desired_replicas{namespace="your-namespace"}
   
   # Check if HPA can read metrics
   kubectl get hpa <hpa-name> -n <namespace> -o yaml
   # Look for currentMetrics field
   ```

4. **Verify deployment exists:**
   ```bash
   kubectl get deployment <deployment-name> -n <namespace>
   ```

**Common Causes:**

- **No HPA/KEDA configured** - WVA only emits metrics, doesn't scale directly
- **Metrics not being scraped** - Check Prometheus ServiceMonitor
- **HPA stabilization window** - HPA may delay scaling (120s recommended)
- **Min/max replicas constraints** - Check HPA min/max settings

### VA Status Shows "Deployment Not Found"

**Symptom:** VA status condition shows deployment missing.

**Solution:**

```bash
# Check if deployment exists in same namespace
kubectl get deployment -n <namespace>

# Verify scaleTargetRef is correct
kubectl get variantautoscaling <name> -n <namespace> -o yaml

# Example correct configuration:
spec:
  scaleTargetRef:
    kind: Deployment
    name: llama-8b  # Must match actual deployment name
```

### Slow Scaling Response

**Symptom:** Deployment scales, but slowly.

**Check:**

1. **HPA stabilization window:**
   ```yaml
   behavior:
     scaleUp:
       stabilizationWindowSeconds: 120  # Increase for slower scaling
     scaleDown:
       stabilizationWindowSeconds: 300
   ```

2. **Metric scraping interval:**
   ```yaml
   # In WVA ConfigMap
   metricsScrapingIntervalSeconds: 15  # Default
   ```

3. **HPA sync period:**
   ```bash
   # Check kube-controller-manager flags
   --horizontal-pod-autoscaler-sync-period=15s  # Default
   ```

---

## Metrics Issues

### WVA Metrics Not Appearing in Prometheus

**Symptom:** Prometheus doesn't show `wva_*` metrics.

**Diagnosis:**

1. **Check ServiceMonitor:**
   ```bash
   kubectl get servicemonitor -n workload-variant-autoscaler-system
   kubectl describe servicemonitor workload-variant-autoscaler-controller-manager-metrics-monitor \
     -n workload-variant-autoscaler-system
   ```

2. **Verify Prometheus is scraping:**
   ```bash
   # Port-forward to Prometheus
   kubectl port-forward -n monitoring svc/prometheus 9090:9090
   
   # Check targets: http://localhost:9090/targets
   # Look for workload-variant-autoscaler-system/workload-variant-autoscaler-controller-manager-metrics-monitor
   ```

3. **Check metric endpoint directly:**
   ```bash
   kubectl port-forward -n workload-variant-autoscaler-system \
     deployment/workload-variant-autoscaler-controller-manager 8443:8443
   
   # Query metrics (may need TLS)
   curl -k https://localhost:8443/metrics
   ```

**Common Causes:**

- Prometheus Operator not installed
- ServiceMonitor namespace label doesn't match Prometheus config
- Network policies blocking access
- TLS certificate issues

### vLLM Metrics Missing

**Symptom:** WVA can't collect vLLM server metrics.

**Check:**

1. **vLLM metrics endpoint:**
   ```bash
   kubectl port-forward -n <namespace> <vllm-pod> 8000:8000
   curl http://localhost:8000/metrics
   ```

2. **ServiceMonitor for vLLM:**
   ```bash
   kubectl get servicemonitor -n <namespace>
   ```

3. **Prometheus scraping vLLM:**
   - Check Prometheus targets for vLLM pods
   - Verify service labels match ServiceMonitor selector

---

## Performance Problems

### High Controller CPU/Memory Usage

**Symptom:** Controller pod consuming excessive resources.

**Diagnosis:**

```bash
# Check resource usage
kubectl top pod -n workload-variant-autoscaler-system

# Check for excessive reconciliation
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager | grep "Reconciling"
```

**Solutions:**

1. **Increase resource limits:**
   ```yaml
   # In values.yaml
   resources:
     limits:
       cpu: 500m
       memory: 512Mi
   ```

2. **Reduce reconciliation frequency** (if applicable in future versions)

3. **Check for metric query issues:**
   - Large time ranges
   - High cardinality metrics

### Slow Metric Queries

**Symptom:** Controller logs show slow Prometheus queries.

**Check:**

1. **Prometheus performance:**
   ```bash
   # Check Prometheus metrics
   kubectl port-forward -n monitoring svc/prometheus 9090:9090
   # Query: prometheus_http_request_duration_seconds
   ```

2. **Query complexity:**
   - Check WVA log for actual PromQL queries
   - Verify time ranges are reasonable
   - Consider Prometheus query timeout settings

---

## CRD and API Issues

### Validation Errors Creating VariantAutoscaling

**Symptom:** `kubectl apply` fails with validation error.

**Check CRD version:**

```bash
# Get CRD schema
kubectl get crd variantautoscalings.llmd.ai -o yaml

# Verify your YAML matches schema
kubectl explain variantautoscaling.spec
```

**Common Issues:**

1. **Missing required fields:**
   ```yaml
   # Required in v0.5.0+
   spec:
     scaleTargetRef:  # Must be present
       kind: Deployment
       name: my-deployment
     modelID: "meta/llama-3.1-8b"
   ```

2. **Invalid field types:**
   - `variantCost` must be a string: `"10.0"`, not `10.0`

### CRD Version Mismatch

**Symptom:** Old VariantAutoscaling resources don't work after upgrade.

**Solution:**

```bash
# Backup existing VAs
kubectl get variantautoscaling -A -o yaml > va-backup.yaml

# Update CRDs
kubectl apply -f charts/workload-variant-autoscaler/crds/

# Update existing VAs if needed
kubectl edit variantautoscaling <name> -n <namespace>
```

---

## Integration Issues

### HPA Not Reading WVA Metrics

**Symptom:** HPA shows "unable to get external metric" or "unknown".

**Check:**

1. **Prometheus Adapter installed:**
   ```bash
   kubectl get apiservice v1beta1.external.metrics.k8s.io
   kubectl get pods -n monitoring | grep prometheus-adapter
   ```

2. **Prometheus Adapter configuration:**
   ```bash
   kubectl get configmap -n monitoring prometheus-adapter -o yaml
   ```

   Verify it includes WVA metrics:
   ```yaml
   rules:
   - seriesQuery: 'wva_desired_replicas'
     resources:
       overrides:
         namespace: {resource: "namespace"}
     name:
       matches: "^wva_(.*)$"
       as: "${1}"
     metricsQuery: 'wva_<<.Series>>{<<.LabelMatchers>>}'
   ```

3. **Test metric availability:**
   ```bash
   kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/wva_desired_replicas" | jq
   ```

### KEDA Not Triggering Scaling

**Symptom:** ScaledObject exists but doesn't scale deployment.

**Check:**

1. **KEDA operator running:**
   ```bash
   kubectl get pods -n keda
   ```

2. **ScaledObject status:**
   ```bash
   kubectl describe scaledobject <name> -n <namespace>
   ```

3. **Prometheus scaler configuration:**
   ```yaml
   triggers:
   - type: prometheus
     metadata:
       serverAddress: http://prometheus:9090
       query: wva_desired_replicas{namespace="my-namespace"}
       threshold: '1'
   ```

---

## Getting Help

### Collecting Diagnostic Information

When opening an issue, include:

```bash
# 1. WVA version
helm list -n workload-variant-autoscaler-system

# 2. Controller logs
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager --tail=100 > wva-logs.txt

# 3. VA resources
kubectl get variantautoscaling -A -o yaml > va-resources.yaml

# 4. HPA/KEDA status
kubectl get hpa -A -o yaml > hpa-status.yaml
kubectl get scaledobject -A -o yaml > keda-status.yaml

# 5. Events
kubectl get events -A --sort-by='.lastTimestamp' | grep -i variant > events.txt

# 6. CRD version
kubectl get crd variantautoscalings.llmd.ai -o yaml > crd.yaml
```

### Enable Debug Logging

**Increase controller verbosity:**

```bash
# Edit deployment
kubectl edit deployment -n workload-variant-autoscaler-system \
  workload-variant-autoscaler-controller-manager

# Add/modify args:
spec:
  template:
    spec:
      containers:
      - name: manager
        args:
        - --zap-log-level=debug  # Add this line
```

### Common Log Messages

| Log Message | Meaning | Action |
|-------------|---------|--------|
| `Failed to get metrics from Prometheus` | Prometheus connection issue | Check Prometheus URL, TLS config |
| `Deployment not found` | Target deployment missing | Verify scaleTargetRef |
| `No metrics available for variant` | vLLM metrics not found | Check vLLM ServiceMonitor |
| `Reconciliation error` | Controller issue | Check full error message, verify RBAC |

### Still Need Help?

- **Documentation:** [User Guide](../README.md), [FAQ](faq.md)
- **GitHub Issues:** [Open an issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- **Community:** [Join llm-d meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)

---

**Pro Tip:** Enable debug logging when troubleshooting, but remember to disable it in production to avoid excessive log volume.
