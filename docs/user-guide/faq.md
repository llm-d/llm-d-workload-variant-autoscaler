# Frequently Asked Questions (FAQ)

## General Questions

### What is Workload-Variant-Autoscaler (WVA)?

WVA is a Kubernetes controller that performs intelligent autoscaling for LLM inference model servers based on saturation metrics. It determines optimal replica counts for given request traffic loads by analyzing KV cache utilization and queue depth.

### How is WVA different from HPA or KEDA?

WVA works *with* HPA or KEDA, not instead of them. WVA analyzes saturation metrics from inference servers and exposes optimization results as custom metrics. HPA or KEDA then reads these metrics to perform the actual scaling. This architecture provides:

- Separation of concerns (analysis vs. scaling execution)
- Flexibility to use either HPA or KEDA
- Standard Kubernetes autoscaling patterns

### What inference servers does WVA support?

Currently, WVA is optimized for vLLM servers. The controller monitors vLLM-specific metrics including:

- KV cache utilization (`vllm:gpu_cache_usage_perc`)
- Queue depth
- Request rates
- Token generation metrics

### Do I need GPUs to test WVA?

No! WVA includes a Kind-based emulator that works on Mac (Apple Silicon/Intel) and Windows without physical GPUs. See the [Local Development Guide](../deploy/kind-emulator/README.md).

```bash
make deploy-llm-d-wva-emulated-on-kind
```

## Installation & Setup

### What are the minimum requirements?

- Kubernetes v1.31.0+ or OpenShift 4.18+
- Prometheus for metrics collection
- Helm 3.x for installation
- kubectl or oc CLI

### Should I create the VariantAutoscaling CR before or after the Deployment?

You can create them in any order! WVA handles both scenarios gracefully:

- If the VA is created first, it waits for the deployment and automatically begins operation when the deployment appears
- If the deployment exists first, the VA immediately begins monitoring and optimizing
- If a deployment is deleted, the VA status reflects the missing deployment and resumes when the deployment is recreated

### How do I know if WVA is working?

Check the status conditions on your VariantAutoscaling resource:

```bash
kubectl get variantautoscaling <name> -o jsonpath='{.status.conditions}' | jq
```

Look for:
- `MetricsAvailable: True` - vLLM metrics are being collected
- `OptimizationReady: True` - Optimization is running successfully

See [Metrics Health Monitoring](../metrics-health-monitoring.md) for detailed condition meanings.

## Configuration

### How do I configure the saturation threshold?

Edit the WVA ConfigMap to set your desired saturation threshold (default is 80%):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: wva-config-saturation-scaling
data:
  config.yaml: |
    saturation:
      target_utilization: 0.8  # 80% target utilization
      min_replicas: 1
      max_replicas: 10
```

See [Configuration Guide](configuration.md) for all available options.

### How often does WVA reconcile?

By default, WVA reconciles all VariantAutoscaling resources every 60 seconds. This can be configured via the `--reconcile-interval` flag in the controller manager.

### Can I run multiple WVA controllers?

Yes! WVA supports multi-controller isolation using namespace-based or label-based filtering. See [Multi-Controller Isolation](multi-controller-isolation.md).

## Troubleshooting

### My VariantAutoscaling shows "MetricsAvailable: False"

This typically means:

1. **ServiceMonitor not configured** - Ensure your vLLM deployment has a ServiceMonitor for Prometheus
2. **Prometheus not scraping** - Check Prometheus targets: `kubectl port-forward svc/prometheus 9090:9090`
3. **Wrong metric names** - Verify vLLM is exposing metrics at `/metrics`

See [Troubleshooting Guide](troubleshooting.md) for detailed debugging steps.

### Scaling is too aggressive/slow

Adjust the HPA stabilization window:

```yaml
behavior:
  scaleDown:
    stabilizationWindowSeconds: 300  # 5 minutes
  scaleUp:
    stabilizationWindowSeconds: 0    # Immediate
```

See [HPA Integration](../integrations/hpa-integration.md) for tuning guidance.

### WVA isn't scaling my deployment

WVA doesn't scale deployments directly. Check that:

1. Your HPA or KEDA is configured to read WVA metrics
2. The metric name matches: `inferno_desired_ratio`
3. HPA has appropriate permissions

Example check:
```bash
kubectl get hpa <name> -o yaml
kubectl describe hpa <name>
```

### How do I debug reconciliation issues?

Enable debug logging:

```bash
# Port forward to WVA controller
kubectl port-forward -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager 8080:8080

# Check metrics endpoint
curl http://localhost:8080/metrics

# Check logs
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager -f
```

See [Debugging Guide](../developer-guide/debugging.md) for advanced techniques.

## Performance & Optimization

### What's the recommended replica count range?

Start with:
- `minReplicas: 1-2` (avoid cold starts)
- `maxReplicas: 10-20` (prevent runaway scaling)

Adjust based on your traffic patterns and cost constraints.

### How does WVA handle cold starts?

WVA includes a scale-from-zero engine that can handle cold-start scenarios, but it's recommended to maintain at least 1 replica for production workloads to ensure responsiveness.

### Can WVA optimize costs?

Yes! WVA's saturation-based approach minimizes replica count while maintaining SLO requirements. It considers:

- Current KV cache utilization
- Queue depth and slack capacity
- Request rate trends

### How accurate is the saturation model?

The saturation model is most accurate when:
- vLLM metrics are up-to-date (< 5 minutes old)
- Traffic patterns are relatively stable
- Batch sizes are within configured limits

For accuracy analysis, see [Saturation Analyzer](../saturation-analyzer.md).

## Advanced Topics

### Can I customize the optimization algorithm?

Yes, through ConfigMaps. You can adjust:
- Saturation thresholds
- Scaling policies
- Metric weights
- Queue depth limits

See [Saturation Scaling Configuration](../saturation-scaling-config.md).

### Does WVA support multi-model scenarios?

Yes! Each model deployment gets its own VariantAutoscaling resource. WVA independently optimizes each variant based on its specific metrics and configuration.

### How does WVA handle traffic spikes?

WVA continuously monitors queue depth and saturation. When a spike is detected:
1. Increased queue depth triggers higher desired replicas
2. HPA/KEDA reads the updated `inferno_desired_ratio` metric
3. Scaling occurs according to HPA/KEDA policy

The stabilization window prevents thrashing during transient spikes.

### Can I use WVA with model architectures other than standard transformers?

WVA makes assumptions about token generation patterns. For specialized architectures (HSSM, MoE, etc.), see [Architecture Limitations](../design/architecture-limitations.md) for compatibility guidance.

## Contributing & Community

### How can I contribute?

See our [Contributing Guide](../../CONTRIBUTING.md) for:
- Setting up a development environment
- Running tests
- Submitting pull requests
- Code style guidelines

### Where can I get help?

- Open a [GitHub Issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- Join [llm-d community meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)
- Review existing documentation in [docs/](../README.md)

### Is there a roadmap?

See the [GitHub Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues) and [Project Board](https://github.com/orgs/llm-d-incubation/projects) for planned features and enhancements.

## Additional Resources

- [Installation Guide](installation.md)
- [Configuration Guide](configuration.md)
- [CRD Reference](crd-reference.md)
- [Troubleshooting Guide](troubleshooting.md)
- [Tutorials](../tutorials/demo.md)
