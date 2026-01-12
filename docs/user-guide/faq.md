# Frequently Asked Questions (FAQ)

## General

### What is the Workload-Variant-Autoscaler (WVA)?

WVA is a Kubernetes controller that performs intelligent autoscaling for inference model servers based on saturation metrics. It monitors request traffic patterns and server performance to optimize replica counts while maintaining service level objectives (SLOs).

### How is WVA different from Horizontal Pod Autoscaler (HPA)?

WVA provides model-aware autoscaling specifically designed for inference workloads:

- **Saturation-based scaling**: Monitors KV cache utilization and request queue depth
- **Model-specific metrics**: Uses vLLM metrics like TTFT, ITL, and token generation rates
- **Cost optimization**: Considers variant costs when making scaling decisions
- **Prevents capacity exhaustion**: Proactively scales before OOM errors occur

HPA works well for general workloads but lacks awareness of inference server internals. WVA complements HPA by providing the scaling metrics, while HPA handles the actual scaling operations.

### Which inference servers does WVA support?

Currently, WVA is optimized for **vLLM** servers. The controller collects vLLM-specific metrics from Prometheus to make scaling decisions.

Support for additional inference servers (e.g., TensorRT-LLM, Text Generation Inference) is planned for future releases.

## Installation & Setup

### What are the minimum Kubernetes version requirements?

- **Kubernetes**: v1.31.0 or higher
- **OpenShift**: 4.18 or higher

### Do I need GPUs to try WVA?

No! You can use the Kind emulator for local development and testing:

```bash
make deploy-llm-d-wva-emulated-on-kind
```

This creates a local Kind cluster with emulated GPUs, perfect for development on Mac, Windows, or Linux without physical GPUs.

### Can I use WVA in production without Prometheus?

No. Prometheus is a **required dependency** for WVA. The controller queries Prometheus to collect vLLM metrics and emit scaling decisions. See the [Prometheus Integration Guide](../integrations/prometheus.md) for setup instructions.

### How do I configure Prometheus connectivity?

Set the `PROMETHEUS_BASE_URL` environment variable or ConfigMap entry:

```yaml
env:
- name: PROMETHEUS_BASE_URL
  value: "https://prometheus-k8s.monitoring.svc.cluster.local:9091"
```

For production deployments, configure TLS certificates. See [Prometheus Integration](../integrations/prometheus.md) for security best practices.

## Configuration

### When should I create the VariantAutoscaling CR?

You can create the `VariantAutoscaling` CR **before or after** your deployment is ready. WVA handles creation order gracefully:

- If deployment exists: Autoscaling starts immediately
- If deployment is missing: Status reflects missing deployment, resumes when deployment is created

### What does the `variantCost` field do?

The `variantCost` field (default: `"10.0"`) represents the relative cost per replica for this variant. When managing multiple variants of the same model on different accelerators, WVA uses cost to optimize scaling decisions:

```yaml
spec:
  variantCost: "80.0"  # H100 variant - higher cost
```

Lower-cost variants are preferred when both can handle the load.

### How do I configure the HPA stabilization window?

Set the `behavior` section in your HPA configuration:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
spec:
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 120  # Recommended: 120+ seconds
```

This prevents rapid scaling oscillations. See [HPA Integration](../integrations/hpa-integration.md) for complete configuration.

### Can I scale multiple deployments with one VariantAutoscaling?

No. Each `VariantAutoscaling` CR manages exactly one deployment, specified in `spec.scaleTargetRef`:

```yaml
spec:
  scaleTargetRef:
    kind: Deployment
    name: my-inference-deployment
```

For multiple deployments, create one `VariantAutoscaling` CR per deployment.

## Metrics & Monitoring

### How do I check if metrics are available?

Use the `MetricsAvailable` condition:

```bash
kubectl get variantautoscaling -A

# Check detailed status
kubectl describe variantautoscaling <name> -n <namespace>
```

If `MetricsAvailable` is `False`, check:
1. ServiceMonitor is configured for vLLM pods
2. Prometheus is scraping metrics successfully
3. vLLM server is running and healthy

See [Metrics Health Monitoring](../metrics-health-monitoring.md) for troubleshooting.

### What metrics does WVA expose?

WVA exposes four custom metrics for monitoring and alerting:

- `inferno_desired_replicas`: Target replica count from optimization
- `inferno_current_replicas`: Current replica count
- `inferno_desired_ratio`: Ratio of desired to current replicas (for HPA/KEDA)
- `inferno_replica_scaling_total`: Counter of scaling operations

See [Custom Metrics Reference](../integrations/custom-metrics.md) for details.

### Why are my metrics showing as stale?

Metrics are considered stale if they haven't updated in over 5 minutes. Common causes:

- vLLM server stopped emitting metrics
- Prometheus stopped scraping
- Network connectivity issues
- ServiceMonitor misconfigured

Check Prometheus targets status:

```bash
# Port-forward to Prometheus
kubectl port-forward -n monitoring svc/prometheus-k8s 9090:9090

