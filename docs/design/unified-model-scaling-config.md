# Unified Model Scaling Configuration

## Overview

The `model-scaling-config` ConfigMap provides a unified configuration format that combines:
- Saturation-based scaling thresholds (from `saturation-scaling-config`)
- Scale-to-zero settings (from `model-scale-to-zero-config`)

This simplifies configuration management by having a single ConfigMap for all model-specific scaling behavior.

## ConfigMap Structure

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scaling-config
  namespace: <controller-namespace>
data:
  # Global defaults applied to all models
  default: |
    kvCacheThreshold: 0.80
    queueLengthThreshold: 5
    kvSpareTrigger: 0.1
    queueSpareTrigger: 3
    enableScaleToZero: false
    scaleToZeroRetentionPeriod: "10m"

  # Per-model override (requires model_id field)
  my-model-override: |
    model_id: "meta/llama-70b"
    namespace: "production"
    kvCacheThreshold: 0.75
    enableScaleToZero: true
    scaleToZeroRetentionPeriod: "5m"
```

## Configuration Fields

### Saturation Thresholds

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `kvCacheThreshold` | float (0.0-1.0) | 0.80 | Replica is saturated if KV cache utilization >= threshold |
| `queueLengthThreshold` | float | 5.0 | Replica is saturated if queue length >= threshold |
| `kvSpareTrigger` | float (0.0-1.0) | 0.1 | Scale-up signal if average spare KV capacity < trigger |
| `queueSpareTrigger` | float | 3.0 | Scale-up signal if average spare queue capacity < trigger |

### Scale-to-Zero Settings

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enableScaleToZero` | boolean | false | Allow scaling to zero replicas when idle |
| `scaleToZeroRetentionPeriod` | duration string | "10m" | Time to wait after last request before scaling to zero (e.g., "5m", "1h") |

### Override-specific Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `model_id` | string | Yes (for overrides) | Model identifier to match |
| `namespace` | string | No | Namespace filter (optional) |

## Configuration Priority

1. **Per-model override** - Highest priority, matches by `model_id`
2. **Global defaults** - Under the `default` key
3. **System defaults** - Built-in fallback values

## Helm Chart Configuration

In `values.yaml`:

```yaml
wva:
  modelScaling:
    default:
      kvCacheThreshold: 0.80
      queueLengthThreshold: 5
      kvSpareTrigger: 0.1
      queueSpareTrigger: 3
      # enableScaleToZero: false
      # scaleToZeroRetentionPeriod: "10m"

    overrides:
      granite-model:
        modelID: "ibm/granite-13b"
        namespace: "production"
        kvCacheThreshold: 0.75
        enableScaleToZero: true
        scaleToZeroRetentionPeriod: "5m"
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MODEL_SCALING_CONFIG_MAP_NAME` | `model-scaling-config` | Name of the unified ConfigMap |

## Example: Complete Configuration

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-scaling-config
  namespace: wva-system
data:
  default: |
    kvCacheThreshold: 0.80
    queueLengthThreshold: 5
    kvSpareTrigger: 0.1
    queueSpareTrigger: 3
    enableScaleToZero: false
    scaleToZeroRetentionPeriod: "10m"

  llama-prod: |
    model_id: "meta/llama-70b"
    namespace: "production"
    kvCacheThreshold: 0.85
    queueLengthThreshold: 10
    enableScaleToZero: true
    scaleToZeroRetentionPeriod: "15m"

  granite-dev: |
    model_id: "ibm/granite-13b"
    namespace: "development"
    kvCacheThreshold: 0.70
    kvSpareTrigger: 0.2
    enableScaleToZero: true
    scaleToZeroRetentionPeriod: "2m"
```
