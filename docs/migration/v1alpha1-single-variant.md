# Migration Guide: Multi-Variant to Single-Variant Architecture

## Overview

This guide helps you migrate from the **multi-variant** CRD architecture (pre-v0.7.0) to the **single-variant** architecture (v0.7.0+).

**Breaking Change:** The VariantAutoscaling CRD has been refactored from a multi-variant to a single-variant architecture. Each VariantAutoscaling resource now manages exactly one model variant instead of multiple variants in an array.

## Why This Change?

The single-variant architecture provides:
- **Clearer ownership**: 1:1 mapping between VariantAutoscaling resource and target Deployment
- **Simpler spec structure**: Direct fields instead of nested arrays
- **Better alignment**: Follows Kubernetes patterns (similar to HPA)
- **Easier maintenance**: Simpler status management and controller logic

## Quick Migration Checklist

- [ ] Backup existing VariantAutoscaling resources
- [ ] Update CRD to latest version
- [ ] Transform multi-variant resources to single-variant format
- [ ] Apply new resources
- [ ] Verify metrics and optimization
- [ ] Update any HPA/KEDA configurations if needed

## Breaking Changes Summary

### VariantAutoscalingSpec Changes

| Old (Multi-Variant) | New (Single-Variant) | Notes |
|---------------------|----------------------|-------|
| `spec.variants[]` array | Removed | Create separate resources instead |
| N/A | `spec.scaleTargetRef` | New: References target Deployment |
| N/A | `spec.variantID` | New: Business identifier |
| N/A | `spec.accelerator` | Moved from `variants[].acc` |
| N/A | `spec.acceleratorCount` | Moved from `variants[].accCount` |
| `spec.modelProfile` | `spec.variantProfile` | Renamed |
| `spec.modelProfile.accelerators[]` | `spec.variantProfile.perfParms` | Simplified to single variant |
| N/A | `spec.variantCost` | New: Optional cost specification |

### VariantAutoscalingStatus Changes

| Old (Multi-Variant) | New (Single-Variant) | Notes |
|---------------------|----------------------|-------|
| `status.allocations[]` array | `status.currentAlloc` | Single allocation object |
| `status.desiredOptimizedAlloc[]` array | `status.desiredOptimizedAlloc` | Single optimized allocation |
| `status.allocations[].load` | Removed | Collected on-demand, not stored |
| `status.allocations[].itlAverage` | Removed | Collected on-demand, not stored |
| `status.allocations[].ttftAverage` | Removed | Collected on-demand, not stored |

## Migration Steps

### Step 1: Backup Existing Resources

```bash
# Backup all VariantAutoscaling resources
kubectl get variantautoscaling -A -o yaml > variantautoscaling-backup.yaml

# Optional: Export to individual files per namespace
for ns in $(kubectl get variantautoscaling -A -o jsonpath='{.items[*].metadata.namespace}' | tr ' ' '\n' | sort -u); do
  kubectl get variantautoscaling -n $ns -o yaml > variantautoscaling-backup-$ns.yaml
done
```

### Step 2: Update CRD

The CRD will be automatically updated when you upgrade the workload-variant-autoscaler. If you're using Helm:

```bash
# Upgrade WVA with new CRD
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  --reuse-values
```

Or manually apply the new CRD:

```bash
kubectl apply -f config/crd/bases/llmd.ai_variantautoscalings.yaml
```

### Step 3: Delete Old Resources

```bash
# Delete old multi-variant resources (they're backed up)
kubectl delete variantautoscaling --all -A
```

### Step 4: Transform and Apply New Resources

#### Example Transformation

**Before (Multi-Variant):**
```yaml
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: vllme-deployment
  namespace: llm-d-sim
  labels:
    inference.optimization/acceleratorName: A100
spec:
  modelID: default/default
  sloClassRef:
    name: premium
    key: opt-125m
  modelProfile:
    accelerators:
      - acc: "A100"
        accCount: 1
        perfParms:
          decodeParms:
            alpha: "20.58"
            beta: "0.41"
          prefillParms:
            gamma: "5.2"
            delta: "0.1"
        maxBatchSize: 4
      - acc: "L40S"
        accCount: 2
        perfParms:
          decodeParms:
            alpha: "18.42"
            beta: "0.38"
          prefillParms:
            gamma: "4.8"
            delta: "0.09"
        maxBatchSize: 8
```

**After (Single-Variant) - Create TWO resources:**

Resource 1 (A100 variant):
```yaml
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: vllme-deployment-a100
  namespace: llm-d-sim
  labels:
    inference.optimization/acceleratorName: A100
spec:
  # Reference to the target Deployment
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: vllme-deployment-a100

  # Model identifier
  modelID: default/default

  # Business identifier: {modelID}-{accelerator}-{acceleratorCount}
  variantID: "default/default-A100-1"

  # Accelerator type and count
  accelerator: "A100"
  acceleratorCount: 1

  # Optional: Cost per replica (defaults to "10")
  variantCost: "40.00"

  # SLO configuration reference
  sloClassRef:
    name: premium
    key: opt-125m

  # Performance profile for this specific variant
  variantProfile:
    perfParms:
      decodeParms:
        alpha: "20.58"
        beta: "0.41"
      prefillParms:
        gamma: "5.2"
        delta: "0.1"
    maxBatchSize: 4
```