# Visit http://localhost:9090/targets
```

## Scaling Behavior

### Why isn't WVA scaling my deployment?

WVA doesn't scale deployments directly. It emits metrics that HPA or KEDA uses for scaling. Check:

1. **Metrics available**: `kubectl describe variantautoscaling <name>`
2. **HPA/KEDA configured**: `kubectl get hpa` or `kubectl get scaledobject`
3. **HPA reading metrics**: `kubectl describe hpa <name>` - check "Metrics" section

### How often does WVA recalculate scaling decisions?

WVA reconciles on a polling interval (default: configurable via controller configuration). Each reconciliation:

1. Queries Prometheus for latest vLLM metrics
2. Runs saturation analysis
3. Calculates optimal replica count
4. Emits metrics to Prometheus
5. Updates `VariantAutoscaling` status

### What is "cascade scaling" and how does WVA prevent it?

Cascade scaling occurs when autoscalers continuously add replicas while waiting for pending pods to start, causing over-provisioning. WVA prevents this by:

- Only considering **ready replicas** (those reporting metrics) in calculations
- Blocking scale-up when replicas are pending
- Using worst-case safety simulations before scale-down

### Can I disable autoscaling temporarily?

Yes, delete or scale down the HPA/KEDA ScaledObject. The `VariantAutoscaling` CR will continue calculating optimal replicas, but no scaling actions occur without HPA/KEDA.

## Multi-Controller & Advanced

### Can I run multiple WVA controller instances?

Yes! Use controller instance isolation with label selectors. See [Multi-Controller Isolation](../user-guide/multi-controller-isolation.md) for configuration.

Each controller instance only manages `VariantAutoscaling` resources with matching `wva.llmd.ai/controller-instance` labels.

### Does WVA support multi-model serving?

WVA manages **model variants** (same model on different accelerators/configurations). Each variant gets its own `VariantAutoscaling` CR.

For multi-model serving (different models in one pod), WVA is not currently designed for that use case.

### How do I upgrade WVA?

**Important**: Helm doesn't automatically update CRDs. Before upgrading:

```bash
# 1. Apply updated CRDs
kubectl apply -f charts/workload-variant-autoscaler/crds/

# 2. Upgrade Helm release
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system
```

See [Upgrading](../../README.md#upgrading) section in main README for version-specific breaking changes.

## Troubleshooting

### The controller is crashing with "Prometheus connection failed"

Check:
1. `PROMETHEUS_BASE_URL` is set correctly
2. Network connectivity from WVA pod to Prometheus
3. TLS certificates are valid (if using HTTPS)
4. Bearer token is valid (if using authentication)

Enable debug logging:

```bash
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager
```

### Scaling is too aggressive/conservative

Adjust HPA behavior parameters:

```yaml
behavior:
  scaleUp:
    stabilizationWindowSeconds: 60
    policies:
    - type: Percent
      value: 50
      periodSeconds: 60
  scaleDown:
    stabilizationWindowSeconds: 300
```

See [HPA Integration](../integrations/hpa-integration.md) for tuning guidance.

### How do I debug what WVA is calculating?

Check the `VariantAutoscaling` status:

```bash
kubectl get variantautoscaling <name> -n <namespace> -o yaml
```

Look at:
- `status.desiredOptimizedAlloc`: Target replica count
- `status.conditions`: Health status
- `status.actuation.applied`: Whether actuation succeeded

Also check controller logs for detailed reconciliation output.

## Contributing & Support

### How do I report a bug or request a feature?

Open a [GitHub Issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues) with:
- WVA version
- Kubernetes/OpenShift version
- Detailed description and reproduction steps
- Relevant logs and `VariantAutoscaling` YAML

### How can I contribute?

See [Contributing Guide](../../CONTRIBUTING.md) and [Developer Guide](../developer-guide/development.md) to get started.

We welcome:
- Bug fixes and improvements
- Documentation updates
- Test coverage
- Feature development

### Where can I get help?

- **Documentation**: Check [docs](../) directory
- **Issues**: Search [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- **Community**: Join llm-d community meetings

---

**Don't see your question?** Open an [issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues) to ask or suggest additions to this FAQ.
