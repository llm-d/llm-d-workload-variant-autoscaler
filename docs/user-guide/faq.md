# Frequently Asked Questions (FAQ)

## General Questions

### What is Workload-Variant-Autoscaler (WVA)?

WVA is a Kubernetes controller that performs intelligent autoscaling for inference model servers based on saturation. It determines optimal replica counts for given request traffic loads, helping you minimize infrastructure costs while meeting SLO requirements.

### How is WVA different from HPA or KEDA?

WVA focuses specifically on inference workloads and uses saturation-based scaling (KV cache utilization, queue depth) rather than generic CPU/memory metrics. WVA works **with** HPA or KEDA by emitting custom metrics that these autoscalers consume for scaling decisions.

### What inference servers does WVA support?

Currently, WVA is optimized for vLLM-based inference servers. Support for other inference frameworks may be added in the future.

### Do I need physical GPUs to try WVA?

No! WVA includes a Kind-based emulator that works on Mac (Apple Silicon/Intel) and Windows without physical GPUs. See the [Local Development guide](../deploy/kind-emulator/README.md).

## Installation & Setup

### What Kubernetes version do I need?

- Kubernetes v1.31.0+ (or OpenShift 4.18+)
- Helm 3.x
- kubectl

### Can I run multiple WVA controller instances?

Yes! WVA supports multi-controller isolation. See the [Multi-Controller Isolation guide](multi-controller-isolation.md) for details.

### Do I need Prometheus?

Yes, WVA requires Prometheus to:
- Collect metrics from vLLM servers
- Emit custom metrics for HPA/KEDA
- Monitor controller health

See the [Prometheus Integration guide](../integrations/prometheus.md).

### How do I upgrade WVA?

**Important:** Helm does not automatically update CRDs. Before upgrading:

```bash
# Apply the latest CRDs first
kubectl apply -f charts/workload-variant-autoscaler/crds/

# Then upgrade the Helm release
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  [your-values...]
```

See the [Upgrading section](../../README.md#upgrading) in the main README.

## Configuration & Usage

### When should I create the VariantAutoscaling CR?

WVA handles creation order gracefully:
- You can create the VA before or after the deployment
- If a deployment is deleted, VA status is immediately updated
- When the deployment is recreated, VA automatically resumes operation

### What is the `scaleTargetRef` field?

Added in v0.5.0, `scaleTargetRef` explicitly specifies the target deployment to scale. If not set, the controller infers the target from the `modelID` field.

```yaml
spec:
  scaleTargetRef:
    kind: Deployment
    name: llama-8b
```

### How do I configure saturation thresholds?

Saturation thresholds are configured in the WVA ConfigMap. See the [Configuration guide](configuration.md) for details on:
- KV cache utilization thresholds
- Queue depth thresholds
- Scale-up/scale-down behavior

### What is `variantCost`?

The `variantCost` field (optional, defaults to "10.0") is used by the optimizer to make cost-aware scaling decisions. Higher values indicate more expensive infrastructure.

### How often does WVA reconcile?

WVA continuously monitors request rates and server performance. The reconciliation frequency depends on:
- Metric scraping interval (default: 15s)
- Changes to VariantAutoscaling resources
- Deployment status changes

## Scaling Behavior

### Why isn't my deployment scaling?

Common causes:
1. **HPA/KEDA not configured** - WVA emits metrics, but you need HPA or KEDA to actually scale
2. **Metrics not available** - Check Prometheus has vLLM server metrics
3. **Deployment not ready** - Ensure pods are running and healthy
4. **Stabilization window** - HPA may be delaying scale operations (recommend 120s+)

See the [Troubleshooting guide](troubleshooting.md) for diagnosis steps.

### Can WVA scale to zero?

Yes! WVA supports scale-to-zero behavior. Configure it in the WVA ConfigMap:

```yaml
scaleToZero:
  enabled: true
  idleTimeSeconds: 300
```

See the [Scale to Zero sample](../../config/samples/model-scale-to-zero-config.yaml).

### What is the recommended HPA stabilization window?

We recommend a stabilization window of **120 seconds or more** for gradual scaling behavior and to avoid thrashing during traffic spikes.

## Architecture & Limitations

### What model architectures are supported?

WVA is optimized for standard decoder-only transformer architectures. **Important limitations:**
- Hybrid State Space Models (HSSM) like Mamba
- Mixture-of-Experts (MoE) architectures
- Non-standard architectures

**Read the [Architecture Limitations guide](../design/architecture-limitations.md) if using these architectures.**

### How does WVA determine optimal replica counts?

WVA uses a saturation-based capacity model:
1. Monitors KV cache utilization and queue depth
2. Identifies servers with slack capacity
3. Calculates optimal replicas based on thresholds
4. Emits metrics to Prometheus
5. External autoscaler (HPA/KEDA) scales the deployment

See the [Controller Behavior guide](../design/controller-behavior.md) for details.

### Does WVA support multi-model deployments?

Each VariantAutoscaling resource manages a single deployment. For multiple models, create multiple VA resources. WVA supports multi-controller isolation for namespace-level or cluster-wide operation.

## Monitoring & Debugging

### What metrics does WVA emit?

WVA emits several metric families:
- `wva_desired_replicas` - Recommended replica count
- `wva_current_replicas` - Current replica count
- `wva_saturation_*` - Saturation metrics (KV cache, queue depth)
- `wva_controller_*` - Controller health metrics

See the [Prometheus Integration guide](../integrations/prometheus.md) for the full list.

### How do I debug WVA issues?

1. **Check controller logs:**
   ```bash
   kubectl logs -n workload-variant-autoscaler-system \
     deployment/workload-variant-autoscaler-controller-manager
   ```

2. **Check VA status:**
   ```bash
   kubectl get variantautoscaling -A -o yaml
   ```

3. **Verify metrics in Prometheus:**
   ```promql
   wva_desired_replicas{namespace="your-namespace"}
   ```

See the [Debugging guide](../developer-guide/debugging.md) for advanced techniques.

### Where can I find logs?

- **Controller logs:** `kubectl logs -n workload-variant-autoscaler-system deployment/workload-variant-autoscaler-controller-manager`
- **vLLM server logs:** `kubectl logs -n <namespace> <vllm-pod-name>`
- **HPA logs:** `kubectl describe hpa <hpa-name> -n <namespace>`

## Contributing & Community

### How can I contribute?

See the [Contributing guide](../../CONTRIBUTING.md) for:
- Code contributions
- Documentation improvements
- Issue reporting
- Community meetings

### Where can I get help?

- **Documentation:** Start with the [User Guide](../README.md)
- **Issues:** Open a [GitHub Issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- **Community:** Join [llm-d autoscaling community meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)

### Is WVA production-ready?

WVA is under active development. While it is being used in several environments, we recommend:
- Thorough testing in your environment
- Starting with non-critical workloads
- Monitoring closely during initial deployment
- Reading the [Architecture Limitations](../design/architecture-limitations.md)

---

**Still have questions?** Open an [issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues) or check the [Troubleshooting guide](troubleshooting.md).
