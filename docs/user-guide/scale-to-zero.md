# Scale-to-Zero Configuration Guide

> **Status:** Infrastructure Phase (v0.5.0)
> 
> The scale-to-zero configuration infrastructure is available in v0.5.0. The complete scale-from-zero engine implementation is planned for a future release.

## Overview

Scale-to-zero enables WVA to scale deployments down to zero replicas when there is no traffic, providing complete resource optimization. When load returns, the deployment automatically scales back up.

This feature is particularly valuable for:
- **Development and testing environments** where models are used intermittently
- **Cost optimization** by eliminating idle resource consumption
- **Multi-tenant environments** with sporadic usage patterns
- **Non-production workloads** with predictable idle periods

## How It Works

Scale-to-zero operates based on request activity monitoring:

1. **Active State**: Model serves requests normally with WVA managing replica count
2. **Monitoring Period**: After the last request, WVA starts a retention period countdown
3. **Idle Detection**: If no new requests arrive during the retention period, the model is eligible for scale-to-zero
4. **Scale Down**: The deployment scales to zero replicas, freeing all GPU resources
5. **Scale Up**: When new requests arrive, the deployment scales back up (typically 2-7 minutes for model loading)

### Request Tracking

WVA monitors the `vllm:request_success_total` metric from Prometheus to determine request activity:

```promql
sum(increase(vllm:request_success_total{namespace="<namespace>",model_name="<modelID>"}[<retentionPeriod>]))
```

If this query returns 0 (no requests in the retention period), the model is eligible for scale-to-zero.

## Configuration

Scale-to-zero is configured through a dedicated ConfigMap that supports both global defaults and per-model overrides.

### Global Configuration

Enable scale-to-zero globally for all models:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  default: |
    enable_scale_to_zero: true
    retention_period: "15m"
```

### Per-Model Configuration

Override settings for specific models:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  # Global defaults
  default: |
    enable_scale_to_zero: true
    retention_period: "15m"
  
  # Disable scale-to-zero for production model
  llama-70b-production: |
    model_id: meta/llama-3.1-70b
    namespace: production
    enable_scale_to_zero: false
  
  # Custom retention for development model
  llama-8b-dev: |
    model_id: meta/llama-3.1-8b
    namespace: development
    retention_period: "5m"
```

### Configuration Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `model_id` | string | Yes (overrides) | - | Model identifier (e.g., "meta/llama-3.1-8b") |
| `namespace` | string | No | - | Namespace filter for this configuration |
| `enable_scale_to_zero` | boolean | No | false | Enable/disable scale-to-zero for this model |
| `retention_period` | string | No | "10m" | Time to wait after last request before scaling to zero |

### Retention Period Format

The retention period uses Prometheus duration format:
- `"30s"` - 30 seconds
- `"5m"` - 5 minutes
- `"1h"` - 1 hour
- `"90m"` - 90 minutes

**Recommendation:** Use retention periods of at least 10 minutes to avoid frequent scale-down/scale-up cycles.

## Configuration Priority

Scale-to-zero configuration follows a priority hierarchy (highest to lowest):

1. **Per-model ConfigMap entry** (specific model_id + optional namespace)
2. **Global defaults in ConfigMap** (key: "default")
3. **Environment variable** (`WVA_SCALE_TO_ZERO`)
4. **System default** (disabled, 10-minute retention)

### Partial Overrides

You can override individual fields while inheriting others from global defaults:

```yaml
data:
  # Global: enabled with 15-minute retention
  default: |
    enable_scale_to_zero: true
    retention_period: "15m"
  
  # Override only retention period, inherit enable_scale_to_zero from default
  llama-8b-fast: |
    model_id: meta/llama-3.1-8b
    retention_period: "5m"  # Faster scale-down
    # enable_scale_to_zero: true (inherited from default)
```

## Environment Variable Configuration

For simpler deployments or development, use the `WVA_SCALE_TO_ZERO` environment variable:

### Helm Chart

```yaml
# values.yaml
wva:
  scaleToZero: true  # Enables WVA_SCALE_TO_ZERO=true
```

This generates:

```yaml
env:
  - name: WVA_SCALE_TO_ZERO
    value: "true"
```

### Direct Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: wva-controller
spec:
  template:
    spec:
      containers:
      - name: manager
        env:
        - name: WVA_SCALE_TO_ZERO
          value: "true"
