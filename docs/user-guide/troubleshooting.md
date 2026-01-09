# Troubleshooting Guide

This guide helps you diagnose and resolve common issues with Workload-Variant-Autoscaler (WVA).

## Quick Diagnostics

### Check WVA Controller Status

```bash
# Check controller pods
kubectl get pods -n workload-variant-autoscaler-system

# Check controller logs
kubectl logs -n workload-variant-autoscaler-system \
  -l control-plane=controller-manager \
  --tail=100 -f

# Check controller metrics endpoint
kubectl port-forward -n workload-variant-autoscaler-system \
  svc/workload-variant-autoscaler-controller-manager-metrics-service 8443:8443
curl -k https://localhost:8443/metrics
```

### Check VariantAutoscaling Resource

```bash
# List all VariantAutoscaling resources
kubectl get variantautoscaling -A

# Get detailed status
kubectl describe variantautoscaling <name> -n <namespace>

# Check conditions
kubectl get variantautoscaling <name> -n <namespace> -o jsonpath='{.status.conditions}'
```

### Enable Debug Logging

```bash
# Edit the controller deployment
kubectl edit deployment -n workload-variant-autoscaler-system \
  workload-variant-autoscaler-controller-manager

# Add or modify the --zap-log-level flag
# args:
#   - --zap-log-level=2  # 0=info, 1=debug, 2=trace
```

## Installation Issues

### Controller Pod Not Starting

**Symptoms:**
- Pod stuck in `Pending`, `CrashLoopBackOff`, or `Error` state

**Diagnosis:**
```bash
# Check pod status
kubectl get pods -n workload-variant-autoscaler-system

# Check pod events
kubectl describe pod -n workload-variant-autoscaler-system <pod-name>

# Check pod logs
kubectl logs -n workload-variant-autoscaler-system <pod-name>
```

**Common Causes & Solutions:**

1. **Image Pull Errors**
   ```bash
   # Verify image exists and credentials are correct
   kubectl get pod <pod-name> -n workload-variant-autoscaler-system -o jsonpath='{.spec.containers[0].image}'
   
   # Check image pull secrets
   kubectl get secrets -n workload-variant-autoscaler-system
   ```

2. **Insufficient Resources**
   ```bash
   # Check node resources
   kubectl top nodes
   kubectl describe nodes
   ```
   
   **Solution**: Adjust resource requests or scale cluster

3. **RBAC Permissions**
   ```bash
   # Verify ServiceAccount exists
   kubectl get serviceaccount -n workload-variant-autoscaler-system
   
   # Verify ClusterRole and ClusterRoleBinding
   kubectl get clusterrole | grep workload-variant-autoscaler
   kubectl get clusterrolebinding | grep workload-variant-autoscaler
   ```
   
   **Solution**: Reapply RBAC manifests
   ```bash
   kubectl apply -f config/rbac/
   ```

### CRD Installation Failures

**Symptoms:**
- Error: `error: unable to recognize "...": no matches for kind "VariantAutoscaling"`

**Solution:**
```bash
# Manually install CRDs
kubectl apply -f charts/workload-variant-autoscaler/crds/

# Verify CRD is installed
kubectl get crd variantautoscalings.llmd.ai

# Check CRD version
kubectl get crd variantautoscalings.llmd.ai -o jsonpath='{.spec.versions[*].name}'
```

### Webhook Configuration Issues

**Symptoms:**
- Error creating VariantAutoscaling resources
- Validation webhook errors

**Diagnosis:**
```bash
# Check ValidatingWebhookConfiguration
kubectl get validatingwebhookconfiguration | grep workload-variant-autoscaler

# Check MutatingWebhookConfiguration  
kubectl get mutatingwebhookconfiguration | grep workload-variant-autoscaler

# Check webhook service
kubectl get svc -n workload-variant-autoscaler-system
```

**Solution:**
```bash
# For development, disable webhooks in kustomization
kubectl delete validatingwebhookconfiguration <name>
kubectl delete mutatingwebhookconfiguration <name>
```

## Configuration Issues

### Prometheus Connection Failures

**Symptoms:**
- Logs show: `failed to query Prometheus`
- Metrics not being collected

