# Troubleshooting Guide

This guide helps diagnose and resolve common issues with Workload-Variant-Autoscaler (WVA).

## Quick Diagnostic Commands

Run these commands to gather essential diagnostic information:

```bash
# Check controller status
kubectl get pods -n workload-variant-autoscaler-system
kubectl logs -n workload-variant-autoscaler-system -l control-plane=controller-manager --tail=100

# Check VariantAutoscaling resources
kubectl get variantautoscaling --all-namespaces
kubectl describe variantautoscaling <name> -n <namespace>

# Check HPA status (if using HPA integration)
kubectl get hpa --all-namespaces
kubectl describe hpa <name> -n <namespace>

# Check metrics availability
kubectl get --raw /apis/external.metrics.k8s.io/v1beta1 | jq
```

## Installation Issues

### Controller Pod Not Starting

**Symptoms:**
- Controller pod stuck in `CrashLoopBackOff` or `Pending`
- Error messages in pod events

**Diagnostic Steps:**

```bash
# Check pod status
kubectl get pods -n workload-variant-autoscaler-system

# View pod events
kubectl describe pod -n workload-variant-autoscaler-system -l control-plane=controller-manager

# Check logs
kubectl logs -n workload-variant-autoscaler-system -l control-plane=controller-manager
```

**Common Causes & Solutions:**

1. **Missing CRDs**

   ```bash
   # Verify CRDs are installed
   kubectl get crd variantautoscalings.llmd.ai
   
   # If missing, apply CRDs
   kubectl apply -f charts/workload-variant-autoscaler/crds/
   ```

2. **RBAC Permission Issues**

   ```bash
   # Check ServiceAccount
   kubectl get serviceaccount -n workload-variant-autoscaler-system
   
   # Verify ClusterRole and ClusterRoleBinding
   kubectl get clusterrole | grep variantautoscaling
   kubectl get clusterrolebinding | grep variantautoscaling
   ```

   **Solution**: Reapply RBAC configuration:
   ```bash
   helm upgrade --install workload-variant-autoscaler ./charts/workload-variant-autoscaler \
     --namespace workload-variant-autoscaler-system
   ```

3. **Prometheus Connection Failure**

   **Symptoms**: Logs show connection errors to Prometheus
   
   ```bash
   # Check Prometheus URL configuration
   kubectl get configmap -n workload-variant-autoscaler-system \
     wva-controller-manager-config -o yaml | grep prometheusURL
   ```

   **Solution**: Update Prometheus URL in ConfigMap or Helm values:
   ```yaml
   prometheus:
     url: "http://prometheus-server.monitoring.svc:9090"
   ```

4. **TLS Certificate Issues**

   **Symptoms**: `x509: certificate signed by unknown authority`
   
   **Solution**: Provide Prometheus CA certificate:
   ```bash
   helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
     --set-file prometheus.caCert=/path/to/prometheus-ca.crt
   ```

### Helm Installation Fails

**Symptoms:**
- `helm install` or `helm upgrade` returns errors
- Resources not created

**Diagnostic Steps:**

```bash
# Check Helm release status
helm list -n workload-variant-autoscaler-system

# Get detailed error information
helm upgrade --install workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system --dry-run --debug
```

**Common Causes & Solutions:**

1. **Namespace doesn't exist**
   ```bash
   kubectl create namespace workload-variant-autoscaler-system
   ```

2. **Invalid values.yaml**
   ```bash
   # Validate values file
   helm lint ./charts/workload-variant-autoscaler -f values.yaml
   ```

3. **CRD conflicts from previous installation**
   ```bash
   # Remove old CRDs (WARNING: deletes all VariantAutoscaling resources)
   kubectl delete crd variantautoscalings.llmd.ai
   
   # Reinstall
   kubectl apply -f charts/workload-variant-autoscaler/crds/
   ```

## VariantAutoscaling Issues

### Status Shows "DeploymentNotFound"

**Symptoms:**
- VariantAutoscaling status: `DeploymentNotFound`
- No scaling occurs

**Diagnostic Steps:**

```bash
# Check VariantAutoscaling details
kubectl get variantautoscaling <name> -n <namespace> -o yaml

# Check if deployment exists
kubectl get deployment <deployment-name> -n <namespace>

# Check scaleTargetRef
kubectl get variantautoscaling <name> -n <namespace> -o jsonpath='{.spec.scaleTargetRef}'
```

**Solutions:**

