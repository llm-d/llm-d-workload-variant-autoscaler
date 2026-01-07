# Frequently Asked Questions (FAQ)

## General

### What is Workload-Variant-Autoscaler (WVA)?

WVA is a Kubernetes controller that performs intelligent autoscaling for inference model servers based on saturation metrics. It monitors KV cache utilization and queue depth to determine optimal replica counts for serving LLM inference workloads.

### How is WVA different from HPA or KEDA?

WVA complements HPA/KEDA by providing intelligent capacity analysis:

- **WVA**: Analyzes saturation metrics (KV cache, queue depth) and emits custom metrics indicating optimal replica count
- **HPA/KEDA**: Consumes WVA metrics and performs the actual pod scaling

Think of WVA as the "brain" that decides the target replica count, while HPA/KEDA acts as the "executor" that makes it happen.

See [HPA Integration](../integrations/hpa-integration.md) for details.

### What metrics does WVA use?

WVA monitors vLLM-specific metrics via Prometheus:

- `vllm:gpu_cache_usage_perc` - KV cache utilization percentage
- `vllm:num_requests_waiting` - Number of requests in queue
- Request rates and token statistics

See [Prometheus Integration](../integrations/prometheus.md) for the complete metrics list.

### Does WVA require GPU hardware?

No! For development and testing, you can use the Kind emulator which simulates GPU environments without physical hardware. This works on Mac (Apple Silicon/Intel) and Windows.

```bash
make deploy-llm-d-wva-emulated-on-kind
```

For production, WVA works with any Kubernetes cluster running vLLM inference servers with GPU support.

## Installation & Setup

### What are the prerequisites for installing WVA?

- Kubernetes v1.31.0+ or OpenShift 4.18+
- Helm 3.x
- Prometheus with ServiceMonitor support
- vLLM inference servers (v0.6.0+)

See [Installation Guide](installation.md) for detailed requirements.

### Can I install WVA without Prometheus?

No, Prometheus is required. WVA relies on Prometheus metrics to analyze server saturation and make scaling decisions. However, the Helm chart can deploy Prometheus for you if needed.

### How do I upgrade WVA?

**Important**: Helm does not automatically upgrade CRDs. Always apply CRD updates manually first:

```bash
# 1. Apply updated CRDs
kubectl apply -f charts/workload-variant-autoscaler/crds/

# 2. Upgrade Helm release
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system
```

