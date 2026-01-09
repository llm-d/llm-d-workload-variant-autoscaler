# Configuration Guide

This guide explains how to configure Workload-Variant-Autoscaler for your workloads.

> **Important: Architecture Limitations**
> 
> WVA's performance model is designed and calibrated for **dense transformer architectures**. If you are using:
> - **Hybrid State Space Models (HSSM)**
> - **Mixture of Experts (MoE)** - e.g., Mixtral, DeepSeek MoE variants
> - **Other optimized/custom architectures**
> 
> Please read the [Architecture Limitations documentation](../design/architecture-limitations.md) to understand how these models may behave differently and what additional considerations are needed.

## Deployment Lifecycle Management

### Creating VariantAutoscaling Resources

WVA automatically handles the relationship between VariantAutoscaling (VA) resources and their target Deployments:

**Recommended Order:**
```bash
# 1. Create and verify the deployment is ready
kubectl apply -f deployment.yaml
kubectl wait --for=condition=available deployment/llama-8b --timeout=300s

# 2. Create the VariantAutoscaling resource
kubectl apply -f variantautoscaling.yaml
```

**Race Condition Protection:**

WVA handles the race condition where a VA is created before its target deployment exists. If you create a VA before its deployment:

1. VA is created with status indicating the deployment is not found
2. When the deployment is created, WVA automatically detects it
3. VA immediately begins monitoring and autoscaling (no wait for periodic reconciliation)

```yaml
# VA created first - will automatically detect deployment when it appears
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: llama-8b-autoscaler
spec:
  scaleTargetRef:
    name: llama-8b  # Deployment doesn't exist yet
  # ... other config
```

### Deployment Deletion Handling

When a target deployment is deleted, WVA immediately:

1. **Updates VA Status**: Marks the VA as not ready with reason `DeploymentNotFound`
2. **Clears Metrics**: Removes stale metrics to prevent incorrect autoscaling decisions
3. **Maintains VA Resource**: The VA itself is not deleted and will resume operation when deployment is recreated

**Example Status After Deployment Deletion:**

```yaml
status:
  conditions:
  - type: Ready
    status: "False"
    reason: "DeploymentNotFound"
    message: "Target deployment 'llama-8b' no longer exists"
  desiredOptimizedAlloc: {}  # Cleared to reflect no deployment
```

**Recovery Process:**

When the deployment is recreated, WVA automatically:
1. Detects the new deployment immediately (via Create event)
2. Updates VA status to Ready
3. Resumes monitoring and autoscaling

No manual intervention required!

### Best Practices

1. **Always specify scaleTargetRef explicitly** when the deployment name differs from the model ID:
   ```yaml
   spec:
     scaleTargetRef:
       name: my-custom-deployment-name
     modelId: "meta-llama/Llama-3.1-8B"
   ```

2. **Monitor VA status** to detect deployment issues:
   ```bash
   kubectl get va llama-8b-autoscaler -o jsonpath='{.status.conditions[?(@.type=="Ready")]}'
   ```

3. **Use consistent naming** - naming your deployment and VA with related names helps with operational clarity.

## VariantAutoscaling Resource

The `VariantAutoscaling` CR is the primary configuration interface for WVA.

### Basic Example

```yaml
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: llama-8b-autoscaler
  namespace: llm-inference
spec:
  modelName: "meta/llama-3.1-8b"
  serviceClass: "Premium"
  acceleratorType: "A100"
  minReplicas: 1
  maxBatchSize: 256
```

### Complete Reference

For complete field documentation, see the [CRD Reference](crd-reference.md).

## Operating Modes

WVA supports two operating modes controlled by the `EXPERIMENTAL_PROACTIVE_MODEL` environment variable.

### CAPACITY-ONLY Mode (Default)

**Recommended for production.**

- **Behavior**: Reactive scaling based on saturation detection
- **How It Works**: Monitors KV cache usage and queue lengths, scales when thresholds exceeded
- **Configuration**: Uses `capacity-scaling-config` ConfigMap
- **Pros**: Fast response (<30s), predictable, no model training needed
- **Cons**: Reactive (scales after saturation detected)