1. **Deployment name mismatch**
   
   Verify the `scaleTargetRef` matches your deployment:
   ```yaml
   spec:
     scaleTargetRef:
       apiVersion: apps/v1
       kind: Deployment
       name: llama-8b-deployment  # Must match actual deployment name
   ```

2. **Namespace mismatch**
   
   VariantAutoscaling and target Deployment must be in the same namespace.

3. **Deployment not created yet**
   
   WVA handles this gracefully - status will update automatically when deployment is created.

### Status Shows "MetricsUnavailable"

**Symptoms:**
- VariantAutoscaling status: `MetricsUnavailable`
- No scaling recommendations

**Diagnostic Steps:**

```bash
# Check if vLLM pods are running
kubectl get pods -n <namespace> -l app=<your-vllm-app>

# Check if metrics endpoint is accessible
kubectl port-forward -n <namespace> <vllm-pod> 8000:8000
curl http://localhost:8000/metrics | grep vllm

# Check if Prometheus is scraping metrics
# Access Prometheus UI and check targets
kubectl port-forward -n monitoring svc/prometheus-server 9090:9090
# Open http://localhost:9090/targets
```

**Solutions:**

1. **ServiceMonitor not configured**
   
   ```bash
   # Check if ServiceMonitor exists
   kubectl get servicemonitor -n <namespace>
   
   # Verify ServiceMonitor selects your service
   kubectl get servicemonitor <name> -n <namespace> -o yaml
   ```

2. **Prometheus not installed or not accessible**
   
   ```bash
   # Check Prometheus installation
   kubectl get pods -n monitoring -l app=prometheus
   
   # Verify WVA can reach Prometheus
   kubectl exec -n workload-variant-autoscaler-system \
     -it <controller-pod> -- curl http://prometheus-server.monitoring.svc:9090/-/healthy
   ```

3. **vLLM not exposing metrics**
   
   Ensure vLLM is started with metrics enabled (default in v0.6.0+):
   ```bash
   # Check vLLM startup logs
   kubectl logs -n <namespace> <vllm-pod> | grep metrics
   ```

### No Scaling Activity

**Symptoms:**
- VariantAutoscaling status shows valid data
- Deployment replica count doesn't change

**Diagnostic Steps:**

```bash
# Check if HPA/KEDA is configured
kubectl get hpa -n <namespace>
kubectl get scaledobject -n <namespace>

# Check HPA status
kubectl describe hpa <name> -n <namespace>

# Verify metrics are available to HPA
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/variantautoscaling_desired_replicas?labelSelector=variantautoscaling_name=<va-name>" | jq
```

**Solutions:**

1. **HPA/KEDA not configured**
   
   WVA emits metrics but doesn't scale directly. You need HPA or KEDA:
   ```yaml
   apiVersion: autoscaling/v2
   kind: HorizontalPodAutoscaler
   metadata:
     name: llama-8b-hpa
   spec:
     scaleTargetRef:
       apiVersion: apps/v1
       kind: Deployment
       name: llama-8b-deployment
     minReplicas: 1
     maxReplicas: 10
     metrics:
     - type: External
       external:
         metric:
           name: variantautoscaling_desired_replicas
           selector:
             matchLabels:
               variantautoscaling_name: llama-8b-autoscaler
         target:
           type: Value
           value: "1"
   ```

2. **Metrics server not configured**
   
   ```bash
   # For external metrics, ensure custom metrics API is available
   kubectl get apiservice | grep external.metrics
   
   # Check if prometheus-adapter is deployed
   kubectl get pods -n monitoring -l app=prometheus-adapter
   ```

3. **HPA stabilization preventing scaling**
   
   HPA may be in stabilization window:
   ```bash
   kubectl describe hpa <name> -n <namespace> | grep -A5 "Conditions"
   ```

## Metrics Issues

### Metrics Not Appearing in Prometheus

**Symptoms:**
- Prometheus targets show vLLM endpoints as down
- WVA controller logs show metric query failures

**Diagnostic Steps:**

```bash
# Check Prometheus targets
kubectl port-forward -n monitoring svc/prometheus-server 9090:9090
# Open http://localhost:9090/targets and search for vllm

# Test direct metrics access
kubectl get svc -n <namespace> | grep vllm
kubectl port-forward -n <namespace> svc/<vllm-service> 8000:8000
curl http://localhost:8000/metrics
```

**Solutions:**

1. **ServiceMonitor selector mismatch**
   
   ```bash
   # Check ServiceMonitor selector
   kubectl get servicemonitor <name> -n <namespace> -o yaml
   
   # Compare with Service labels
   kubectl get svc <vllm-service> -n <namespace> --show-labels
   ```
   
   Ensure ServiceMonitor selector matches Service labels.

