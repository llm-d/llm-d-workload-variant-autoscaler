# Custom Metrics Reference

## Overview

The Workload-Variant-Autoscaler (WVA) exposes custom metrics to Prometheus that reflect scaling decisions and current deployment state. These metrics enable:

- **HPA/KEDA integration**: External autoscalers read metrics to scale deployments
- **Monitoring and alerting**: Operators track autoscaling behavior
- **Debugging**: Troubleshoot scaling decisions and performance

All metrics are emitted after each reconciliation cycle and queryable via Prometheus.

## Metric Types

WVA emits two types of metrics:

1. **Input metrics**: vLLM metrics collected from inference servers
2. **Output metrics**: Scaling decisions and state exposed by WVA

This document focuses on **output metrics** that WVA produces. For vLLM input metrics, see [vLLM Metrics](#vllm-input-metrics).

## WVA Output Metrics

### inferno_desired_replicas

**Type**: Gauge  
**Description**: The optimal number of replicas calculated by WVA's optimization engine.

**Labels**:
- `variant_name`: Name of the VariantAutoscaling resource
- `namespace`: Kubernetes namespace
- `accelerator_type`: GPU accelerator type (e.g., "A100", "H100", "L40S")

**Example Query**:
```promql
inferno_desired_replicas{variant_name="llama-8b-autoscaler", namespace="llm-inference"}
```

**Use Cases**:
- Monitor scaling decisions over time
- Alert on unexpected scaling behavior
- Compare desired vs. current replicas

---

### inferno_current_replicas

**Type**: Gauge  
**Description**: The current number of replicas for the deployment (ready replicas reporting metrics).

**Labels**:
- `variant_name`: Name of the VariantAutoscaling resource
- `namespace`: Kubernetes namespace
- `accelerator_type`: GPU accelerator type

**Example Query**:
```promql
inferno_current_replicas{variant_name="llama-8b-autoscaler", namespace="llm-inference"}
```

**Use Cases**:
- Track actual deployment state
- Calculate scaling lag (desired - current)
- Monitor replica availability

**Note**: This reflects **ready replicas** (those reporting metrics), not total pod count. Pending or unhealthy pods are excluded.

---

### inferno_desired_ratio

**Type**: Gauge  
**Description**: The ratio of desired replicas to current replicas (desired / current). This is the **primary metric** for HPA/KEDA scaling.

**Labels**:
- `variant_name`: Name of the VariantAutoscaling resource
- `namespace`: Kubernetes namespace
- `accelerator_type`: GPU accelerator type

**Value Range**:
- `> 1.0`: Scale up needed (e.g., 1.5 = 50% increase needed)
- `= 1.0`: Current allocation is optimal
- `< 1.0`: Scale down possible (e.g., 0.75 = 25% reduction possible)

**Example Query**:
```promql
inferno_desired_ratio{variant_name="llama-8b-autoscaler", namespace="llm-inference"}
```

**Use Cases**:
- **HPA target metric**: Configure HPA to target ratio of 1.0
- **KEDA scaling**: Use as ScaledObject trigger
- **Alerting**: Notify when ratio deviates significantly from 1.0

**HPA Configuration Example**:
```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: llama-8b-hpa
spec:
  scaleTargetRef:
    kind: Deployment
    name: llama-8b
  minReplicas: 1
  maxReplicas: 10
  metrics:
  - type: External
    external:
      metric:
        name: inferno_desired_ratio
        selector:
          matchLabels:
            variant_name: llama-8b-autoscaler
      target:
        type: Value
        value: "1"
```

See [HPA Integration](hpa-integration.md) for complete configuration.

---

### inferno_replica_scaling_total

**Type**: Counter  
**Description**: Total number of scaling operations performed, with direction and reason.

**Labels**:
- `variant_name`: Name of the VariantAutoscaling resource
- `namespace`: Kubernetes namespace
- `accelerator_type`: GPU accelerator type
- `direction`: Scaling direction ("up" or "down")
- `reason`: Scaling reason (e.g., "saturation", "demand_increase", "cost_optimization")

**Example Query**:
```promql
# Total scale-ups for a variant
sum(increase(inferno_replica_scaling_total{variant_name="llama-8b-autoscaler", direction="up"}[1h]))

# Scale operations by reason
sum by (reason) (increase(inferno_replica_scaling_total{namespace="llm-inference"}[24h]))
```

**Use Cases**:
- Track scaling frequency and patterns
- Identify scaling triggers
- Alert on excessive scaling (thrashing)
- Generate scaling reports

**Scaling Reasons**:
- `saturation`: KV cache or queue saturation detected
- `demand_increase`: Request rate increased
- `demand_decrease`: Request rate decreased
- `cost_optimization`: Scaling to lower-cost variant
- `safety`: Worst-case safety check triggered

---

## vLLM Input Metrics

WVA queries the following vLLM metrics from Prometheus to make scaling decisions:

### Core Metrics

| Metric | Description | Purpose |
|--------|-------------|---------|
| `vllm:num_requests_running` | Current number of running requests | Validate metrics availability |
| `vllm:request_success_total` | Total successful requests | Calculate arrival rate |
| `vllm:kv_cache_usage_perc` | KV cache utilization (0.0-1.0) | Detect saturation, prevent OOM |
| `vllm:num_requests_waiting` | Requests waiting in queue | Detect queue saturation |

### Performance Metrics

| Metric | Description | Purpose |
|--------|-------------|---------|
| `vllm:time_to_first_token_seconds_sum` | Sum of TTFT across requests | Calculate average TTFT |
| `vllm:time_to_first_token_seconds_count` | Count of TTFT measurements | Calculate average TTFT |
| `vllm:time_per_output_token_seconds_sum` | Sum of ITL across requests | Calculate inter-token latency |
| `vllm:time_per_output_token_seconds_count` | Count of ITL measurements | Calculate inter-token latency |

### Token Metrics

| Metric | Description | Purpose |
|--------|-------------|---------|
| `vllm:request_prompt_tokens_sum` | Sum of prompt tokens | Calculate average prompt length |
| `vllm:request_prompt_tokens_count` | Count of prompt token measurements | Calculate average prompt length |
| `vllm:request_generation_tokens_sum` | Sum of generated tokens | Calculate average output length |
| `vllm:request_generation_tokens_count` | Count of generation token measurements | Calculate average output length |

For detailed vLLM metric configuration, see [vLLM documentation](https://docs.vllm.ai/en/latest/serving/metrics.html).

---

## Querying Metrics

### Using Prometheus UI

1. **Port-forward to Prometheus**:
   ```bash
   kubectl port-forward -n monitoring svc/prometheus-k8s 9090:9090
   ```

2. **Access UI**: Navigate to http://localhost:9090

3. **Example Queries**:
   ```promql
   # Current desired replicas for all variants
   inferno_desired_replicas
   
   # Desired ratio over time
   inferno_desired_ratio{namespace="llm-inference"}
   
   # Scaling operations in last hour
   increase(inferno_replica_scaling_total[1h])
   ```

### Using kubectl with Prometheus Adapter

If using Prometheus Adapter for custom metrics API:

```bash
# List available custom metrics
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1" | jq .

# Query specific metric
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1/namespaces/llm-inference/metrics/inferno_desired_ratio" | jq .
```

---

## Metric Labels

### Standard Labels

All WVA metrics include these standard labels:

| Label | Description | Example |
|-------|-------------|---------|
| `variant_name` | VariantAutoscaling resource name | `"llama-8b-autoscaler"` |
| `namespace` | Kubernetes namespace | `"llm-inference"` |
| `accelerator_type` | GPU accelerator type | `"A100"`, `"H100"`, `"L40S"` |

### Optional Labels

Some metrics include additional labels:

| Label | Applies To | Description |
|-------|------------|-------------|
| `direction` | `inferno_replica_scaling_total` | Scaling direction: `"up"` or `"down"` |
| `reason` | `inferno_replica_scaling_total` | Scaling reason (see above) |
| `model_name` | (future) | Model identifier |
| `controller_instance` | (multi-controller) | Controller instance ID |

---

## Integration Examples

### Grafana Dashboard

Example Grafana panel queries:

**Desired vs. Current Replicas**:
```promql
# Desired replicas
inferno_desired_replicas{variant_name="llama-8b-autoscaler"}

# Current replicas
inferno_current_replicas{variant_name="llama-8b-autoscaler"}
```

**Scaling Operations Rate**:
```promql
sum by (direction) (rate(inferno_replica_scaling_total{namespace="llm-inference"}[5m]))
```

**Scaling Lag**:
```promql
inferno_desired_replicas - inferno_current_replicas
```

### Alerting Rules

Example Prometheus alerting rules:

```yaml
groups:
- name: wva-alerts
  rules:
  # Alert on high scaling lag
  - alert: WVAHighScalingLag
    expr: |
      abs(inferno_desired_replicas - inferno_current_replicas) > 2
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "High scaling lag for {{ $labels.variant_name }}"
      description: "Desired replicas differ from current by more than 2 for 10 minutes"

  # Alert on excessive scaling
  - alert: WVAExcessiveScaling
    expr: |
      sum by (variant_name) (increase(inferno_replica_scaling_total[1h])) > 10
    labels:
      severity: warning
    annotations:
      summary: "Excessive scaling activity for {{ $labels.variant_name }}"
      description: "More than 10 scaling operations in the last hour"

  # Alert on low desired ratio (under-provisioned)
  - alert: WVAUnderProvisioned
    expr: |
      inferno_desired_ratio > 1.5
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "{{ $labels.variant_name }} is under-provisioned"
      description: "Desired replicas are 50% higher than current for 5 minutes"
```

---

## Best Practices

### Metric Collection

1. **Scrape interval**: Use 15-30 second intervals for timely scaling decisions
2. **Retention**: Keep at least 7 days for trend analysis
3. **High availability**: Run Prometheus in HA mode for production

### Monitoring

1. **Track scaling lag**: Alert if `desired - current > threshold` persists
2. **Monitor scaling frequency**: Excessive scaling indicates instability
3. **Correlate with vLLM metrics**: Compare WVA decisions with actual server load

### Debugging

1. **Check metric availability**: Verify metrics exist in Prometheus before investigating HPA
2. **Compare timestamps**: Ensure metrics are recent (not stale)
3. **Query raw values**: Use Prometheus UI to validate metric values directly

---

## Troubleshooting

### Metrics Not Appearing

**Problem**: WVA metrics not visible in Prometheus

**Solutions**:
1. Verify ServiceMonitor is configured:
   ```bash
   kubectl get servicemonitor -n workload-variant-autoscaler-system
   ```

2. Check Prometheus targets:
   ```bash
   kubectl port-forward -n monitoring svc/prometheus-k8s 9090:9090
   # Visit http://localhost:9090/targets
   ```

3. Check WVA controller logs:
   ```bash
   kubectl logs -n workload-variant-autoscaler-system \
     deployment/workload-variant-autoscaler-controller-manager
   ```

### Stale Metrics

**Problem**: Metrics exist but values are outdated

**Causes**:
- WVA controller not reconciling (check controller logs)
- Prometheus scraping stopped (check targets status)
- Network issues between WVA and Prometheus

**Solution**: Check `MetricsAvailable` condition on VariantAutoscaling resource:
```bash
kubectl describe variantautoscaling <name> -n <namespace>
```

### HPA Not Scaling

**Problem**: HPA configured but not triggering scaling

**Checklist**:
1. HPA can read the metric:
   ```bash
   kubectl describe hpa <name> -n <namespace>
   # Check "Metrics" section
   ```

2. Prometheus Adapter is configured (if using custom metrics API)

3. Metric selector matches WVA metric labels

See [HPA Integration](hpa-integration.md) for complete HPA troubleshooting.

---

## Related Documentation

- [Prometheus Integration](prometheus.md) - Prometheus setup and configuration
- [HPA Integration](hpa-integration.md) - Using metrics with HPA
- [KEDA Integration](keda-integration.md) - Using metrics with KEDA
- [Metrics Health Monitoring](../metrics-health-monitoring.md) - Validating metrics availability

---

## Reference

- **Metric Definition**: [`internal/constants/metrics.go`](../../internal/constants/metrics.go)
- **Metric Emission**: [`internal/actuator/actuator.go`](../../internal/actuator/actuator.go)
- **vLLM Metrics**: [vLLM Documentation](https://docs.vllm.ai/en/latest/serving/metrics.html)
