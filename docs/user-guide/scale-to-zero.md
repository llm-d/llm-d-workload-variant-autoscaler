# Scale-to-Zero

Scale-to-zero is a feature that enables WVA to automatically scale down inference model deployments to zero replicas when they are not receiving traffic, significantly reducing infrastructure costs for idle models.

## Overview

When scale-to-zero is enabled, WVA monitors request activity for each model. If a model receives no requests for a configurable retention period, WVA scales the deployment down to zero replicas. When new requests arrive, the deployment is automatically scaled back up.

### Benefits

- **Cost Optimization**: Eliminate compute costs for idle models
- **Resource Efficiency**: Free up cluster resources for active workloads
- **Automatic Management**: No manual intervention required

### How It Works

1. WVA continuously monitors request metrics via Prometheus
2. When no requests are received for the configured retention period, the desired replica count is set to zero
3. External autoscaler (HPA/KEDA) scales the deployment to zero replicas
4. When new requests arrive, the deployment is scaled back up automatically

## Prerequisites

- **HPA with Scale-to-Zero Support**: Kubernetes 1.31+ with `HPAScaleToZero` feature gate enabled (alpha feature), OR
- **KEDA**: KEDA natively supports scale-to-zero without requiring feature gates
- **Prometheus**: Metrics source for tracking request activity