**Enable:**
```yaml
# Already enabled by default, no configuration needed
# Or explicitly set:
env:
  - name: EXPERIMENTAL_PROACTIVE_MODEL
    value: "false"
```

### HYBRID Mode (Experimental)

**Not recommended for production.**

- **Behavior**: Combines saturation analyzer with model-based optimizer
- **How It Works**:
  1. Runs saturation analyzer for saturation detection
  2. Runs model-based optimizer for proactive scaling
  3. Arbitrates between the two (capacity safety overrides)
- **Pros**: Proactive scaling (can scale before saturation)
- **Cons**: Slower (~60s), requires model training, experimental

**Enable:**
```yaml
env:
  - name: EXPERIMENTAL_PROACTIVE_MODEL
    value: "true"
```

**Recommendation:** Stick with CAPACITY-ONLY mode unless you have specific proactive scaling requirements.

See [Saturation Analyzer Documentation](../../docs/saturation-analyzer.md) for configuration details.

## ConfigMaps

WVA uses two ConfigMaps for cluster-wide configuration.

### Accelerator Unit Cost ConfigMap

Defines GPU pricing for cost optimization:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: accelerator-unitcost
  namespace: workload-variant-autoscaler-system
data:
  accelerators: |
    - name: A100
      type: NVIDIA-A100-PCIE-80GB
      cost: 40
      memSize: 81920
    - name: MI300X
      type: AMD-MI300X-192GB
      cost: 65
      memSize: 196608
    - name: H100
      type: NVIDIA-H100-80GB-HBM3
      cost: 80
      memSize: 81920
```

### Service Class ConfigMap

Defines SLO requirements for different service tiers:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: serviceclass
  namespace: workload-variant-autoscaler-system
data:
  serviceClasses: |
    - name: Premium
      model: meta/llama-3.1-8b
      priority: 1
      slo-itl: 24        # Time per output token (ms)
      slo-ttw: 500       # Time to first token (ms)
      
    - name: Standard
      model: meta/llama-3.1-8b
      priority: 5
      slo-itl: 50
      slo-ttw: 1000
      
    - name: Freemium
      model: meta/llama-3.1-8b
      priority: 10
      slo-itl: 100
      slo-ttw: 2000
```

## Configuration Options

### Model-Specific Settings

- **modelName**: Identifier for your model (e.g., "meta/llama-3.1-8b")
- **serviceClass**: Service tier (must match ConfigMap)
- **acceleratorType**: Preferred GPU type (e.g., "A100", "MI300X")

### Scaling Parameters

- **minReplicas**: Minimum number of replicas (default: 1)
- **maxBatchSize**: Maximum batch size for inference
- **keepAccelerator**: Pin to specific accelerator type (true/false)

### Cost Configuration

#### variantCost (Optional)

Specifies the cost per replica for this variant, used in saturation-based cost optimization.

```yaml
spec:
  modelID: "meta/llama-3.1-8b"
  variantCost: 15.5  # Cost per replica (default: 10.0)
  modelProfile:
    accelerators:
      - acc: "A100"
        accCount: 1
```

**Default:** 10.0
**Validation:** Must be >= 0

**Use Cases:**
- **Differentiated Pricing**: Higher cost for premium accelerators (H100) vs. standard (A100)
- **Multi-Tenant Cost Tracking**: Assign different costs per customer/tenant
- **Cost-Based Optimization**: Saturation analyzer prefers lower-cost variants when multiple variants can handle load

**Example:**
```yaml
# Premium variant (H100, higher cost)
spec:
  modelID: "meta/llama-3.1-70b"
  variantCost: 80.0
  modelProfile:
    accelerators:
      - acc: "H100"

# Standard variant (A100, lower cost)
spec:
  modelID: "meta/llama-3.1-70b"
  variantCost: 40.0
  modelProfile:
    accelerators:
      - acc: "A100"
```

**Behavior:**
- Saturation analyzer uses `variantCost` when deciding which variant to scale
- If costs are equal, chooses variant with most available capacity
- Does not affect model-based optimization (uses accelerator unit costs)

### Advanced Options

See [CRD Reference](crd-reference.md) for advanced configuration options.

## Best Practices

### Environment Variables

WVA supports configuration via environment variables for operational settings:

