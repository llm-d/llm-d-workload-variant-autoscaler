# Custom Metrics

This guide explains the custom metrics emitted by Workload-Variant-Autoscaler (WVA) and how to use them for monitoring, alerting, and autoscaling.

## Overview

WVA emits Prometheus metrics that can be consumed by:
- **Horizontal Pod Autoscaler (HPA)** via Prometheus Adapter
- **KEDA** via Prometheus scaler
- **Monitoring dashboards** (Grafana, Prometheus UI)
- **Alerting systems** (Alertmanager, PagerDuty)

## Metric Categories

### 1. Autoscaling Decision Metrics

These metrics drive autoscaling decisions:

#### `wva_desired_replicas`

**Description:** Recommended number of replicas calculated by WVA.

**Type:** Gauge

**Labels:**
- `namespace`: Namespace of the target deployment
- `deployment`: Name of the target deployment
- `model_id`: Model identifier
- `variant_name`: Name of the VariantAutoscaling resource

**Example:**
```promql
wva_desired_replicas{
  namespace="llm-inference",
  deployment="llama-8b",
  model_id="meta/llama-3.1-8b",
  variant_name="llama-8b-autoscaler"
}
```

**Usage:** Primary metric for HPA/KEDA scaling decisions.

---

#### `wva_current_replicas`

**Description:** Current number of replicas running.

**Type:** Gauge

**Labels:** Same as `wva_desired_replicas`

**Example:**
```promql
wva_current_replicas{namespace="llm-inference", deployment="llama-8b"}
```

**Usage:** Compare with `wva_desired_replicas` to monitor scaling lag.

---

### 2. Saturation Metrics

Metrics indicating server saturation levels:

#### `wva_saturation_kv_cache_utilization`

**Description:** KV cache utilization percentage (0-100).

**Type:** Gauge

**Labels:**
- `namespace`
- `deployment`
- `pod`: Specific pod name
- `variant_name`

**Example:**
```promql
wva_saturation_kv_cache_utilization{
  namespace="llm-inference",
  pod="llama-8b-5f7b8c9d-xz42k"
}
```

**Usage:** Monitor memory pressure on inference servers.

**Thresholds:**
- < 70%: Healthy
- 70-85%: Warning (consider scaling)
- \> 85%: Critical (scale immediately)

---

#### `wva_saturation_queue_depth`

**Description:** Number of requests waiting in the vLLM server queue.

**Type:** Gauge

**Labels:** Same as KV cache metric

**Example:**
```promql
wva_saturation_queue_depth{namespace="llm-inference", pod="llama-8b-5f7b8c9d-xz42k"}
```

**Usage:** Detect request backpressure and scaling needs.

**Thresholds:**
- < 10: Healthy
- 10-50: Warning
- \> 50: Critical

---

#### `wva_saturation_overall`

**Description:** Composite saturation score (0-1) combining KV cache and queue depth.

**Type:** Gauge

**Labels:**
- `namespace`
- `deployment`
- `variant_name`

**Example:**
```promql
wva_saturation_overall{namespace="llm-inference", deployment="llama-8b"}
```

**Usage:** Single metric for overall server health.

---

### 3. Controller Health Metrics

Metrics about WVA controller itself:

#### `wva_controller_reconcile_duration_seconds`

**Description:** Time spent in reconciliation loop.

**Type:** Histogram

**Labels:**
- `controller`: Always "variantautoscaling"
- `success`: "true" or "false"

**Example:**
```promql
histogram_quantile(0.95, 
  rate(wva_controller_reconcile_duration_seconds_bucket[5m])
)
```

**Usage:** Monitor controller performance and detect slowdowns.

---

#### `wva_controller_reconcile_total`

**Description:** Total number of reconciliation loops.

**Type:** Counter

**Labels:**
- `controller`
- `result`: "success", "error", or "requeue"

**Example:**
```promql
rate(wva_controller_reconcile_total{result="error"}[5m])
```

**Usage:** Track reconciliation failures.

---

#### `wva_controller_reconcile_errors_total`

**Description:** Total number of reconciliation errors.

**Type:** Counter

**Labels:**
- `controller`
- `namespace`
- `variant_name`

**Example:**
```promql
increase(wva_controller_reconcile_errors_total[1h])
```

**Usage:** Alert on sustained errors.

---

### 4. Metrics Collection Metrics

Metrics about WVA's interaction with Prometheus and vLLM:

#### `wva_metrics_collection_duration_seconds`

**Description:** Time taken to collect metrics from Prometheus.

**Type:** Histogram

**Labels:**
- `source`: "prometheus" or "pod"
- `success`: "true" or "false"

**Example:**
```promql
histogram_quantile(0.99,
  rate(wva_metrics_collection_duration_seconds_bucket[5m])
)
```

**Usage:** Detect slow Prometheus queries.

---

#### `wva_metrics_collection_failures_total`

**Description:** Total number of metric collection failures.

**Type:** Counter

**Labels:**
- `source`
- `reason`: Error category

**Example:**
```promql
rate(wva_metrics_collection_failures_total[5m])
```

**Usage:** Alert on collection issues.

---

### 5. Optimization Metrics

Metrics from the capacity optimizer:

#### `wva_optimizer_execution_duration_seconds`

**Description:** Time taken by optimization algorithm.

**Type:** Histogram

**Example:**
```promql
histogram_quantile(0.95,
  rate(wva_optimizer_execution_duration_seconds_bucket[5m])
)
```

**Usage:** Monitor optimizer performance.

---

#### `wva_optimizer_cost_estimate`

**Description:** Estimated cost of current allocation.

**Type:** Gauge

**Labels:**
- `namespace`
- `variant_name`

