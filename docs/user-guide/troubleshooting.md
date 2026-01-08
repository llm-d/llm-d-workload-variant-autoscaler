# Troubleshooting Guide

This guide helps diagnose and resolve common issues with Workload-Variant-Autoscaler (WVA).

## Quick Diagnostic Commands

```bash
# Check WVA controller status
kubectl get pods -n workload-variant-autoscaler-system

# Check VariantAutoscaling resources
kubectl get variantautoscaling -A

# View detailed VA status
kubectl describe variantautoscaling <name> -n <namespace>

# Check WVA controller logs
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager -f

# Check HPA status
kubectl get hpa -n <namespace>
kubectl describe hpa <name> -n <namespace>
```

## Common Issues

### 1. MetricsAvailable: False

**Symptom:** VariantAutoscaling status shows `MetricsAvailable: False`

**Possible Causes:**

#### A. ServiceMonitor Not Configured

Check if your vLLM service has a ServiceMonitor:

```bash
kubectl get servicemonitor -n <namespace>
kubectl describe servicemonitor <name> -n <namespace>
```

**Solution:** Create a ServiceMonitor for your vLLM service:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: vllm-monitor
  namespace: <namespace>
spec:
  selector:
    matchLabels:
      app: vllm
  endpoints:
  - port: metrics
    path: /metrics
    interval: 30s
```

#### B. Prometheus Not Scraping

Verify Prometheus is discovering and scraping the target:

```bash
# Port forward to Prometheus
kubectl port-forward -n monitoring svc/prometheus 9090:9090

# Check targets at http://localhost:9090/targets
# Look for your vLLM service
```

**Solution:** 
- Ensure ServiceMonitor labels match Prometheus' `serviceMonitorSelector`
- Check Prometheus RBAC permissions
- Verify network policies allow Prometheus to reach the pods

#### C. Metrics Are Stale

Check the timestamp of metrics in Prometheus:

```bash
# Query Prometheus for metric age
curl -G http://localhost:9090/api/v1/query \
  --data-urlencode 'query=time() - timestamp(vllm:gpu_cache_usage_perc{namespace="<namespace>"})'
```

If the value is > 300 seconds (5 minutes), metrics are considered stale.

**Solution:**
- Check if vLLM pods are running and healthy
- Verify vLLM is exposing metrics on `/metrics` endpoint
- Increase Prometheus scrape interval if needed

#### D. Wrong Metric Names

WVA expects specific vLLM metric names. Verify they exist:

```bash
# Check available metrics
kubectl exec -n <namespace> <vllm-pod> -- curl localhost:8000/metrics | grep vllm
```

Required metrics:
- `vllm:gpu_cache_usage_perc`
- `vllm:num_requests_waiting`
- `vllm:num_requests_running`

**Solution:** Ensure vLLM version supports these metrics (vLLM 0.4.0+).

### 2. OptimizationReady: False

**Symptom:** Status shows `OptimizationReady: False` with reason `OptimizationFailed`

**Diagnosis:**

```bash
# Check controller logs for optimization errors
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  | grep -i "optimization\|error"
```

**Common Causes:**

#### A. Invalid Configuration

Check ConfigMap settings:

```bash
kubectl get configmap -n workload-variant-autoscaler-system wva-config-saturation-scaling -o yaml
```

**Solution:** Verify configuration values are within valid ranges:
- `target_utilization`: 0.0 - 1.0
- `min_replicas`: >= 0
- `max_replicas`: > min_replicas

#### B. Missing Deployment

The target deployment doesn't exist:

```bash
kubectl get deployment <target-name> -n <namespace>
```

**Solution:** 
- Create the target deployment
- Verify `scaleTargetRef` in VariantAutoscaling spec matches the deployment name
- WVA will automatically detect when the deployment appears

#### C. Insufficient Metrics Data

Not enough historical data for optimization:

**Solution:** Wait 2-3 reconciliation cycles (default: 2-3 minutes) for WVA to collect sufficient metrics.

### 3. Scaling Not Happening

**Symptom:** WVA reports desired replicas, but deployment doesn't scale

**Diagnosis:**

```bash
# Check if HPA is reading WVA metrics
kubectl describe hpa <name> -n <namespace>

# Look for:
# - Current metrics value
# - Desired replicas
# - Any error messages
```

**Common Causes:**

#### A. HPA Not Configured

**Solution:** Create HPA for the deployment:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: vllm-hpa
  namespace: <namespace>
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: <deployment-name>
  minReplicas: 1
  maxReplicas: 10
  metrics:
  - type: External
    external:
      metric:
        name: inferno_desired_ratio
        selector:
          matchLabels:
            variant_name: <va-name>
            namespace: <namespace>
      target:
        type: Value
        value: "1"
```

See [HPA Integration](../integrations/hpa-integration.md) for complete configuration.

#### B. Prometheus Adapter Not Configured

HPA needs Prometheus Adapter to read custom metrics:

```bash
kubectl get apiservice v1beta1.external.metrics.k8s.io
```

