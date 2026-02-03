# Troubleshooting Guide

This guide helps you diagnose and resolve common issues with Workload-Variant-Autoscaler.

## Quick Diagnostics

### Check WVA Controller Health

```bash
# Check controller pods are running
kubectl get pods -n workload-variant-autoscaler-system

# Expected output:
# NAME                                                       READY   STATUS    RESTARTS   AGE
# workload-variant-autoscaler-controller-manager-xxx-xxx    2/2     Running   0          10m

# Check controller logs
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -c manager --tail=100
```

### Check VariantAutoscaling Status

```bash
# List all VAs
kubectl get va -A

# Get detailed status
kubectl describe va <name> -n <namespace>

# Check conditions
kubectl get va <name> -n <namespace> -o jsonpath='{.status.conditions}' | jq
```

### Verify Metrics Availability

```bash
# Check if metrics are being emitted
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/inferno_desired_replicas" | jq

# Check custom metrics API is available
kubectl get apiservices | grep metrics
```

## Common Issues

### 1. Controller Not Starting

**Symptoms:**
- Controller pod in CrashLoopBackOff
- Pod shows error in logs
- No controller logs available

**Diagnosis:**

```bash
# Check pod status
kubectl get pods -n workload-variant-autoscaler-system

# View pod events
kubectl describe pod <controller-pod-name> -n workload-variant-autoscaler-system

# Check logs
kubectl logs <controller-pod-name> -n workload-variant-autoscaler-system -c manager
```

**Common Causes & Solutions:**

**a) Missing CRDs**
```bash
# Check if CRD exists
kubectl get crd variantautoscalings.llmd.ai

# If missing, install CRDs
kubectl apply -f charts/workload-variant-autoscaler/crds/
```

**b) RBAC Permissions Issues**
```bash
# Verify ServiceAccount exists
kubectl get sa -n workload-variant-autoscaler-system

# Check ClusterRoleBindings
kubectl get clusterrolebinding | grep workload-variant-autoscaler
```

**c) Image Pull Errors**
```bash
# Check image pull policy and credentials
kubectl describe pod <controller-pod-name> -n workload-variant-autoscaler-system | grep -A5 "Events"

# Verify image exists
docker pull ghcr.io/llm-d/workload-variant-autoscaler:latest
```

**d) Configuration Errors**
```bash
# Check ConfigMaps
kubectl get configmap -n workload-variant-autoscaler-system

# Verify Prometheus configuration
kubectl get configmap -n workload-variant-autoscaler-system -o yaml | grep PROMETHEUS
```

### 2. VariantAutoscaling Shows "DeploymentNotFound"

**Symptoms:**
- VA status condition shows `DeploymentNotFound`
- Metrics not emitted
- No scaling occurs

**Diagnosis:**

```bash
# Check VA status
kubectl get va <name> -n <namespace> -o yaml

# Check for target deployment
kubectl get deployment <target-name> -n <namespace>
```

**Solutions:**

**a) Deployment Doesn't Exist**
```bash
# Create the deployment first
kubectl apply -f deployment.yaml

# Wait for deployment to be ready
kubectl wait --for=condition=available deployment/<name> --timeout=300s
```

**b) Name Mismatch**
```yaml
# Verify scaleTargetRef matches deployment name exactly
spec:
  scaleTargetRef:
    kind: Deployment
    name: llama-8b  # Must match deployment metadata.name
  modelID: "meta/llama-3.1-8b"
```

**c) Namespace Mismatch**
```bash
# Ensure VA and deployment are in same namespace
kubectl get va <name> -n <namespace>
kubectl get deployment <target> -n <namespace>
```

### 3. Metrics Not Available

**Symptoms:**
- VA condition `MetricsAvailable` is False
- HPA shows "unable to get external metric"
- No `inferno_desired_replicas` metric

**Diagnosis:**

```bash
# Check VA metrics condition
kubectl get va <name> -n <namespace> -o jsonpath='{.status.conditions[?(@.type=="MetricsAvailable")]}'

# Check controller logs for metric-related errors
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -c manager | grep -i "metric\|prometheus"

# Verify Prometheus connectivity
kubectl exec -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -c manager -- curl -k $PROMETHEUS_BASE_URL/api/v1/status/config
```