Resource 2 (L40S variant):
```yaml
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: vllme-deployment-l40s
  namespace: llm-d-sim
  labels:
    inference.optimization/acceleratorName: L40S
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: vllme-deployment-l40s

  modelID: default/default
  variantID: "default/default-L40S-2"

  accelerator: "L40S"
  acceleratorCount: 2

  variantCost: "25.00"

  sloClassRef:
    name: premium
    key: opt-125m

  variantProfile:
    perfParms:
      decodeParms:
        alpha: "18.42"
        beta: "0.38"
      prefillParms:
        gamma: "4.8"
        delta: "0.09"
    maxBatchSize: 8
```

### Step 5: Apply New Resources

```bash
# Apply the new single-variant resources
kubectl apply -f vllme-deployment-a100.yaml
kubectl apply -f vllme-deployment-l40s.yaml
```

### Step 6: Verify Migration

```bash
# Check VariantAutoscaling resources
kubectl get variantautoscaling -A

# Expected output:
# NAMESPACE    NAME                      MODEL             VARIANTID                ACCELERATOR   CURRENTREPLICAS   OPTIMIZED
# llm-d-sim    vllme-deployment-a100     default/default   default/default-A100-1   A100          2                 2
# llm-d-sim    vllme-deployment-l40s     default/default   default/default-L40S-2   L40S          1                 1

# Verify metrics are being emitted
kubectl logs -n workload-variant-autoscaler-system deploy/workload-variant-autoscaler-controller-manager | grep "EmitReplicaMetrics"

# Check Prometheus metrics (if using HPA/KEDA integration)
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/llm-d-sim/inferno_desired_replicas"
```

## Updating HPA/KEDA Configurations

If you're using HPA or KEDA with the old multi-variant architecture, you'll need to update the label selectors:

**Before:**
```yaml
metrics:
  - type: External
    external:
      metric:
        name: inferno_desired_replicas
        selector:
          matchLabels:
            variant_name: vllme-deployment
```

**After:**
```yaml
metrics:
  - type: External
    external:
      metric:
        name: inferno_desired_replicas
        selector:
          matchLabels:
            variant_name: vllme-deployment-a100  # Updated to match new resource name
```

## Rollback Procedure

If you need to rollback:

```bash
# 1. Delete new single-variant resources
kubectl delete variantautoscaling --all -A

# 2. Downgrade WVA to previous version
helm rollback workload-variant-autoscaler -n workload-variant-autoscaler-system

# 3. Restore old resources from backup
kubectl apply -f variantautoscaling-backup.yaml
```

## Common Migration Scenarios

### Scenario 1: Single Variant Already

If you already had only one variant in the array:
1. Extract the single variant from `spec.variants[0]`
2. Move fields to top-level `spec`
3. Add `scaleTargetRef` pointing to your Deployment
4. Set `variantID` following the format: `{modelID}-{accelerator}-{acceleratorCount}`

### Scenario 2: Multiple Variants per Model

If you had multiple variants (different accelerators) for the same model:
1. Create one VariantAutoscaling resource per variant
2. Each should have a unique name (e.g., `{deployment-name}-{accelerator}`)
3. Each references its own Deployment via `scaleTargetRef`
4. Keep the same `modelID` for all variants of the same model

## Validation

The new CRD includes enhanced validation:

1. **VariantID Format**: Must match pattern `^.+-[A-Za-z0-9_-]+-[1-9][0-9]*$` (ends with `-{accelerator}-{acceleratorCount}`)
2. **Required Fields**: `scaleTargetRef`, `modelID`, `variantID`, `accelerator`, `acceleratorCount`
3. **MaxLength Constraints**: `modelID` (128), `variantID` (256), `accelerator` (64)

## Troubleshooting

### Issue: "variantID doesn't match required pattern"

**Cause:** Pattern validation enforces that `variantID` ends with `-{accelerator}-{acceleratorCount}`.

**Solution:** Ensure `variantID` follows the format: `{modelID}-{accelerator}-{acceleratorCount}`

Example:
```yaml
modelID: "meta/llama-3.1-8b"
accelerator: "A100"
acceleratorCount: 4
variantID: "meta/llama-3.1-8b-A100-4"  # Correct format
```

### Issue: Metrics not showing up

**Cause:** Resource name changed, affecting Prometheus label `variant_name`.

**Solution:** Update HPA/KEDA metric selectors to use the new resource name:
```yaml
selector:
  matchLabels:
    variant_name: vllme-deployment-a100  # Updated name
```

## Additional Resources

- [CRD Reference](../user-guide/crd-reference.md)
- [Configuration Guide](../user-guide/configuration.md)
- [HPA Integration](../integrations/hpa-integration.md)
- [KEDA Integration](../integrations/keda-integration.md)

## Support

If you encounter issues during migration:
1. Check the [troubleshooting section](#troubleshooting) above
2. Review the [CRD Reference](../user-guide/crd-reference.md) for field details
3. Open an issue at https://github.com/llm-d-incubation/workload-variant-autoscaler/issues