2. **Prometheus RBAC issues**
   
   Prometheus needs permission to scrape metrics:
   ```bash
   # Check Prometheus ServiceAccount
   kubectl get sa -n monitoring prometheus-server
   
   # Verify ClusterRole allows scraping
   kubectl get clusterrole prometheus-server -o yaml
   ```

3. **Network policy blocking access**
   
   ```bash
   # Check for network policies
   kubectl get networkpolicy -n <namespace>
   
   # Ensure Prometheus can access vLLM pods
   kubectl describe networkpolicy <name> -n <namespace>
   ```

### WVA Metrics Not Available to HPA

**Symptoms:**
- HPA shows "failed to get external metric"
- `kubectl get --raw /apis/external.metrics.k8s.io/v1beta1` returns empty or error

**Diagnostic Steps:**

```bash
# Check custom metrics API
kubectl get apiservice v1beta1.external.metrics.k8s.io

# Check prometheus-adapter
kubectl get pods -n monitoring -l app=prometheus-adapter
kubectl logs -n monitoring -l app=prometheus-adapter --tail=50

# Test direct metric access
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/variantautoscaling_desired_replicas" | jq
```

**Solutions:**

1. **Prometheus-adapter not configured**
   
   Install prometheus-adapter:
   ```bash
   helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
   helm install prometheus-adapter prometheus-community/prometheus-adapter \
     -n monitoring -f config/samples/prometheus-adapter-values.yaml
   ```

2. **Metric rules not configured**
   
   Ensure prometheus-adapter rules include WVA metrics:
   ```yaml
   rules:
   - seriesQuery: 'variantautoscaling_desired_replicas'
     resources:
       overrides:
         namespace: {resource: "namespace"}
         variantautoscaling_name: {resource: "variantautoscaling"}
     name:
       matches: "^(.*)$"
       as: "${1}"
     metricsQuery: 'max(<<.Series>>{<<.LabelMatchers>>}) by (variantautoscaling_name)'
   ```

3. **APIService not ready**
   
   ```bash
   kubectl get apiservice v1beta1.external.metrics.k8s.io -o yaml
   
   # If unavailable, check service endpoint
   kubectl get endpoints -n monitoring prometheus-adapter
   ```

## Performance Issues

### High CPU/Memory Usage

**Symptoms:**
- Controller pod using excessive resources
- OOMKilled or CPU throttling

**Diagnostic Steps:**

```bash
# Check resource usage
kubectl top pod -n workload-variant-autoscaler-system

# Check resource limits
kubectl get pod -n workload-variant-autoscaler-system -l control-plane=controller-manager -o yaml | grep -A5 resources

# Check number of managed VariantAutoscalings
kubectl get variantautoscaling --all-namespaces | wc -l
```

**Solutions:**

1. **Increase resource limits**
   
   ```yaml
   # In Helm values.yaml
   controller:
     resources:
       limits:
         cpu: 1000m
         memory: 1Gi
       requests:
         cpu: 200m
         memory: 256Mi
   ```

2. **Reduce reconciliation frequency**
   
   ```yaml
   controller:
     reconciliationInterval: 120s  # Increase from default 60s
   ```

3. **Enable caching**
   
   Verify caching is enabled in ConfigMap (default):
   ```yaml
   metricsCollection:
     cacheTTL: 30s
   ```

### Slow Scaling Response

**Symptoms:**
- Long delay between load changes and scaling
- Desired replicas not updating promptly

**Diagnostic Steps:**

```bash
# Check reconciliation timing
kubectl logs -n workload-variant-autoscaler-system -l control-plane=controller-manager | grep "Reconciliation completed"

# Check Prometheus query performance
kubectl port-forward -n monitoring svc/prometheus-server 9090:9090
# Open http://localhost:9090/graph and test WVA queries

# Check HPA stabilization window
kubectl get hpa <name> -n <namespace> -o yaml | grep stabilizationWindowSeconds
```

**Solutions:**

1. **Reduce reconciliation interval**
   ```yaml
   controller:
     reconciliationInterval: 30s
   ```

2. **Reduce HPA stabilization window**
   ```yaml
   behavior:
     scaleUp:
       stabilizationWindowSeconds: 30  # Reduce from default
   ```

3. **Optimize Prometheus queries**
   - Ensure Prometheus has adequate resources
   - Check for slow queries in Prometheus UI
   - Consider using recording rules for complex queries

## Configuration Issues