See [HPA Integration - Scale to Zero](../integrations/hpa-integration.md#feature-scale-to-zero) for detailed HPA setup instructions.

## Configuration

Scale-to-zero can be configured at three levels with the following priority:

1. **Per-model configuration** (highest priority)
2. **Global defaults** in ConfigMap
3. **Environment variable** `WVA_SCALE_TO_ZERO`
4. **System default** (disabled)

### Global Configuration via Environment Variable

The simplest way to enable scale-to-zero for all models:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: wva-controller-manager
spec:
  template:
    spec:
      containers:
      - name: manager
        env:
        - name: WVA_SCALE_TO_ZERO
          value: "true"
```

**Helm Chart:**

```bash
helm upgrade -i workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --set wva.scaleToZero=true
```

### Per-Model Configuration via ConfigMap

For fine-grained control, create a ConfigMap to configure scale-to-zero settings per model:

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

  # Override for specific model (shorter retention)
  llama-8b-override: |
    model_id: meta/llama-3.1-8b
    retention_period: "5m"

  # Disable scale-to-zero for critical model
  llama-70b-override: |
    model_id: meta/llama-3.1-70b
    enable_scale_to_zero: false

  # Namespace-specific configuration
  llama-production: |
    model_id: meta/llama-3.1-8b
    namespace: production
    enable_scale_to_zero: true
    retention_period: "30m"
```

**Configuration Fields:**

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| `model_id` | string | Model identifier (required for overrides) | - |
| `namespace` | string | Namespace for this override (optional) | - |
| `enable_scale_to_zero` | boolean | Enable/disable scale-to-zero | Inherited from global |
| `retention_period` | duration | Time to wait after last request before scaling to zero | `10m` |

**Retention Period Format:**

Valid duration strings: `"30s"`, `"5m"`, `"1h"`, `"90m"`, etc.

### Configuration Priority

Configuration is resolved in this order:

1. **Per-model ConfigMap entry** (if `enable_scale_to_zero` is explicitly set)
2. **Global defaults** in ConfigMap (`default` key)
3. **Environment variable** `WVA_SCALE_TO_ZERO`
4. **System default** (scale-to-zero disabled, 10-minute retention)

### Partial Overrides

You can override only specific fields while inheriting others from global defaults:

```yaml
data:
  default: |
    enable_scale_to_zero: true
    retention_period: "15m"

  # Only override retention period, inherit enable_scale_to_zero=true
  fast-model: |
    model_id: fast-model-id
    retention_period: "2m"
```

## Usage Example

### With KEDA (Recommended)

KEDA natively supports scale-to-zero without requiring Kubernetes feature gates:

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: llama-8b-scaler
spec:
  scaleTargetRef:
    name: llama-8b-deployment
  minReplicaCount: 0  # Enable scale-to-zero
  maxReplicaCount: 10
  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus-operated.monitoring.svc:9090
      metricName: wva_desired_replicas
      query: |
        wva_desired_replicas{
          namespace="llm-inference",
          model_id="meta/llama-3.1-8b"
        }
      threshold: "1"
```

### With HPA (Kubernetes 1.31+)

Requires enabling the `HPAScaleToZero` feature gate:

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
  minReplicas: 0  # Requires HPAScaleToZero feature gate
  maxReplicas: 10
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 60
      policies:
      - type: Pods
        value: 1
        periodSeconds: 60
  metrics:
  - type: Object
    object:
      metric:
        name: wva_desired_replicas
      describedObject:
        apiVersion: llmd.ai/v1alpha1
        kind: VariantAutoscaling
        name: llama-8b-autoscaler
      target:
        type: AverageValue
        averageValue: "1"
```

See [HPA Integration - Scale to Zero](../integrations/hpa-integration.md#feature-scale-to-zero) for complete HPA setup instructions.

## Monitoring

### Metrics

WVA emits the following metrics for scale-to-zero monitoring:

```promql
# Last request timestamp (Unix epoch)
wva_last_request_timestamp{namespace="...", model_id="..."}

# Time since last request (seconds)
wva_time_since_last_request{namespace="...", model_id="..."}

# Scale-to-zero status (1=enabled, 0=disabled)
wva_scale_to_zero_enabled{namespace="...", model_id="..."}

# Desired replicas (will be 0 when scaled down)
wva_desired_replicas{namespace="...", model_id="..."}
```

### Observing Scale-to-Zero Behavior

**Check if a model is eligible for scale-to-zero:**

```bash
kubectl get variantautoscaling -n llm-inference -o jsonpath='{.items[*].status.conditions[?(@.type=="ScaleToZeroEligible")]}'
```

**View retention period expiration:**

```promql
# Time until scale-to-zero (negative = already eligible)
wva_scale_to_zero_retention_seconds - wva_time_since_last_request
```

**Check current replica count:**

```bash
kubectl get deployment llama-8b-deployment -n llm-inference -o jsonpath='{.status.replicas}'
```

## Best Practices

### Retention Period Selection

- **Development/Testing**: Short retention (2-5 minutes) for rapid testing
- **Staging**: Medium retention (10-15 minutes) balancing cost and availability
- **Production**: Longer retention (15-30 minutes) to avoid unnecessary cold starts

### Cold Start Considerations

When a deployment scales from zero, there is a cold start delay:

1. **Pod Creation**: ~10-30 seconds
2. **Model Loading**: 2-7 minutes depending on model size
3. **Warm-up**: Additional time for cache population

**Mitigation Strategies:**

- Set appropriate retention periods based on traffic patterns
- Use persistent volumes to speed up model loading
- Consider disabling scale-to-zero for high-priority, latency-sensitive models
- Monitor `wva_time_since_last_request` to predict scale-to-zero events

### Production Recommendations

```yaml
data:
  default: |
    enable_scale_to_zero: true
    retention_period: "20m"

  # Disable for critical, high-traffic models
  critical-model: |
    model_id: critical-model-id
    enable_scale_to_zero: false

  # Short retention for experimental models
  experimental-model: |
    model_id: experimental-model-id
    retention_period: "5m"
```

## Troubleshooting

### Deployment Not Scaling to Zero

**Symptom:** Deployment remains at 1+ replicas despite no traffic.

**Possible Causes:**

1. **Scale-to-zero not enabled**
   ```bash
   # Check configuration
   kubectl get configmap model-scale-to-zero-config -n workload-variant-autoscaler-system -o yaml
   
   # Check environment variable
   kubectl get deployment wva-controller-manager -n workload-variant-autoscaler-system -o yaml | grep WVA_SCALE_TO_ZERO
   ```

2. **Retention period not elapsed**
   ```promql
   # Check time since last request
   wva_time_since_last_request{model_id="..."}
   ```

3. **HPA feature gate not enabled** (HPA only)
   ```bash
   kubectl -n kube-system get pod -l component=kube-apiserver -o yaml | grep -A2 feature-gates
   # Should show: --feature-gates=HPAScaleToZero=true
   ```

4. **Metrics not available**
   ```bash
   # Verify Prometheus metrics
   kubectl port-forward -n monitoring svc/prometheus-operated 9090:9090
   # Open http://localhost:9090 and query: vllm:request_success_total
   ```

### Deployment Stuck at Zero Replicas

**Symptom:** Deployment scaled to zero but not scaling back up with new traffic.

**Possible Causes:**

1. **WVA not detecting new requests**
   - Verify vLLM metrics are being scraped
   - Check `vllm:request_success_total` in Prometheus

2. **HPA/KEDA not responding**
   ```bash
   # Check HPA status
   kubectl describe hpa llama-8b-hpa -n llm-inference
   
   # Check KEDA status
   kubectl describe scaledobject llama-8b-scaler -n llm-inference
   ```

3. **Metrics lag**
   - Prometheus scrape interval may cause delays (typically 15-30 seconds)
   - Adjust `reconcileInterval` if needed

### Unexpected Scale-to-Zero Events

**Symptom:** Models scale to zero during active traffic periods.

**Solutions:**

1. **Increase retention period**
   ```yaml
   retention_period: "30m"  # Increase from default 10m
   ```

2. **Check for intermittent traffic patterns**
   ```promql
   # View request rate over time
   rate(vllm:request_success_total{model_id="..."}[5m])
   ```

3. **Disable scale-to-zero for this model**
   ```yaml
   model-override: |
     model_id: your-model-id
     enable_scale_to_zero: false
   ```

## Limitations

- **Cold Start Latency**: First requests after scale-to-zero experience significant latency (2-7 minutes)
- **Alpha Feature (HPA)**: The `HPAScaleToZero` feature gate is alpha in Kubernetes 1.31 and may not be production-ready
- **Metrics Dependency**: Requires accurate vLLM metrics; if metrics are unavailable, scale-to-zero decisions may be incorrect
- **Retention Period Resolution**: Metrics are checked at reconciliation intervals (default 60s), so actual scale-down may occur slightly after retention period expires

## See Also

- [HPA Integration](../integrations/hpa-integration.md) - Using scale-to-zero with HPA
- [KEDA Integration](../integrations/keda-integration.md) - Using scale-to-zero with KEDA (recommended)
- [Configuration Guide](configuration.md) - General WVA configuration
- [Prometheus Integration](../integrations/prometheus.md) - Metrics and monitoring