**Diagnosis:**
```bash
# Check Prometheus URL in controller config
kubectl get configmap -n workload-variant-autoscaler-system wva-configmap -o yaml

# Test connectivity from controller pod
kubectl exec -n workload-variant-autoscaler-system <pod-name> -- \
  curl -k https://prometheus-service:9091/api/v1/query?query=up
```

**Common Causes & Solutions:**

1. **Wrong Prometheus URL**
   ```bash
   # Update ConfigMap
   kubectl edit configmap -n workload-variant-autoscaler-system wva-configmap
   ```

2. **TLS Certificate Issues** (OpenShift)
   ```bash
   # Check CA certificate ConfigMap
   kubectl get configmap -n workload-variant-autoscaler-system \
     workload-variant-autoscaler-prometheus-ca
   
   # Verify certificate is valid
   kubectl get configmap -n workload-variant-autoscaler-system \
     workload-variant-autoscaler-prometheus-ca -o jsonpath='{.data.ca\.crt}' | \
     openssl x509 -text -noout
   ```
   
   **Solution**: Update CA certificate
   ```bash
   helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
     --set-file prometheus.caCert=/path/to/correct/ca.crt
   ```

3. **Network Policies**
   ```bash
   # Check network policies
   kubectl get networkpolicy -A
   
   # Test from controller pod
   kubectl exec -n workload-variant-autoscaler-system <pod-name> -- \
     nc -zv prometheus-service 9091
   ```
   
   **Solution**: Add network policy to allow traffic

### Missing or Stale Metrics

**Symptoms:**
- VariantAutoscaling status shows zero values
- `currentAlloc` not populated

**Diagnosis:**
```bash
# Check if Prometheus is scraping inference servers
kubectl port-forward svc/prometheus-service 9090:9090
# Visit http://localhost:9090 and query: vllm_*

# Check WVA controller can query metrics
kubectl logs -n workload-variant-autoscaler-system \
  -l control-plane=controller-manager | grep "metrics"
```

**Solutions:**

1. **Inference server not exposing metrics**
   - Verify vLLM metrics endpoint: `curl http://<pod-ip>:8000/metrics`
   - Ensure `--disable-log-stats` is NOT set

2. **Prometheus not scraping**
   ```bash
   # Check ServiceMonitor (Prometheus Operator)
   kubectl get servicemonitor -A
   
   # Or check Prometheus config
   kubectl get configmap prometheus-config -o yaml
   ```

3. **Metrics lag**
   - Increase Prometheus scrape interval
   - Check Prometheus query performance

### Invalid VariantAutoscaling Configuration

**Symptoms:**
- Resource creation fails with validation errors
- Status shows `ConfigurationError` condition

**Common Validation Errors:**

1. **Missing required fields**
   ```yaml
   # Ensure scaleTargetRef and modelID are set
   spec:
     scaleTargetRef:
       apiVersion: apps/v1
       kind: Deployment
       name: my-inference-server
     modelID: "meta/llama-3.1-8b"
   ```

2. **Invalid cost format**
   ```yaml
   # Cost must be numeric string
   variantCost: "10.5"  # ✅ Valid
   variantCost: "$10"   # ❌ Invalid
   ```

## Scaling Issues

### Replicas Not Scaling

**Symptoms:**
- Desired replicas in VariantAutoscaling status doesn't match actual replicas
- HPA not responding to WVA metrics

**Diagnosis:**
```bash
# Check VariantAutoscaling status
kubectl get variantautoscaling <name> -n <namespace> -o yaml

# Check HPA status
kubectl get hpa -n <namespace>
kubectl describe hpa <hpa-name> -n <namespace>

# Check HPA can access WVA metrics
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1/namespaces/<namespace>/metrics/wva_desired_replicas"
```

**Common Causes & Solutions:**

1. **No HPA Configured**
   ```bash
   # Create HPA to read WVA metrics
   kubectl apply -f - <<EOF
   apiVersion: autoscaling/v2
   kind: HorizontalPodAutoscaler
   metadata:
     name: my-inference-hpa
     namespace: default
   spec:
     scaleTargetRef:
       apiVersion: apps/v1
       kind: Deployment
       name: my-inference-server
     minReplicas: 1
     maxReplicas: 10
     metrics:
     - type: Pods
       pods:
         metric:
           name: wva_desired_replicas
         target:
           type: AverageValue
           averageValue: "1"
   EOF
   ```