See the [Upgrading section](../../README.md#upgrading) in the main README.

### What namespace should I install WVA in?

The default namespace is `workload-variant-autoscaler-system`. You can choose any namespace, but ensure:

1. The namespace exists before installation
2. ServiceMonitors are configured for your monitoring namespace
3. RBAC permissions are set correctly across namespaces

## Configuration

### When should I create the VariantAutoscaling CR?

You can create the VariantAutoscaling CR before or after deploying your inference server:

- **Before deployment**: WVA detects when the deployment is created and begins monitoring
- **After deployment**: WVA immediately begins analyzing existing replicas

The controller handles both scenarios gracefully.

### What happens if I delete the deployment?

WVA immediately updates the VariantAutoscaling status to reflect the missing deployment. When you recreate the deployment, WVA automatically resumes operation without manual intervention.

### How do I configure saturation thresholds?

Edit the `capacity-scaling-config` ConfigMap in the controller namespace:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: capacity-scaling-config
  namespace: workload-variant-autoscaler-system
data:
  config.yaml: |
    default:
      kvCacheThreshold: 0.80
      queueLengthThreshold: 5
      kvSpareTrigger: 0.10
      queueSpareTrigger: 3
```

Changes take effect immediately via ConfigMap watch. See [Saturation Scaling Configuration](../saturation-scaling-config.md).

### Should I use the same thresholds for WVA and End Point Picker (EPP)?

**Yes, strongly recommended**. Using aligned thresholds between WVA and InferenceScheduler's End Point Picker ensures:

- Reduced request drop rates
- Consistent capacity management
- Better coordination between routing and scaling decisions

See [Best Practices section](../saturation-scaling-config.md#best-practices-coordinating-with-inferencescheduler-end-point-picker) for details.

## Troubleshooting

### My VariantAutoscaling status shows "DeploymentNotFound"

This means the controller cannot find the target deployment. Check:

1. **Deployment exists**: `kubectl get deployment <name> -n <namespace>`
2. **ScaleTargetRef is correct**: Verify the name and namespace in your VariantAutoscaling CR
3. **RBAC permissions**: Ensure the controller has permission to read deployments

```bash
kubectl get variantautoscaling <name> -n <namespace> -o yaml
```

### Why isn't WVA scaling my deployment?

WVA emits metrics for HPA/KEDA to consume - it doesn't scale directly. Check:

1. **HPA/KEDA is configured**: Verify your HPA or KEDA ScaledObject exists
2. **Metrics are available**: Check if WVA metrics are being scraped by Prometheus
3. **HPA can read metrics**: Ensure metrics-server or custom metrics API is configured

```bash
# Check WVA metrics
kubectl get --raw /apis/external.metrics.k8s.io/v1beta1 | grep variantautoscaling

# Check HPA status
kubectl get hpa <name> -n <namespace> -o yaml
```

See [HPA Integration troubleshooting](../integrations/hpa-integration.md#troubleshooting).

### WVA shows high CPU/memory usage

Common causes:

1. **High reconciliation frequency**: Default is 60s. Increase if monitoring many variants
2. **Large number of variants**: Each variant requires metrics queries
3. **Prometheus queries timing out**: Check Prometheus performance

To adjust reconciliation interval:

```yaml
# In Helm values.yaml
controller:
  reconciliationInterval: 120s
```

### How do I enable debug logging?

Set the log level in the controller deployment:

```bash
# Via Helm
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --set controller.logLevel=debug

# Or edit directly
kubectl edit deployment workload-variant-autoscaler-controller-manager \
  -n workload-variant-autoscaler-system
```

Change the `--zap-log-level` flag to `debug` or `2`.

### Metrics are not appearing in Prometheus

Check:

1. **ServiceMonitor is created**: `kubectl get servicemonitor -n <namespace>`
2. **Prometheus is scraping**: Check Prometheus targets UI
3. **vLLM metrics endpoint**: Verify vLLM pods expose metrics at `:8000/metrics`

```bash
# Test vLLM metrics endpoint
kubectl port-forward <vllm-pod> 8000:8000
curl http://localhost:8000/metrics | grep vllm
```

## Performance & Scaling

### How many variants can WVA manage?

WVA is designed to handle multiple variants efficiently. Performance depends on:

- Number of variants (VariantAutoscaling CRs)
- Number of replicas per variant
- Prometheus query performance
- Reconciliation interval

Tested with up to 50 variants with 20 replicas each without issues.

### What is the recommended HPA stabilization window?

We recommend at least **120 seconds** for the HPA stabilization window to prevent scaling thrashing:

```yaml
behavior:
  scaleDown:
    stabilizationWindowSeconds: 120
  scaleUp:
    stabilizationWindowSeconds: 60
```

This gives WVA metrics time to stabilize after scaling events.

### Can I run multiple WVA controllers?

Yes, for multi-tenant scenarios. Use the `selector` field to isolate controller instances:

```yaml
spec:
  selector:
    matchLabels:
      team: data-science
```

See [Multi-Controller Isolation](multi-controller-isolation.md) for details.

## Integration & Compatibility

### Which vLLM versions are supported?

WVA works with vLLM v0.6.0 and later. Earlier versions may have incomplete metrics support.

### Does WVA work with other inference servers besides vLLM?

Currently, WVA is optimized for vLLM metrics. Support for other inference servers (TensorRT-LLM, TGI) depends on their metric format compatibility. Community contributions are welcome!

### Can I use WVA with Istio/service mesh?

Yes, but ensure:

1. Prometheus can scrape metrics through the mesh
2. ServiceMonitor configuration includes proper Istio annotations
3. mTLS is configured if required

### Does WVA work on OpenShift?

Yes! WVA includes OpenShift-specific configuration. See [OpenShift Deployment Guide](../../deploy/openshift/README.md).

## Advanced Topics

### What is "unlimited mode" vs "limited mode"?

- **Unlimited mode** (current): Each variant gets optimal allocation independently. If cluster capacity is exceeded, pods remain Pending for cluster autoscaler.
- **Limited mode** (future): Respects cluster capacity constraints with resource accounting and priority-based allocation.

See [Modeling & Optimization](../design/modeling-optimization.md#current-mode-unlimited) for details.

### How does the saturation analyzer work?

The saturation analyzer uses a two-step decision architecture:

1. **Step 1**: Calculate capacity targets based on pure saturation metrics
2. **Step 2**: Arbitrate with model-based optimizer (optional hybrid mode)

See [Saturation Analyzer](../saturation-analyzer.md) for the complete architecture.

### Can I customize the queueing model parameters?

Yes, but it requires code changes. The queueing model parameters are configured in the controller. For offline benchmarking and parameter estimation, see [Parameter Estimation Tutorial](../tutorials/parameter-estimation.md).

### What are the architecture limitations?

WVA makes assumptions about model architectures. Read [Architecture Limitations](../design/architecture-limitations.md) if you're using:

- Hierarchical State Space Models (HSSM)
- Mixture of Experts (MoE) models
- Non-standard architectures

## Contributing

### How can I contribute to WVA?

See the [Contributing Guide](../../CONTRIBUTING.md) and [Developer Guide](../developer-guide/development.md) to get started.

### Where can I report bugs or request features?

- **Bugs**: Open a [GitHub Issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- **Features**: Start a [Discussion](https://github.com/llm-d-incubation/workload-variant-autoscaler/discussions)
- **Questions**: Join community meetings (see README)

### Is there a roadmap?

Current priorities:

1. Limited mode support with capacity constraints
2. Additional inference server support (TRT-LLM, TGI)
3. Enhanced cost optimization algorithms
4. Multi-cluster support

Check the [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues) for detailed roadmap items.

## Getting Help

### Where can I get help?

1. Check this FAQ first
2. Review the [documentation](../README.md)
3. Search [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
4. Join [community meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)
5. Open a [new issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues/new)

### How do I stay updated on WVA releases?

- Watch the [GitHub repository](https://github.com/llm-d-incubation/workload-variant-autoscaler)
- Check the [Releases page](https://github.com/llm-d-incubation/workload-variant-autoscaler/releases)
- Subscribe to release notifications

---

**Still have questions?** Open an issue or join our community meetings!
