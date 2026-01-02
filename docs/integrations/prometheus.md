# Prometheus Integration

The WVA controller integrates with Prometheus to collect vLLM metrics and expose custom autoscaling metrics. This document covers both aspects of the Prometheus integration.

## Overview

WVA uses Prometheus for two purposes:

1. **Metric Collection** - Query vLLM server metrics (KV cache, queue depth, request rates)
2. **Metric Exposition** - Expose WVA's scaling decisions as custom metrics

## Metric Collection Architecture

WVA uses a sophisticated metrics collector with caching and background fetching to minimize latency and Prometheus load. For detailed architecture information, see the [Metrics Collector Architecture](../developer-guide/metrics-collector.md) documentation.

### Key Features

- **Intelligent caching** - Reduces Prometheus query load by 70-90%
- **Background fetching** - Proactive metric collection to minimize reconciliation latency
- **Freshness tracking** - Monitors metric age and staleness
- **Thread-safe operations** - Concurrent access from multiple goroutines

### Configuration

The metrics collector is configured automatically with sensible defaults:

- **Cache TTL**: 30 seconds
- **Background fetch interval**: 30 seconds  
- **Freshness threshold**: 1 minute (fresh), 5 minutes (unavailable)

These defaults work well for most production deployments. For custom configuration, see the [Metrics Collector Architecture](../developer-guide/metrics-collector.md) guide.

## WVA Custom Metrics

The WVA exposes a focused set of custom metrics that provide insights into the autoscaling behavior and optimization performance. These metrics are exposed via Prometheus and can be used for monitoring, alerting, and dashboard creation.

## Metrics Overview

All custom metrics are prefixed with `inferno_` and include labels for `variant_name`, `namespace`, and other relevant dimensions to enable detailed analysis and filtering.

## Optimization Metrics

*No optimization metrics are currently exposed. Optimization timing is logged at DEBUG level.*

## Replica Management Metrics

### `inferno_current_replicas`
- **Type**: Gauge
- **Description**: Current number of replicas for each variant
- **Labels**:
  - `variant_name`: Name of the variant
  - `namespace`: Kubernetes namespace
  - `accelerator_type`: Type of accelerator being used
- **Use Case**: Monitor current number of replicas per variant

### `inferno_desired_replicas`
- **Type**: Gauge
- **Description**: Desired number of replicas for each variant
- **Labels**:
  - `variant_name`: Name of the variant
  - `namespace`: Kubernetes namespace
  - `accelerator_type`: Type of accelerator being used
- **Use Case**: Expose the desired optimized number of replicas per variant

### `inferno_desired_ratio`
- **Type**: Gauge
- **Description**: Ratio of the desired number of replicas and the current number of replicas for each variant
- **Labels**:
  - `variant_name`: Name of the variant
  - `namespace`: Kubernetes namespace
  - `accelerator_type`: Type of accelerator being used
- **Use Case**: Compare the desired and current number of replicas per variant, for scaling purposes

### `inferno_replica_scaling_total`
- **Type**: Counter
- **Description**: Total number of replica scaling operations
- **Labels**:
  - `variant_name`: Name of the variant
  - `namespace`: Kubernetes namespace
  - `direction`: Direction of scaling (up, down)
  - `reason`: Reason for scaling
- **Use Case**: Track scaling frequency and reasons

## Configuration

### Metrics Endpoint
The metrics are exposed at the `/metrics` endpoint on port 8080 (HTTP).

### ServiceMonitor Configuration
```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: workload-variant-autoscaler
  namespace: workload-variant-autoscaler-system
  labels:
    release: kube-prometheus-stack
spec:
  selector:
    matchLabels:
      control-plane: controller-manager
  endpoints:
  - port: http
    scheme: http
    interval: 30s
    path: /metrics
```

## Example Queries

### Basic Queries
```promql
# Current replicas by variant
inferno_current_replicas

# Scaling frequency
rate(inferno_replica_scaling_total[5m])

# Desired replicas by variant
inferno_desired_replicas
```

### Advanced Queries
```promql
# Scaling frequency by direction
rate(inferno_replica_scaling_total{direction="scale_up"}[5m])

# Replica count mismatch
abs(inferno_desired_replicas - inferno_current_replicas)

# Scaling frequency by reason
rate(inferno_replica_scaling_total[5m]) by (reason)
```

## Related Documentation

- **[Metrics Collector Architecture](../developer-guide/metrics-collector.md)** - Detailed guide on metrics collection, caching, and background fetching
- **[Configuration Guide](../user-guide/configuration.md)** - WVA configuration options
- **[Saturation Analyzer](../saturation-analyzer.md)** - How saturation analysis uses vLLM metrics
- **[Metrics & Health Monitoring](../metrics-health-monitoring.md)** - Monitoring WVA health and performance