2. **Metrics Adapter Not Installed**
   ```bash
   # Check if prometheus-adapter is running
   kubectl get pods -n monitoring | grep prometheus-adapter
   
   # Install prometheus-adapter
   helm install prometheus-adapter prometheus-community/prometheus-adapter \
     -f config/samples/prometheus-adapter-values.yaml
   ```

3. **HPA Stabilization Preventing Scale**
   ```bash
   # Check HPA events
   kubectl describe hpa <hpa-name> -n <namespace>
   
   # Adjust stabilization window if needed
   kubectl patch hpa <hpa-name> -n <namespace> --type=json -p='[
     {"op": "replace", "path": "/spec/behavior/scaleDown/stabilizationWindowSeconds", "value": 60}
   ]'
   ```

### Frequent Scale Oscillations

**Symptoms:**
- Deployment scales up and down repeatedly
- Thrashing between replica counts

**Diagnosis:**
```bash
# Check HPA behavior
kubectl get hpa <hpa-name> -n <namespace> -o yaml | grep -A 20 behavior

# Check WVA optimization history
kubectl logs -n workload-variant-autoscaler-system \
  -l control-plane=controller-manager | grep "optimization"
```

**Solutions:**

1. **Increase Stabilization Window**
   ```yaml
   apiVersion: autoscaling/v2
   kind: HorizontalPodAutoscaler
   spec:
     behavior:
       scaleDown:
         stabilizationWindowSeconds: 300  # 5 minutes
       scaleUp:
         stabilizationWindowSeconds: 120  # 2 minutes
   ```

2. **Adjust Saturation Thresholds**
   ```bash
   kubectl edit configmap -n workload-variant-autoscaler-system saturation-scaling-config
   # Increase thresholds to reduce sensitivity
   ```

3. **Enable Scale Rate Limiting**
   ```yaml
   behavior:
     scaleDown:
       policies:
       - type: Percent
         value: 50
         periodSeconds: 60
     scaleUp:
       policies:
       - type: Percent
         value: 100
         periodSeconds: 60
   ```

### Scale to Zero Not Working

**Symptoms:**
- Deployment doesn't scale to zero when idle

**Note**: WVA doesn't natively support scale-to-zero. Use KEDA for this feature:

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: my-inference-scaler
spec:
  scaleTargetRef:
    name: my-inference-server
  minReplicaCount: 0
  maxReplicaCount: 10
  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus:9090
      metricName: wva_desired_replicas
      threshold: "1"
      query: |
        wva_desired_replicas{deployment="my-inference-server"}
```

See [KEDA Integration](../integrations/keda-integration.md) for details.

## Performance Issues

### High Controller Memory Usage

**Symptoms:**
- Controller pod OOMKilled
- Memory usage growing over time

**Diagnosis:**
```bash
# Check memory usage
kubectl top pod -n workload-variant-autoscaler-system

# Check metrics cache size
kubectl logs -n workload-variant-autoscaler-system \
  -l control-plane=controller-manager | grep "cache"
```

**Solutions:**

1. **Adjust Cache Settings**
   ```bash
   kubectl edit configmap -n workload-variant-autoscaler-system wva-configmap
   # Reduce cache TTL or size
   ```

2. **Increase Memory Limits**
   ```bash
   kubectl edit deployment -n workload-variant-autoscaler-system \
     workload-variant-autoscaler-controller-manager
   # Update resources.limits.memory
   ```

### Slow Reconciliation

**Symptoms:**
- Long delays between optimization runs
- Status updates take minutes

**Diagnosis:**
```bash
# Check reconciliation metrics
kubectl port-forward -n workload-variant-autoscaler-system \
  svc/workload-variant-autoscaler-controller-manager-metrics-service 8443:8443
curl -k https://localhost:8443/metrics | grep reconcile

# Check controller logs for timing
kubectl logs -n workload-variant-autoscaler-system \
  -l control-plane=controller-manager | grep "reconcile"