**Common Causes & Solutions:**

**a) Prometheus Not Configured**
```yaml
# Check environment variables in deployment
env:
  - name: PROMETHEUS_BASE_URL
    value: "https://prometheus.example.com"  # Must be set
  - name: PROMETHEUS_CA_CERT_PATH
    value: "/etc/prometheus-certs/ca.crt"
```

**b) TLS/Authentication Issues**
```bash
# Check if certificates are mounted
kubectl describe pod -n workload-variant-autoscaler-system \
  -l control-plane=controller-manager | grep -A10 "Mounts"

# Verify certificate secret exists
kubectl get secret -n workload-variant-autoscaler-system | grep prometheus

# Test connectivity manually
kubectl exec -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -c manager -- curl -v --cacert /path/to/ca.crt https://prometheus.example.com
```

**c) ServiceMonitor Not Scraping**
```bash
# Check if ServiceMonitor exists for inference server
kubectl get servicemonitor -n <namespace>

# Verify Prometheus targets
# Access Prometheus UI -> Status -> Targets
# Look for your inference server endpoints
```

**d) Inference Server Not Exposing Metrics**
```bash
# Check if vLLM/inference server is exposing metrics
kubectl port-forward -n <namespace> deployment/<inference-deployment> 8000:8000

# Query metrics endpoint
curl http://localhost:8000/metrics
```

### 4. HPA Not Scaling

**Symptoms:**
- HPA shows "unable to get external metric"
- HPA status shows unknown/invalid
- Deployment not scaling despite metrics

**Diagnosis:**

```bash
# Check HPA status
kubectl describe hpa <hpa-name> -n <namespace>

# Check custom metrics API
kubectl get apiservices v1beta1.external.metrics.k8s.io -o yaml

# Verify metric is available
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/inferno_desired_replicas"
```

**Solutions:**

**a) Custom Metrics API Not Available**
```bash
# Check if prometheus-adapter or KEDA is installed
kubectl get pods -n monitoring  # or your monitoring namespace

# For prometheus-adapter:
kubectl get pods -n kube-system | grep prometheus-adapter

# For KEDA:
kubectl get pods -n keda
```

**b) HPA Configuration Issues**
```yaml
# Ensure HPA references correct metric
metrics:
- type: External
  external:
    metric:
      name: inferno_desired_replicas
      selector:
        matchLabels:
          variantautoscaling: <va-name>  # Must match VA name
    target:
      type: Value
      value: "1"
```

**c) Metric Name Mismatch**
```bash
# List available external metrics
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1" | jq

# Verify VA is emitting metrics
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  | grep "Emitted metric"
```

### 5. Scaling Too Aggressive or Conservative

**Symptoms:**
- Rapid scale-up/scale-down cycles
- Deployment scales when it shouldn't
- Deployment doesn't scale when needed

**Solutions:**

**a) Adjust HPA Stabilization Windows**
```yaml
behavior:
  scaleUp:
    stabilizationWindowSeconds: 180  # Increase for more conservative scale-up
    policies:
    - type: Percent
      value: 50  # Scale by max 50% at a time
      periodSeconds: 60
  scaleDown:
    stabilizationWindowSeconds: 300  # Longer window for scale-down
    policies:
    - type: Pods
      value: 1  # Scale down 1 pod at a time
      periodSeconds: 120
```

**b) Adjust Saturation Thresholds**
```yaml
# Edit capacity-scaling-config ConfigMap
apiVersion: v1
kind: ConfigMap
metadata:
  name: capacity-scaling-config
  namespace: workload-variant-autoscaler-system
data:
  saturationConfig: |
    kv_cache_threshold: 0.85  # Increase for less aggressive scaling
    queue_depth_threshold: 5   # Increase to tolerate more queuing
```