```

**Limitation:** Environment variable provides only a global on/off switch with default retention (10 minutes). Use ConfigMap for per-model control.

## Integration with HPA

Scale-to-zero requires HPA support for `minReplicas: 0`, which is an alpha feature in Kubernetes.

### Enable HPA Scale-to-Zero (Kubernetes 1.32+)

For Kind clusters or self-managed Kubernetes:

```bash
# 1. Access the control plane
docker exec -it <control-plane-container> bash

# 2. Enable feature gate on API server
sed -i 's#- kube-apiserver#- kube-apiserver\n    - --feature-gates=HPAScaleToZero=true#g' \
  /etc/kubernetes/manifests/kube-apiserver.yaml

# 3. Enable feature gate on controller manager
sed -i 's#- kube-controller-manager#- kube-controller-manager\n    - --feature-gates=HPAScaleToZero=true#g' \
  /etc/kubernetes/manifests/kube-controller-manager.yaml

# 4. Wait for components to restart
kubectl wait --for=condition=Ready pod -l component=kube-apiserver -n kube-system --timeout=120s
kubectl wait --for=condition=Ready pod -l component=kube-controller-manager -n kube-system --timeout=120s
```

### Configure HPA

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: llama-8b-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: llama-8b
  minReplicas: 0  # Requires HPAScaleToZero feature gate
  maxReplicas: 10
  metrics:
  - type: External
    external:
      metric:
        name: inferno_desired_replicas
        selector:
          matchLabels:
            variant_name: llama-8b
      target:
        type: AverageValue
        averageValue: "1"
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 300  # Longer stabilization for scale-to-zero
```

### OpenShift

OpenShift 4.18+ supports HPA scale-to-zero without additional configuration.

## Integration with KEDA

KEDA provides native scale-to-zero support without requiring feature gates.

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: llama-8b-scaledobject
spec:
  scaleTargetRef:
    name: llama-8b
  minReplicaCount: 0  # Native KEDA support
  maxReplicaCount: 10
  triggers:
  - type: prometheus
    metadata:
      serverAddress: https://prometheus-k8s.monitoring.svc:9091
      metricName: inferno_desired_replicas
      query: |
        avg(inferno_desired_replicas{variant_name="llama-8b"})
      threshold: "1"
```

See [KEDA Integration Guide](../integrations/keda-integration.md) for details.

## Operational Considerations

### Cold Start Latency

When a deployment scales from zero, expect:
- **Model Loading Time**: 2-7 minutes depending on model size
- **First Request Latency**: Adds to normal inference latency
- **Subsequent Requests**: Normal performance once loaded

**Mitigation Strategies:**
- Use longer retention periods (15-30 minutes) for frequently accessed models
- Implement request queuing at the application layer
- Consider keeping critical models at `minReplicas: 1`

### Monitoring Scale-to-Zero

Track scale-to-zero behavior with these queries:

```promql
# Models currently at zero replicas
count(kube_deployment_spec_replicas{deployment=~".*llama.*"} == 0)

# Time since last request (requires custom recording rule)
time() - max(vllm:request_success_total) by (model_name)

# Scale-up events from zero
rate(kube_deployment_spec_replicas{deployment=~".*llama.*"}[5m]) > 0
```

### Cost-Benefit Analysis

**When to Use:**
- ✅ Development/staging environments
- ✅ Models with predictable idle periods
- ✅ Multi-tenant platforms with sporadic usage
- ✅ Batch processing workloads

**When to Avoid:**
- ❌ Production services requiring low latency
- ❌ High-frequency, unpredictable traffic patterns
- ❌ SLA requirements incompatible with cold starts
- ❌ Models with very long loading times (>10 minutes)

## Troubleshooting

### Scale-to-Zero Not Activating

**Check Configuration:**
```bash
# Verify ConfigMap exists
kubectl get configmap model-scale-to-zero-config -n workload-variant-autoscaler-system

# Check configuration
kubectl get configmap model-scale-to-zero-config -n workload-variant-autoscaler-system -o yaml

# Verify environment variable
kubectl get deployment wva-controller -n workload-variant-autoscaler-system -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="WVA_SCALE_TO_ZERO")].value}'
```

**Check HPA Feature Gate:**
```bash
# API server
kubectl -n kube-system get pod -l component=kube-apiserver -o yaml | grep -A2 feature-gates