### ConfigMap Changes Not Taking Effect

**Symptoms:**
- Updated saturation thresholds not applied
- Controller still using old configuration

**Diagnostic Steps:**

```bash
# Check ConfigMap
kubectl get configmap capacity-scaling-config -n workload-variant-autoscaler-system -o yaml

# Check controller logs for reload
kubectl logs -n workload-variant-autoscaler-system -l control-plane=controller-manager | grep "ConfigMap updated"
```

**Solutions:**

1. **ConfigMap not mounted correctly**
   
   ```bash
   # Verify ConfigMap mount
   kubectl describe pod -n workload-variant-autoscaler-system -l control-plane=controller-manager | grep -A5 Mounts
   ```

2. **Invalid YAML syntax**
   
   ```bash
   # Validate ConfigMap YAML
   kubectl get configmap capacity-scaling-config -n workload-variant-autoscaler-system -o yaml | yq eval
   ```

3. **Restart controller to force reload**
   
   ```bash
   kubectl rollout restart deployment -n workload-variant-autoscaler-system \
     workload-variant-autoscaler-controller-manager
   ```

## Integration Issues

### HPA Integration Problems

See the detailed [HPA Integration Troubleshooting](../integrations/hpa-integration.md#troubleshooting) section.

### KEDA Integration Problems

See the detailed [KEDA Integration Troubleshooting](../integrations/keda-integration.md#troubleshooting) section.

### OpenShift-Specific Issues

**Problem: User-Workload Monitoring not enabled**

```bash
# Enable user-workload monitoring
kubectl edit configmap cluster-monitoring-config -n openshift-monitoring

# Add:
data:
  config.yaml: |
    enableUserWorkload: true
```

See [OpenShift Deployment Guide](../../deploy/openshift/README.md) for details.

## Debugging Tips

### Enable Debug Logging

```bash
# Via Helm
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --set controller.logLevel=debug \
  --namespace workload-variant-autoscaler-system

# Or edit deployment
kubectl edit deployment -n workload-variant-autoscaler-system \
  workload-variant-autoscaler-controller-manager
# Change --zap-log-level=info to --zap-log-level=debug
```

### Inspect Controller Events

```bash
# Watch controller events
kubectl get events -n workload-variant-autoscaler-system --watch

# Filter for VariantAutoscaling events
kubectl get events --all-namespaces --field-selector involvedObject.kind=VariantAutoscaling
```

### Test Prometheus Queries Manually

```bash
# Port-forward to Prometheus
kubectl port-forward -n monitoring svc/prometheus-server 9090:9090

# Test WVA queries in Prometheus UI
# Example query: max_over_time(vllm:gpu_cache_usage_perc[1m])
```

### Remote Debugging

For advanced debugging, see the [Debugging Guide](../developer-guide/debugging.md) which covers:
- Remote debugging with Delve
- SSH tunnel setup
- Attaching to running controllers

## Getting Additional Help

If you're still experiencing issues:

1. **Check existing GitHub issues**: [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
2. **Review the FAQ**: [FAQ](faq.md)
3. **Enable debug logging and gather diagnostics**:
   ```bash
   # Collect diagnostic bundle
   kubectl logs -n workload-variant-autoscaler-system -l control-plane=controller-manager --tail=500 > controller.log
   kubectl get variantautoscaling --all-namespaces -o yaml > variantautoscalings.yaml
   kubectl get hpa --all-namespaces -o yaml > hpas.yaml
   kubectl get events --all-namespaces > events.log
   ```
4. **Open a new issue**: Include diagnostic information, steps to reproduce, and expected vs actual behavior

## Common Error Messages

### "failed to get metrics from Prometheus"

**Cause**: Cannot connect to Prometheus or query failed

**Solution**: Check Prometheus URL configuration and network connectivity

### "deployment not found"

**Cause**: ScaleTargetRef doesn't match any deployment

**Solution**: Verify deployment name and namespace

### "failed to parse threshold value"

**Cause**: Invalid threshold configuration in ConfigMap

**Solution**: Ensure thresholds are valid numbers (0.0-1.0 for KV, integers for queue)

### "x509: certificate signed by unknown authority"

**Cause**: TLS verification failure connecting to Prometheus

**Solution**: Provide CA certificate via `--set-file prometheus.caCert=`

### "admission webhook denied the request"

**Cause**: CRD validation failure

**Solution**: Check VariantAutoscaling spec meets validation requirements

---

**Need more help?** Visit the [FAQ](faq.md) or open a [GitHub Issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues).