```

**Solutions:**

1. **Reduce Prometheus Query Complexity**
   - Use shorter time ranges
   - Optimize metric queries
   - Enable Prometheus query caching

2. **Scale Controller Replicas**
   ```bash
   kubectl scale deployment -n workload-variant-autoscaler-system \
     workload-variant-autoscaler-controller-manager --replicas=2
   ```

3. **Use Multiple Controllers with Isolation**
   - See [Multi-Controller Isolation](multi-controller-isolation.md)

## Platform-Specific Issues

### OpenShift Issues

#### Security Context Constraints

**Symptoms:**
- Pods fail with `unable to validate against any security context constraint`

**Solution:**
```bash
# Apply SCC
oc adm policy add-scc-to-user anyuid -z workload-variant-autoscaler \
  -n workload-variant-autoscaler-system
```

#### Route Configuration

**Symptoms:**
- Cannot access metrics externally

**Solution:**
```bash
# Create Route for metrics service
oc create route passthrough wva-metrics \
  --service=workload-variant-autoscaler-controller-manager-metrics-service \
  -n workload-variant-autoscaler-system
```

See [OpenShift Deployment Guide](../../deploy/openshift/README.md) for details.

### Kind Emulator Issues

**Symptoms:**
- GPU emulation not working
- vLLM emulator fails

**Diagnosis:**
```bash
# Check emulator setup
kubectl get pods -n llm-d-system
kubectl logs -n llm-d-system <vllm-emulator-pod>
```

**Solutions:**
- See [Kind Emulator Troubleshooting](../../deploy/kind-emulator/README.md#troubleshooting)

## Debugging Tools

### Remote Debugging

For deep debugging, use SSH tunneling to connect a debugger:

```bash
# Forward debugger port from controller pod
kubectl port-forward -n workload-variant-autoscaler-system \
  pod/<controller-pod> 2345:2345
```

See [Debugging Guide](../developer-guide/debugging.md) for detailed instructions.

### Collecting Diagnostic Information

```bash
#!/bin/bash
# Save this as collect-diagnostics.sh

NAMESPACE="workload-variant-autoscaler-system"
OUTPUT_DIR="wva-diagnostics-$(date +%Y%m%d-%H%M%S)"

mkdir -p "$OUTPUT_DIR"

echo "Collecting WVA diagnostics..."

# Controller info
kubectl get pods -n "$NAMESPACE" -o yaml > "$OUTPUT_DIR/pods.yaml"
kubectl logs -n "$NAMESPACE" -l control-plane=controller-manager \
  --tail=1000 > "$OUTPUT_DIR/controller-logs.txt"

# Resources
kubectl get variantautoscaling -A -o yaml > "$OUTPUT_DIR/variantautoscalings.yaml"
kubectl get hpa -A -o yaml > "$OUTPUT_DIR/hpas.yaml"

# Config
kubectl get configmap -n "$NAMESPACE" -o yaml > "$OUTPUT_DIR/configmaps.yaml"

# Metrics
kubectl port-forward -n "$NAMESPACE" \
  svc/workload-variant-autoscaler-controller-manager-metrics-service 8443:8443 &
sleep 2
curl -k https://localhost:8443/metrics > "$OUTPUT_DIR/metrics.txt"
pkill -f "port-forward.*8443"

tar czf "$OUTPUT_DIR.tar.gz" "$OUTPUT_DIR"
echo "Diagnostics saved to $OUTPUT_DIR.tar.gz"
```

## Getting Help

If you can't resolve your issue:

1. **Check Known Issues**: [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
2. **Search Discussions**: [GitHub Discussions](https://github.com/llm-d-incubation/workload-variant-autoscaler/discussions)
3. **Ask for Help**: 
   - Open a new issue with diagnostic information
   - Join community meetings
4. **Review Documentation**:
   - [FAQ](faq.md)
   - [Developer Guide](../developer-guide/debugging.md)

## Additional Resources

- [Installation Guide](installation.md)
- [Configuration Guide](configuration.md)
- [HPA Integration](../integrations/hpa-integration.md)
- [Prometheus Integration](../integrations/prometheus.md)
- [Debugging Guide](../developer-guide/debugging.md)

---

**Note**: This troubleshooting guide is continuously updated. Please [contribute](../../CONTRIBUTING.md) solutions for issues you encounter!