**c) Review Variant Costs**
```yaml
# Ensure costs are set appropriately
spec:
  modelID: "meta/llama-3.1-8b"
  variantCost: "40.0"  # Adjust based on actual costs
```

### 6. Safety Net Keeps Activating

**Symptoms:**
- Controller logs show "Safety net activated" frequently
- Metrics show fallback values
- HPA receives stale metrics

**Diagnosis:**

```bash
# Check for safety net messages
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  | grep "Safety net activated"
```

**Solutions:**

**a) Prometheus Connectivity Issues**
```bash
# Test Prometheus connectivity
kubectl exec -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -c manager -- curl -v $PROMETHEUS_BASE_URL/api/v1/query?query=up
```

**b) Missing Metrics from Inference Server**
```bash
# Verify inference server is exposing metrics
kubectl port-forward -n <namespace> deployment/<inference-deployment> 8000:8000
curl http://localhost:8000/metrics | grep vllm

# Check if ServiceMonitor is created
kubectl get servicemonitor -n <namespace>
```

**c) Network Policies Blocking Access**
```bash
# Check network policies
kubectl get networkpolicy -n workload-variant-autoscaler-system
kubectl get networkpolicy -n <inference-namespace>

# Verify controller can reach Prometheus
kubectl exec -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -c manager -- nslookup prometheus.monitoring.svc.cluster.local
```

### 7. Multi-Controller Conflicts

**Symptoms:**
- Multiple controllers managing same VAs
- Metric conflicts
- Unexpected scaling behavior

**Diagnosis:**

```bash
# Check for multiple controller instances
kubectl get pods -A | grep workload-variant-autoscaler-controller

# Check VA labels
kubectl get va <name> -n <namespace> -o yaml | grep -A5 "labels"
```

**Solution:**

Use controller instance isolation:

```yaml
# Helm values for each controller
wva:
  controllerInstance: "instance-a"
```

And label VAs accordingly:

```yaml
metadata:
  labels:
    wva.llmd.ai/controller-instance: "instance-a"
```

See [Multi-Controller Isolation Guide](multi-controller-isolation.md).

## Debugging Techniques

### Enable Debug Logging

```yaml
# In controller deployment
env:
  - name: LOG_LEVEL
    value: "debug"
```

### Capture Controller Traces

```bash
# Stream logs with timestamps
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -c manager -f --timestamps

# Save logs to file
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  -c manager --since=1h > wva-controller.log
```

### Inspect Custom Resources

```bash
# Get VA with full status
kubectl get va <name> -n <namespace> -o yaml

# Watch VA status changes
kubectl get va <name> -n <namespace> -w -o jsonpath='{.status.conditions}'

# Check VA events
kubectl get events -n <namespace> --field-selector involvedObject.name=<va-name>
```

### Test Prometheus Queries Manually

```bash
# Port-forward to Prometheus
kubectl port-forward -n monitoring svc/prometheus 9090:9090

# Test queries in browser: http://localhost:9090
# Example query:
vllm:kv_cache_usage_perc{namespace="llm-inference"}
vllm:num_requests_waiting{namespace="llm-inference"}
```

### Remote Debugging

For advanced debugging with remote clusters, see [Debugging Guide](../developer-guide/debugging.md).

## Getting Help

If you're still experiencing issues:

1. **Check the FAQ**: [FAQ](faq.md) covers many common questions
2. **Search GitHub Issues**: [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
3. **Open a New Issue**: Include:
   - WVA version (`kubectl get deployment -n workload-variant-autoscaler-system -o yaml | grep image:`)
   - Kubernetes/OpenShift version (`kubectl version`)
   - VA status (`kubectl get va <name> -n <namespace> -o yaml`)
   - Controller logs (last 100 lines)
   - Steps to reproduce
4. **Join Community Meetings**: [Slack](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)

## Additional Resources

- [Installation Guide](installation.md)
- [Configuration Guide](configuration.md)
- [Developer Guide](../developer-guide/development.md)
- [Debugging Guide](../developer-guide/debugging.md)
- [FAQ](faq.md)

---

**Remember:** Always check controller logs first - they contain valuable diagnostic information!