**Prometheus Configuration:**
- `PROMETHEUS_BASE_URL`: Prometheus server URL (required for metrics collection)
- `PROMETHEUS_TLS_INSECURE_SKIP_VERIFY`: Skip TLS verification (development only)
- `PROMETHEUS_CA_CERT_PATH`: CA certificate path for TLS
- `PROMETHEUS_CLIENT_CERT_PATH`: Client certificate for mutual TLS
- `PROMETHEUS_CLIENT_KEY_PATH`: Client key for mutual TLS
- `PROMETHEUS_SERVER_NAME`: Expected server name in TLS certificate
- `PROMETHEUS_BEARER_TOKEN`: Bearer token for authentication

**Other Configuration:**
- `CONFIG_MAP_NAME`: ConfigMap name (default: auto-generated from Helm release)
- `POD_NAMESPACE`: Controller namespace (auto-injected by Kubernetes)

See [Prometheus Integration](../integrations/prometheus.md) for detailed Prometheus configuration.

### Choosing Service Classes

- **Premium**: Latency-sensitive applications (chatbots, interactive AI)
- **Standard**: Moderate latency requirements (content generation)
- **Freemium**: Best-effort, cost-optimized (batch processing)

### Batch Size Tuning

Batch size affects throughput and latency performance:
- WVA **mirrors** the vLLM server's configured batch size (e.g., `--max-num-seqs`)
- Do not override `maxBatchSize` in VariantAutoscaling unless you also change the vLLM server configuration
- When tuning batch size, update **both** the vLLM server argument and the WVA VariantAutoscaling spec together
- Monitor SLO compliance after any batch size changes

## Monitoring Configuration

WVA exposes metrics for monitoring and integrates with HPA for automatic scaling.

### Safety Net Behavior

WVA includes a **safety net** that prevents HPA from using stale metrics during failures:

1. **Normal Operation**: Emits `inferno_desired_replicas` with optimized targets
2. **Capacity Analysis Fails**:
   - Uses previous desired replicas (from last successful run)
   - If unavailable, uses current replicas (safe no-op)
3. **Log Messages**: Watch for `"Safety net activated"` in controller logs

**Check Safety Net Activation:**
```bash
# Controller logs
kubectl logs -n llm-d-scheduler deployment/wva-controller | grep "Safety net activated"

# Should see:
# "Safety net activated: emitted fallback metrics"
#   variant=my-va
#   currentReplicas=2
#   desiredReplicas=2
#   fallbackSource=current-replicas
```

**Why This Matters:**
- Prevents HPA from scaling based on stale metrics
- Provides graceful degradation during Prometheus outages
- Emits safe no-op signals (current=desired) when no history available

### Prometheus Metrics

See:
- [Prometheus Integration](../integrations/prometheus.md)
- [Custom Metrics](../integrations/prometheus.md#custom-metrics)

## Examples

More configuration examples in:
- [config/samples/](../../config/samples/)
- [Tutorials](../tutorials/)

## Multi-Controller Environments

When running multiple WVA controller instances in the same cluster (e.g., for parallel testing, multi-tenant setups, or canary deployments), use the **controller instance isolation** feature to prevent metric conflicts and ensure proper VA resource management.

### Quick Example

```yaml
# Helm values for controller instance A
wva:
  controllerInstance: "instance-a"

---
# Helm values for controller instance B
wva:
  controllerInstance: "instance-b"
```

Each controller will:
- Only manage VAs with matching `wva.llmd.ai/controller-instance` label
- Emit metrics with `controller_instance` label
- Have HPAs that filter metrics by `controller_instance`

For complete documentation, see [Multi-Controller Isolation Guide](multi-controller-isolation.md).

## Troubleshooting Configuration

### Common Issues

**SLOs not being met:**
- Verify service class configuration matches workload
- Check if accelerator has sufficient capacity
- Review model parameter estimates (alpha, beta values)

**Cost too high:**
- Consider allowing accelerator flexibility (`keepAccelerator: false`)
- Review service class priorities
- Check if min replicas can be reduced

## Next Steps

- [Run the Quick Start Demo](../tutorials/demo.md)
- [Integrate with HPA](../integrations/hpa-integration.md)
- [Set up Prometheus monitoring](../integrations/prometheus.md)