**Solution:** Install and configure Prometheus Adapter. See [HPA Integration](../integrations/hpa-integration.md#prometheus-adapter-setup).

#### C. Metric Name Mismatch

HPA is looking for the wrong metric name:

**Solution:** Verify the metric name in HPA matches what WVA exposes:
- Metric name: `inferno_desired_ratio`
- Labels must match: `variant_name`, `namespace`

#### D. HPA Stabilization Window

HPA might be intentionally delaying scaling:

```bash
# Check HPA events
kubectl describe hpa <name> -n <namespace>
```

**Solution:** Adjust stabilization window if needed:

```yaml
behavior:
  scaleDown:
    stabilizationWindowSeconds: 300  # Reduce for faster scale-down
  scaleUp:
    stabilizationWindowSeconds: 0    # 0 for immediate scale-up
```

### 4. Controller Not Starting

**Symptom:** WVA controller pod is CrashLoopBackOff or not ready

**Diagnosis:**

```bash
# Check pod status
kubectl get pods -n workload-variant-autoscaler-system

# Check pod logs
kubectl logs -n workload-variant-autoscaler-system \
  <controller-pod-name>

# Check pod events
kubectl describe pod -n workload-variant-autoscaler-system \
  <controller-pod-name>
```

**Common Causes:**

#### A. Missing CRDs

```bash
kubectl get crd variantautoscalings.llmd.ai
```

**Solution:** Apply CRDs manually:

```bash
kubectl apply -f charts/workload-variant-autoscaler/crds/
```

#### B. RBAC Permissions

Check ServiceAccount and RoleBindings:

```bash
kubectl get serviceaccount -n workload-variant-autoscaler-system
kubectl get clusterrolebinding | grep workload-variant-autoscaler
```

**Solution:** Re-apply RBAC manifests or reinstall via Helm.

#### C. Prometheus Connection Issues

Controller can't reach Prometheus:

**Solution:** Verify Prometheus configuration in ConfigMap:

```bash
kubectl get configmap -n workload-variant-autoscaler-system wva-config -o yaml
```

Check:
- `prometheus.url` is correct
- Network policies allow traffic
- TLS certificates are valid (if using TLS)

#### D. Resource Limits

Controller is OOMKilled:

**Solution:** Increase memory limits:

```bash
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --set resources.limits.memory=512Mi \
  --reuse-values
```

### 5. Excessive Scaling (Thrashing)

**Symptom:** Deployment scales up and down rapidly

**Diagnosis:**

```bash
# Check scaling events
kubectl get events -n <namespace> --sort-by='.lastTimestamp' | grep -i scale

# Check HPA metrics over time
kubectl describe hpa <name> -n <namespace>
```

**Common Causes:**

#### A. Unstable Traffic Patterns

**Solution:** Increase HPA stabilization window:

```yaml
behavior:
  scaleDown:
    stabilizationWindowSeconds: 600  # 10 minutes
    policies:
    - type: Percent
      value: 50
      periodSeconds: 60  # Scale down max 50% per minute
```

#### B. Saturation Threshold Too Low

**Solution:** Increase target utilization in WVA config:

```yaml
saturation:
  target_utilization: 0.85  # Increase from 0.8
```

#### C. Metric Fluctuations

Prometheus metrics are noisy.

**Solution:** Configure metric smoothing in Prometheus queries or adjust WVA reconciliation interval:

```bash
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --set controller.reconcileInterval=120s \
  --reuse-values
```

### 6. Helm Upgrade Issues

**Symptom:** Helm upgrade fails or shows warnings

#### A. CRD Not Updated

Helm doesn't upgrade CRDs automatically.

**Solution:** Manually apply CRDs before upgrading:

```bash
kubectl apply -f charts/workload-variant-autoscaler/crds/
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  [your-values...]
```

#### B. Values Changed

Previous values are lost.

**Solution:** Use `--reuse-values` or `--values` flag:

```bash
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  --reuse-values
```

## Debug Mode

Enable debug logging for detailed troubleshooting:

```bash
# Update deployment with debug flag
kubectl set env deployment/workload-variant-autoscaler-controller-manager \
  -n workload-variant-autoscaler-system \
  LOG_LEVEL=debug

# Or via Helm
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --set controller.logLevel=debug \
  --reuse-values
```

## Collecting Support Information

When opening an issue, provide:

```bash
# 1. WVA version
helm list -n workload-variant-autoscaler-system

# 2. Controller logs (last 100 lines)
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager \
  --tail=100 > wva-logs.txt

# 3. VariantAutoscaling status
kubectl get variantautoscaling -A -o yaml > va-status.yaml

# 4. HPA status
kubectl get hpa -A -o yaml > hpa-status.yaml

# 5. Relevant events
kubectl get events -A --sort-by='.lastTimestamp' | grep -i "wva\|variant\|autoscal" > events.txt

# 6. ConfigMaps
kubectl get configmap -n workload-variant-autoscaler-system -o yaml > configmaps.yaml
```

## Advanced Debugging

### Remote Debugging

See [Debugging Guide](../developer-guide/debugging.md) for:
- Remote debugging with Delve
- SSH tunneling to remote clusters
- IDE integration

### Profiling

Enable pprof endpoint:

```bash
kubectl port-forward -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager 6060:6060

# CPU profile
curl http://localhost:6060/debug/pprof/profile?seconds=30 -o cpu.prof

# Heap profile
curl http://localhost:6060/debug/pprof/heap -o heap.prof

# Analyze with go tool
go tool pprof cpu.prof
```

### Prometheus Query Debugging

Test WVA's Prometheus queries manually:

```bash
# Port forward to Prometheus
kubectl port-forward -n monitoring svc/prometheus 9090:9090

# Test query
curl -G http://localhost:9090/api/v1/query \
  --data-urlencode 'query=vllm:gpu_cache_usage_perc{namespace="default"}' | jq
```

## Getting Help

If you're still stuck:

1. Check the [FAQ](faq.md)
2. Search [existing issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
3. Open a [new issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues/new) with:
   - Detailed problem description
   - Steps to reproduce
   - Support information (see above)
   - Environment details (K8s version, WVA version, etc.)

## Additional Resources

- [Installation Guide](installation.md)
- [Configuration Guide](configuration.md)
- [Metrics Health Monitoring](../metrics-health-monitoring.md)
- [Developer Debugging Guide](../developer-guide/debugging.md)
- [HPA Integration](../integrations/hpa-integration.md)
