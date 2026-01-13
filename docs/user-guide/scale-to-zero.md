# Scale-to-Zero Configuration

This guide explains how to configure WVA's scale-to-zero feature, which enables complete resource optimization by scaling deployments down to zero replicas when no traffic is detected.

## Overview

The scale-to-zero feature monitors request patterns for your model deployments and automatically scales them down to zero replicas after a configurable retention period of inactivity. This provides:

- **Cost Optimization**: Eliminates compute costs for idle models
- **Resource Efficiency**: Frees up GPU resources for active workloads
- **Automatic Recovery**: Seamlessly scales back up when requests resume

## Architecture

WVA's scale-to-zero implementation uses:

1. **Request Monitoring**: Tracks successful requests via Prometheus metrics (`vllm:request_success_total`)
2. **Retention Period**: Configurable idle time before scaling to zero
3. **Per-Model Configuration**: Global defaults with per-model overrides
4. **Integration with HPA/KEDA**: Works with standard Kubernetes autoscalers

## Configuration Methods

### 1. Global Enable via Helm Values

Enable scale-to-zero for all models using Helm values:

```yaml
# values.yaml
wva:
  scaleToZero: true  # Enable globally (default: false)
```

Or via command line:

```bash
helm upgrade -i workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  --set wva.scaleToZero=true
```

**Note:** This sets the `WVA_SCALE_TO_ZERO` environment variable, which serves as the system-wide default.

### 2. Per-Model Configuration via ConfigMap

For fine-grained control, use the `model-scale-to-zero-config` ConfigMap to configure scale-to-zero behavior per model:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  # Global defaults for all models
  default: |
    enable_scale_to_zero: true
    retention_period: "15m"

  # Override for specific model - custom retention period
  llama-8b: |
    model_id: meta/llama-3.1-8b
    retention_period: "5m"

  # Override to disable scale-to-zero for critical model
  llama-70b: |
    model_id: meta/llama-3.1-70b
    enable_scale_to_zero: false

  # Namespace-specific override
  llama-production: |
    model_id: meta/llama-3.1-8b
    namespace: production
    enable_scale_to_zero: true
    retention_period: "30m"
```

## Configuration Reference

### ConfigMap Fields

Each entry in the ConfigMap supports the following fields:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `model_id` | string | Yes (for overrides) | Model identifier matching the `modelID` in VariantAutoscaling CR |
| `namespace` | string | No | Kubernetes namespace (optional, for namespace-specific overrides) |
| `enable_scale_to_zero` | boolean | No | Enable/disable scale-to-zero for this model |
| `retention_period` | string | No | Duration to wait after last request before scaling to zero (e.g., "5m", "1h", "30s") |

### Configuration Priority

WVA resolves scale-to-zero configuration in the following priority order (highest to lowest):

1. **Per-model ConfigMap entry** (with matching `model_id` and optional `namespace`)
2. **Global defaults** (`default` key in ConfigMap)
3. **Environment variable** (`WVA_SCALE_TO_ZERO`)
4. **System default** (disabled, 10-minute retention)

### Default Values

- **Enable Scale-to-Zero**: `false` (disabled by default)
- **Retention Period**: `10m` (10 minutes)

## Examples

### Example 1: Enable Globally with Default Settings

```bash
# Enable scale-to-zero with 10-minute retention for all models
helm upgrade -i workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  --set wva.scaleToZero=true
```

### Example 2: Custom Global Defaults

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  default: |
    enable_scale_to_zero: true
    retention_period: "20m"  # Wait 20 minutes before scaling to zero
```

### Example 3: Partial Overrides

You can override only specific fields while inheriting others from defaults:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  # Global: enabled with 15-minute retention
  default: |
    enable_scale_to_zero: true
    retention_period: "15m"

  # Override only retention period (inherits enable_scale_to_zero=true)
  fast-scaling-model: |
    model_id: meta/llama-3.1-8b
    retention_period: "5m"

  # Override only enable flag (inherits retention_period="15m")
  no-scale-model: |
    model_id: meta/llama-3.1-70b
    enable_scale_to_zero: false
```

### Example 4: Environment-Specific Configuration

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scale-to-zero-config
  namespace: workload-variant-autoscaler-system
data:
  # Production: longer retention for stability
  llama-prod: |
    model_id: meta/llama-3.1-8b
    namespace: production
    retention_period: "30m"

  # Development: aggressive scale-to-zero for cost savings
  llama-dev: |
    model_id: meta/llama-3.1-8b
    namespace: development
    retention_period: "2m"
```

## Integration with HPA

To enable scale-to-zero with HPA, you need to:

1. **Enable HPAScaleToZero feature gate** (Kubernetes 1.31+ alpha feature)
2. **Set HPA minReplicas to 0**
3. **Configure WVA scale-to-zero settings**

### Prerequisites

Enable the `HPAScaleToZero` feature gate in your cluster:

**For Kind clusters:**

```bash
# Edit kube-apiserver
sed -i 's#- kube-apiserver#- kube-apiserver\n    - --feature-gates=HPAScaleToZero=true#g' /etc/kubernetes/manifests/kube-apiserver.yaml

# Edit kube-controller-manager
sed -i 's#- kube-controller-manager#- kube-controller-manager\n    - --feature-gates=HPAScaleToZero=true#g' /etc/kubernetes/manifests/kube-controller-manager.yaml
```

**For managed Kubernetes clusters**, check your provider's documentation for enabling alpha features.

