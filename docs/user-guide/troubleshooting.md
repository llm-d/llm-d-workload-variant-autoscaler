# Troubleshooting Guide

This guide helps you diagnose and resolve common issues with Workload-Variant-Autoscaler (WVA).

## Common Issues

### VariantAutoscaling Resource Has No Status

**Symptoms:**
- VA resource created but `.status` field remains empty
- VA never shows current or desired replica counts
- No reconciliation appears to be happening

**Causes and Solutions:**

#### 1. Target Deployment Not Found (Race Condition)

**Problem:** The VariantAutoscaling resource was created before its target deployment exists. This commonly occurs in Helm deployments where resources are applied in sequence.

**How to Verify:**
```bash
# Check if the VA references an existing deployment
kubectl get va <va-name> -n <namespace> -o yaml | grep scaleTargetRef -A 3

# Check if that deployment exists
kubectl get deployment <deployment-name> -n <namespace>
```

**Solution:** 
Starting from v0.5.0, WVA automatically handles this race condition. The controller:
1. Watches Deployment creation events
2. Automatically reconciles VAs when their target deployment is created
3. No manual intervention required

For older versions, you can:
- Delete and recreate the VA after the deployment is ready
- Or restart the WVA controller pod to trigger reconciliation

```bash
# Delete and recreate the VA (old versions only)
kubectl delete va <va-name> -n <namespace>
kubectl apply -f <va-manifest.yaml>
```

#### 2. Missing Required Fields

**Problem:** The VA spec is missing required fields like `modelID` or `scaleTargetRef`.

**How to Verify:**
```bash
kubectl describe va <va-name> -n <namespace>
# Look for validation errors in Events section
```

**Solution:** Ensure your VA includes all required fields:
```yaml
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: example-autoscaler
  namespace: llm-inference
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: my-deployment  # Required
  modelID: "meta/llama-3.1-8b"  # Required
```

#### 3. Controller Not Running

**Problem:** The WVA controller pod is not running or is crashing.

**How to Verify:**
```bash
kubectl get pods -n workload-variant-autoscaler-system
kubectl logs -n workload-variant-autoscaler-system <controller-pod-name>
```

**Solution:** Check controller logs for errors and ensure:
- Controller has proper RBAC permissions
- Prometheus is accessible if using Prometheus metrics
- Required ConfigMaps exist

### Metrics Not Available

**Symptoms:**
- VA status shows "No saturation metrics available"
- Autoscaling decisions not being made
- Empty `.status.currentAlloc` fields

**Causes and Solutions:**

#### 1. Prometheus Not Configured

**Problem:** WVA cannot reach Prometheus or Prometheus is not scraping the target pods.

**How to Verify:**
```bash
# Check if Prometheus is configured in WVA
kubectl get configmap -n workload-variant-autoscaler-system workload-variant-autoscaler-variantautoscaling-config -o yaml

# Check if ServiceMonitor exists for your inference workload
kubectl get servicemonitor -n <namespace>

# Test Prometheus connectivity from controller pod
kubectl exec -n workload-variant-autoscaler-system <controller-pod> -- curl -k https://prometheus-service:9090/api/v1/query?query=up
```

**Solution:**
- Ensure Prometheus is configured in WVA Helm values:
  ```yaml
  prometheus:
    enabled: true
    url: "https://prometheus-service:9090"
    caCert: "<base64-encoded-ca-cert>"
  ```
- Verify ServiceMonitor is properly configured to scrape your inference pods
- Check Prometheus is successfully scraping metrics from inference pods

#### 2. Pods Not Ready

**Problem:** Target deployment pods are not ready or not serving metrics.

**How to Verify:**
```bash
kubectl get pods -n <namespace> -l <deployment-selector>
kubectl logs -n <namespace> <pod-name>
```

**Solution:**
- Wait for pods to become Ready
- Ensure vLLM or inference server is properly configured to expose metrics
- Check pod readiness probes are passing

#### 3. Missing Labels on VA

**Problem:** The VariantAutoscaling resource is missing required labels for metrics collection.

**How to Verify:**
```bash
kubectl get va <va-name> -n <namespace> -o yaml | grep labels: -A 5
```

**Solution:** Ensure VA has the required label:
```yaml
metadata:
  labels:
    inference.optimization/acceleratorName: "A100"  # Required for metrics collection
```

### Autoscaling Not Triggering

**Symptoms:**
- VA has status but replica count never changes
- HPA/KEDA not scaling the deployment
- Desired replicas calculated but not applied

**Causes and Solutions:**

#### 1. HPA/KEDA Not Configured

**Problem:** No autoscaler (HPA or KEDA) is watching the WVA metrics.

**How to Verify:**
```bash
# Check if HPA exists
kubectl get hpa -n <namespace>

# Check if KEDA ScaledObject exists
kubectl get scaledobject -n <namespace>
```