**Example:**
```promql
wva_optimizer_cost_estimate{namespace="llm-inference"}
```

**Usage:** Track infrastructure costs.

---

## Using Metrics with HPA

Configure HPA to use WVA metrics via Prometheus Adapter:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: llama-8b-hpa
  namespace: llm-inference
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: llama-8b
  minReplicas: 1
  maxReplicas: 10
  metrics:
  - type: External
    external:
      metric:
        name: wva_desired_replicas
        selector:
          matchLabels:
            deployment: llama-8b
            namespace: llm-inference
      target:
        type: Value
        value: "1"
```

**Note:** The target value is typically `"1"` because WVA directly outputs the desired replica count.

See the [HPA Integration guide](../integrations/hpa-integration.md) for complete setup.

---

## Using Metrics with KEDA

Configure KEDA ScaledObject:

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: llama-8b-scaler
  namespace: llm-inference
spec:
  scaleTargetRef:
    name: llama-8b
  minReplicaCount: 1
  maxReplicaCount: 10
  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus.monitoring:9090
      query: |
        wva_desired_replicas{
          namespace="llm-inference",
          deployment="llama-8b"
        }
      threshold: '1'
```

See the [KEDA Integration guide](../integrations/keda-integration.md) for complete setup.

---

## Common PromQL Queries

### Scaling Status

```promql
# Compare desired vs current replicas
wva_desired_replicas - wva_current_replicas
```

### Saturation Alerts

```promql
# High KV cache utilization
wva_saturation_kv_cache_utilization > 85

# High queue depth
wva_saturation_queue_depth > 50

# Overall saturation
wva_saturation_overall > 0.8
```

### Controller Health

```promql
# Reconciliation error rate
rate(wva_controller_reconcile_errors_total[5m]) > 0.1

# Slow reconciliation
histogram_quantile(0.95,
  rate(wva_controller_reconcile_duration_seconds_bucket[5m])
) > 5
```

### Scaling Activity

```promql
# Rate of replica changes
rate(wva_current_replicas[5m])

# Time series of desired replicas
wva_desired_replicas{namespace="llm-inference"}
```

---

## Grafana Dashboard Example

Sample dashboard panels:

### Panel 1: Desired vs Current Replicas

```promql
# Query A (Desired)
wva_desired_replicas{namespace="$namespace", deployment="$deployment"}

# Query B (Current)
wva_current_replicas{namespace="$namespace", deployment="$deployment"}
```

**Visualization:** Time series graph

---

### Panel 2: Saturation Heatmap

```promql
# Query
wva_saturation_kv_cache_utilization{namespace="$namespace"}
```

**Visualization:** Heatmap by pod

---

### Panel 3: Scaling Lag

```promql
# Query
abs(
  wva_desired_replicas{namespace="$namespace", deployment="$deployment"} -
  wva_current_replicas{namespace="$namespace", deployment="$deployment"}
)
```

**Visualization:** Single stat with sparkline

---

## Alerting Rules

Example Prometheus alerting rules:

```yaml
groups:
- name: wva_alerts
  interval: 30s
  rules:
  - alert: WVAHighSaturation
    expr: wva_saturation_overall > 0.9
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: "WVA detecting critical saturation"
      description: "Deployment {{ $labels.deployment }} in {{ $labels.namespace }} has saturation {{ $value }}"

  - alert: WVAReconciliationErrors
    expr: rate(wva_controller_reconcile_errors_total[5m]) > 0.1
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "WVA controller experiencing errors"
      description: "Error rate: {{ $value }} errors/sec"

  - alert: WVAScalingLag
    expr: |
      abs(
        wva_desired_replicas - wva_current_replicas
      ) > 2
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "Deployment not matching desired replicas"
      description: "{{ $labels.deployment }} has scaling lag"

  - alert: WVAMetricsCollectionFailure
    expr: rate(wva_metrics_collection_failures_total[5m]) > 0.5
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "WVA cannot collect metrics"
      description: "Collection failures: {{ $value }}/sec"
```

---

## Metric Retention

**Recommendations:**
- **Autoscaling metrics:** 7-15 days (high resolution)
- **Saturation metrics:** 7-15 days (high resolution)
- **Controller metrics:** 30 days (medium resolution)
- **Cost metrics:** 90 days (low resolution for trend analysis)

Configure in Prometheus:

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
- job_name: 'wva-controller'
  scrape_interval: 15s  # Frequent for autoscaling
  static_configs:
  - targets: ['wva-controller-service:8443']
```

---

## Debugging with Metrics

### Check if WVA is working

```promql
# Should return data
wva_controller_reconcile_total
```

### Find silent metrics

```promql
# Metrics with no updates in 5 minutes
time() - timestamp(wva_desired_replicas) > 300
```

### Identify stale VAs

```promql
# VAs with no metric updates
absent_over_time(wva_desired_replicas[10m])
```

---

## Best Practices

1. **Use metric selectors** - Always include `namespace` and `deployment` labels in queries
2. **Set appropriate scrape intervals** - 15s for autoscaling metrics, 30s+ for others
3. **Monitor collection health** - Alert on `wva_metrics_collection_failures_total`
4. **Dashboard by namespace** - Use Grafana variables for filtering
5. **Test queries** - Validate PromQL before adding to HPA/KEDA

---

## Related Documentation

- [Prometheus Integration](../integrations/prometheus.md)
- [HPA Integration](../integrations/hpa-integration.md)
- [KEDA Integration](../integrations/keda-integration.md)
- [Metrics Health Monitoring](../metrics-health-monitoring.md)

---

**Need help?** Check the [Troubleshooting Guide](../user-guide/troubleshooting.md) or open an [issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues).