### HPA Configuration

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: llama-8b-hpa
spec:
  minReplicas: 0  # HPAScaleToZero - alpha feature
  maxReplicas: 10
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: llama-8b
  metrics:
  - type: Object
    object:
      metric:
        name: inferno_desired_replicas
      describedObject:
        apiVersion: llmd.ai/v1alpha1
        kind: VariantAutoscaling
        name: llama-8b-autoscaler
      target:
        type: AverageValue
        averageValue: "1"
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 240
      policies:
      - type: Pods
        value: 1
        periodSeconds: 60
```

See [HPA Integration Guide](../integrations/hpa-integration.md#feature-scale-to-zero) for complete details.

## Integration with KEDA

KEDA natively supports scaling to zero without requiring feature gates:

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: llama-8b-scaler
spec:
  scaleTargetRef:
    name: llama-8b
  minReplicaCount: 0  # Native support - no feature gate needed
  maxReplicaCount: 10
  pollingInterval: 30
  cooldownPeriod: 300
  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus:9090
      metricName: inferno_desired_replicas
      threshold: "1"
      query: |
        inferno_desired_replicas{
          variantautoscaling_name="llama-8b-autoscaler",
          variantautoscaling_namespace="llm-inference"
        }
```

See [KEDA Integration Guide](../integrations/keda-integration.md) for more information.

## Monitoring and Troubleshooting

### Checking Scale-to-Zero Status

View the scale-to-zero configuration being used:

```bash
# Check ConfigMap
kubectl get configmap model-scale-to-zero-config \
  -n workload-variant-autoscaler-system \
  -o yaml

# Check WVA controller logs
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller \
  | grep -i "scale.*zero"
```

### Verifying Request Metrics

Ensure Prometheus is collecting request metrics:

```bash
# Query Prometheus for request count
curl -G 'http://prometheus:9090/api/v1/query' \
  --data-urlencode 'query=sum(increase(vllm:request_success_total{namespace="llm-inference",model_name="meta/llama-3.1-8b"}[10m]))'
```

Expected response when requests are present:
```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [{"value": [1234567890, "42"]}]
  }
}
```

Expected response when no requests (ready for scale-to-zero):
```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": []
  }
}
```

### Common Issues

#### Model Not Scaling to Zero

**Symptoms:**
- HPA doesn't scale deployment to zero despite no traffic
- WVA continues to recommend replicas > 0

**Possible causes:**

1. **HPAScaleToZero feature gate not enabled**
   ```bash
   # Check if feature gate is enabled
   kubectl get --raw /metrics | grep -i scaletozero
   ```

2. **Prometheus metrics show recent requests**
   ```bash
   # Verify no requests in retention period
   kubectl logs -n workload-variant-autoscaler-system \
     deployment/workload-variant-autoscaler-controller \
     | grep "model_request_count"
   ```

3. **Scale-to-zero disabled for model**
   ```bash
   # Check configuration
   kubectl get configmap model-scale-to-zero-config \
     -n workload-variant-autoscaler-system \
     -o jsonpath='{.data}'
   ```

4. **Retention period not elapsed**
   - Wait for the configured retention period after the last request
   - Default is 10 minutes

#### HPA Scaling Back Up Too Slowly

**Solution:** Configure HPA behavior for faster scale-up:

```yaml
behavior:
  scaleUp:
    stabilizationWindowSeconds: 0  # React immediately
    policies:
    - type: Pods
      value: 5  # Scale up 5 pods at a time
      periodSeconds: 30
```

See [HPA Integration - Behavior Configuration](../integrations/hpa-integration.md#hpa-behavior-configuration) for optimization strategies.

## Best Practices

### Retention Period Guidelines

Choose retention periods based on your workload characteristics:

| Use Case | Recommended Retention | Rationale |
|----------|----------------------|-----------|
| **Development/Testing** | 2-5 minutes | Maximize cost savings |
| **Production (bursty)** | 15-30 minutes | Balance cost and cold-start frequency |
| **Production (critical)** | Disable scale-to-zero | Eliminate cold-start latency |
| **Scheduled workloads** | 5-10 minutes | Scale down quickly between jobs |

### Cold-Start Considerations

When a model scales from zero:

1. **Pod creation**: 10-60 seconds (depends on image pull, init containers)
2. **Model loading**: 2-7 minutes (depends on model size, GPU memory)
3. **First request**: Additional 1-5 seconds (JIT compilation, cache warmup)

**Total cold-start latency**: 2-8 minutes

**Mitigation strategies:**

- Use image pull secrets and ensure images are pre-cached on nodes
- Configure appropriate retention periods to balance cost vs. availability
- Consider keeping at least 1 replica for critical models
- Use readiness probes to avoid routing traffic before model is ready

### Cost-Performance Trade-offs

**Aggressive scale-to-zero (short retention):**
- ✅ Maximum cost savings
- ✅ Efficient resource utilization
- ❌ Frequent cold starts
- ❌ Higher latency variability

**Conservative scale-to-zero (long retention):**
- ✅ Fewer cold starts
- ✅ Predictable latency
- ❌ Higher idle costs
- ❌ Less resource efficiency

**Recommended approach:**
- Start with 15-minute retention in production
- Monitor cold-start frequency and costs
- Adjust retention period based on actual traffic patterns
- Use different configurations for dev/staging/prod environments

### Security Considerations

1. **ConfigMap permissions**: Restrict write access to prevent unauthorized changes
2. **Namespace isolation**: Use namespace-specific overrides for multi-tenant environments
3. **Monitoring**: Set up alerts for unexpected scale-to-zero events

## Next Steps

- [HPA Integration Guide](../integrations/hpa-integration.md)
- [KEDA Integration Guide](../integrations/keda-integration.md)
- [Prometheus Metrics Reference](../integrations/prometheus.md)
- [Configuration Guide](configuration.md)