**Solution:** Configure HPA or KEDA to read WVA metrics. See:
- [HPA Integration Guide](../integrations/hpa-integration.md)
- [KEDA Integration Guide](../integrations/keda-integration.md)

#### 2. Metrics Not Exported to Prometheus

**Problem:** WVA is not exporting optimized replica metrics to Prometheus.

**How to Verify:**
```bash
# Check if WVA controller ServiceMonitor exists
kubectl get servicemonitor -n workload-variant-autoscaler-system workload-variant-autoscaler-controller-manager-metrics-monitor

# Query Prometheus for WVA metrics
# Access Prometheus UI and query:
workload_optimized_replicas{model_id="meta/llama-3.1-8b"}
```

**Solution:**
- Ensure WVA controller ServiceMonitor exists and is being scraped by Prometheus
- Check WVA controller logs for errors in metrics emission
- Verify Prometheus is scraping the WVA controller metrics endpoint

#### 3. Stabilization Window Too Long

**Problem:** HPA stabilization window prevents scaling changes.

**How to Verify:**
```bash
kubectl describe hpa <hpa-name> -n <namespace>
# Look for "ScaleDown:" and "ScaleUp:" stabilization settings
```

**Solution:** Adjust HPA behavior for faster scaling:
```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: my-hpa
spec:
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 120  # Reduce if too conservative
    scaleUp:
      stabilizationWindowSeconds: 60   # Reduce if too conservative
```

### Namespace Stuck in Terminating State

**Symptoms:**
- Cannot recreate namespace after deletion
- Helm install fails with "namespace is being terminated"
- E2E tests fail on subsequent runs

**Problem:** Previous cleanup left namespace with finalizers or resources that prevent deletion.

**Solution:** Force-delete the namespace:
```bash
# Remove finalizers from namespace
kubectl get namespace <namespace> -o json | \
  jq '.spec.finalizers = []' | \
  kubectl replace --raw "/api/v1/namespaces/<namespace>/finalize" -f -

# Or use the force-delete script (if available)
./hack/force-delete-namespace.sh <namespace>
```

**Prevention:** Starting from v0.5.0, WVA deployment scripts automatically detect and handle terminating namespaces.

## Debugging Techniques

### Enable Debug Logging

Increase log verbosity to see detailed reconciliation logic:

```bash
# Edit the controller deployment
kubectl edit deployment -n workload-variant-autoscaler-system workload-variant-autoscaler-controller-manager

# Add or modify the --v flag
spec:
  template:
    spec:
      containers:
      - args:
        - --v=2  # Set to 2 for debug logs, 3 for trace logs
```

### Check Controller Events

View events emitted by the controller:

```bash
# Events for a specific VA
kubectl describe va <va-name> -n <namespace>

# All events in namespace
kubectl get events -n <namespace> --sort-by='.lastTimestamp'

# Filter for WVA controller events
kubectl get events -n <namespace> --field-selector involvedObject.kind=VariantAutoscaling
```

### Inspect Prometheus Metrics

Query Prometheus directly to verify WVA is collecting and emitting metrics:

```bash
# Port-forward to Prometheus
kubectl port-forward -n <prometheus-namespace> svc/<prometheus-service> 9090:9090

# Open browser to http://localhost:9090 and query:

# Check if vLLM metrics are available
vllm_kv_cache_usage_perc{model_name="meta/llama-3.1-8b"}

# Check if WVA is emitting optimized replicas
workload_optimized_replicas{model_id="meta/llama-3.1-8b"}

# Check if WVA is collecting request rates
vllm_request_success_total{model_name="meta/llama-3.1-8b"}
```

### Run Controller Locally

For deep debugging, run the controller locally against a remote cluster:

See [Developer Debugging Guide](../developer-guide/debugging.md) for detailed instructions on:
- Running controller locally with SSH tunnel to cluster
- Connecting to remote Prometheus
- Using IDE debuggers with breakpoints

## Getting Help

If you're still experiencing issues:

1. **Check logs:**
   ```bash
   kubectl logs -n workload-variant-autoscaler-system <controller-pod-name> --tail=100
   ```

2. **Gather diagnostics:**
   ```bash
   kubectl describe va <va-name> -n <namespace>
   kubectl get deployment <deployment-name> -n <namespace> -o yaml
   kubectl get hpa <hpa-name> -n <namespace> -o yaml
   ```

3. **File an issue:** [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
   - Include WVA version, Kubernetes version, and error logs
   - Describe steps to reproduce the issue
   - Attach relevant YAML manifests (redact sensitive data)

4. **Join the community:** [llm-d Slack channel](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)

## Known Limitations

- **Architecture Dependencies:** WVA performance models are calibrated for dense transformer architectures. See [Architecture Limitations](../design/architecture-limitations.md) for HSSM, MoE, and custom architectures.
- **Cold Start Delay:** First reconciliation may take 60-90 seconds while metrics are collected.
- **Scale-to-Zero:** Requires HPA alpha feature or KEDA for automatic scale-up from zero replicas.