# Controller manager
kubectl -n kube-system get pod -l component=kube-controller-manager -o yaml | grep -A2 feature-gates
```

**Check Request Metrics:**
```bash
# Verify vLLM metrics are available
kubectl exec -n monitoring prometheus-k8s-0 -- promtool query instant http://localhost:9090 \
  'sum(increase(vllm:request_success_total{model_name="meta/llama-3.1-8b"}[10m]))'
```

### Deployment Not Scaling from Zero

**Check HPA Status:**
```bash
kubectl describe hpa llama-8b-hpa

# Look for:
# - "failed to get external metric": Check Prometheus adapter
# - "unable to get metric": Check metric availability
# - "New size: 0; reason: All metrics below target": Expected during scale-down
```

**Check WVA Metrics:**
```bash
# Verify WVA is emitting desired replicas > 0 when traffic arrives
kubectl exec -n monitoring prometheus-k8s-0 -- promtool query instant http://localhost:9090 \
  'inferno_desired_replicas{variant_name="llama-8b"}'
```

### Frequent Scale Cycling

If deployment scales to zero and back repeatedly:

1. **Increase retention period:**
   ```yaml
   retention_period: "30m"  # Longer grace period
   ```

2. **Check for background requests:**
   ```bash
   # Health checks, readiness probes, or monitoring tools may generate requests
   kubectl logs deployment/llama-8b | grep -i "health\|ready\|probe"
   ```

3. **Review HPA stabilization window:**
   ```yaml
   behavior:
     scaleDown:
       stabilizationWindowSeconds: 600  # 10 minutes
   ```

## Examples

### Example 1: Development Environment

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  default: |
    enable_scale_to_zero: true
    retention_period: "5m"  # Fast scale-down for dev
```

### Example 2: Production with Selective Scale-to-Zero

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  # Disabled by default for production
  default: |
    enable_scale_to_zero: false
  
  # Enable only for non-critical models
  llama-8b-experimental: |
    model_id: meta/llama-3.1-8b
    namespace: experiments
    enable_scale_to_zero: true
    retention_period: "30m"
```

### Example 3: Multi-Tenant with Per-Tenant Settings

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  default: |
    enable_scale_to_zero: true
    retention_period: "15m"
  
  tenant-a-premium: |
    model_id: meta/llama-3.1-70b
    namespace: tenant-a
    enable_scale_to_zero: false  # Premium tier always available
  
  tenant-b-standard: |
    model_id: meta/llama-3.1-8b
    namespace: tenant-b
    retention_period: "10m"  # Standard tier can scale to zero
```

## Roadmap

The scale-to-zero feature is being delivered in phases:

### Phase 1: Configuration Infrastructure (v0.5.0) ✅

- ConfigMap-based per-model configuration
- Environment variable support
- Configuration priority hierarchy
- Request metrics collection

### Phase 2: Scale-From-Zero Engine (Future Release)

- Automatic detection of incoming requests
- Intelligent scale-up decisions
- Cold-start optimization
- Request queuing during warm-up

### Phase 3: Advanced Features (Future)

- Predictive scale-up based on usage patterns
- Progressive warm-up strategies
- Multi-model coordination
- Cost-aware scale-to-zero decisions

## Best Practices

1. **Start Conservative**: Begin with longer retention periods (20-30 minutes) and reduce based on observed usage patterns

2. **Monitor Cold Starts**: Track P99 latency including cold starts to ensure user experience meets requirements

3. **Use Namespaces**: Separate production and non-production models to different namespaces with appropriate scale-to-zero policies

4. **Document Expectations**: Clearly communicate cold-start latencies to users/applications consuming scaled-to-zero models

5. **Test Thoroughly**: Validate scale-to-zero behavior in non-production environments before enabling in production

6. **Consider Alternatives**: For latency-sensitive workloads, `minReplicas: 1` with saturation-based scaling may be preferable to scale-to-zero

## Related Documentation

- [Configuration Guide](configuration.md) - General WVA configuration
- [HPA Integration](../integrations/hpa-integration.md) - HPA setup including scale-to-zero
- [KEDA Integration](../integrations/keda-integration.md) - Native scale-to-zero with KEDA
- [Prometheus Integration](../integrations/prometheus.md) - Metrics collection for scale-to-zero
- [CRD Reference](crd-reference.md) - VariantAutoscaling API reference

## Feedback

This feature is under active development. If you encounter issues or have suggestions:

- Open an issue: [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- Join community meetings: [llm-d Community](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)
