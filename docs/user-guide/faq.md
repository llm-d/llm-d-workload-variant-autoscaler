# Frequently Asked Questions (FAQ)

## General Questions

### What is Workload-Variant-Autoscaler (WVA)?

WVA is a Kubernetes controller that performs intelligent autoscaling for inference model servers based on saturation analysis. It optimizes replica counts for inference workloads by analyzing KV cache utilization, queue depth, and other performance metrics.

### How does WVA differ from HPA or KEDA?

WVA provides **intelligence** for autoscaling decisions, while HPA/KEDA provides the **actuation mechanism**:

- **WVA**: Analyzes inference server metrics, applies saturation models, and publishes target replica metrics to Prometheus
- **HPA/KEDA**: Reads these metrics and performs the actual scaling operations

Think of WVA as the "brain" and HPA/KEDA as the "muscles" of your autoscaling system.

### Which inference servers are supported?

Currently, WVA is designed for vLLM-compatible inference servers that expose Prometheus metrics. Support for additional inference frameworks is planned for future releases.

### What Kubernetes versions are supported?

- **Kubernetes**: v1.31.0 or later
- **OpenShift**: v4.18 or later

## Installation & Setup

### Can I test WVA without GPU hardware?

Yes! Use the Kind emulator for local development:

```bash
make deploy-llm-d-wva-emulated-on-kind
```

This works on Mac (Intel/Apple Silicon) and Windows without physical GPUs.

### Do I need to install Prometheus?

Yes, WVA requires Prometheus to:
1. Collect metrics from inference servers
2. Store WVA's published optimization metrics
3. Enable HPA/KEDA to read scaling recommendations

See the [Prometheus Integration Guide](../integrations/prometheus.md) for setup instructions.

### Should I create the VariantAutoscaling CR before or after deploying my inference server?

Either order works! WVA handles both scenarios gracefully:

- **CR first**: WVA waits for the deployment to become available
- **Deployment first**: WVA immediately begins monitoring and optimization

If you delete the deployment, WVA updates the CR status to reflect the missing target. When the deployment is recreated, WVA automatically resumes operation.

### How do I upgrade WVA?

**Important**: Helm does not automatically upgrade CRDs. Follow this process:

```bash
# 1. Apply updated CRDs first
kubectl apply -f charts/workload-variant-autoscaler/crds/

# 2. Then upgrade the Helm release
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  [your-values...]
```

See the [Installation Guide](installation.md#upgrading) for details.

## Configuration

### What metrics does WVA monitor?

WVA monitors inference server metrics including:

- Request arrival rate
- KV cache utilization
- Queue depth and waiting requests
- Average input/output token counts
- Time to First Token (TTFT)
- Inter-Token Latency (ITL)

See [Metrics & Health Monitoring](../metrics-health-monitoring.md) for the complete list.

### How do I configure the saturation threshold?

Configure saturation thresholds via ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: saturation-scaling-config
data:
  config.yaml: |
    kvCacheUtilizationThreshold: 0.85
    queueDepthThreshold: 10
    # ... more options
```

See [Saturation Scaling Configuration](../saturation-scaling-config.md) for all options.

### Can I run multiple WVA controllers in the same cluster?

Yes! Use controller isolation to manage different namespaces or model types:

```yaml
spec:
  controllerNamespace: "team-a-models"
```

See [Multi-Controller Isolation](multi-controller-isolation.md) for detailed configuration.

### What's the recommended HPA stabilization window?

We recommend **120 seconds or longer** to prevent scaling oscillations:

```yaml
behavior:
  scaleDown:
    stabilizationWindowSeconds: 120
  scaleUp:
    stabilizationWindowSeconds: 120
```

See [HPA Integration](../integrations/hpa-integration.md) for more details.

## Troubleshooting

### WVA controller is not starting

**Check logs:**
```bash
kubectl logs -n workload-variant-autoscaler-system \
  deployment/workload-variant-autoscaler-controller-manager
```

**Common issues:**
- Missing RBAC permissions
- Prometheus connection failures
- Invalid TLS certificates (OpenShift)

### The VariantAutoscaling CR shows "Deployment not found"

This is normal if:
- The deployment hasn't been created yet (WVA waits)
- The deployment was recently deleted (WVA updates status)
- The `scaleTargetRef` points to a non-existent resource

**Verify the target exists:**
```bash
kubectl get deployment <deployment-name> -n <namespace>
```

### Replicas are not scaling as expected

**Verify the integration chain:**

1. Check WVA metrics are published:
```bash
kubectl port-forward -n workload-variant-autoscaler-system \
  svc/workload-variant-autoscaler-controller-manager-metrics-service 8443:8443

curl -k https://localhost:8443/metrics | grep wva_
```

2. Check HPA can read metrics:
```bash
kubectl get hpa <hpa-name> -o yaml
```

3. Check HPA events:
```bash
kubectl describe hpa <hpa-name>
```

### Metrics are missing or stale

**Check Prometheus connectivity:**
```bash
# From within the WVA controller pod
curl -k https://prometheus-service:9091/api/v1/query?query=up
```

**Common issues:**
- Network policies blocking traffic
- Invalid CA certificates
- Prometheus not scraping inference server metrics

See [Troubleshooting Guide](troubleshooting.md) for detailed debugging steps.

### How do I enable debug logging?

Edit the WVA deployment to increase log verbosity:

```bash
kubectl edit deployment -n workload-variant-autoscaler-system \
  workload-variant-autoscaler-controller-manager
```

Add or modify the `--zap-log-level` flag:
```yaml
args:
  - --zap-log-level=2  # 0=info, 1=debug, 2=trace
```

## Architecture & Design

### Why does WVA use saturation-based scaling?

Saturation-based scaling provides superior performance for inference workloads compared to traditional CPU/memory metrics because it:

- Accounts for KV cache pressure (the primary bottleneck)
- Responds to queue buildup before latency degrades
- Prevents over-provisioning during low utilization

See [Saturation Analyzer](../saturation-analyzer.md) for the technical details.

### What are the architecture limitations?

WVA makes assumptions about model architecture that may not hold for:

- **Hybrid State Space Models (HSSM)** - Different memory patterns
- **Mixture of Experts (MoE)** - Variable compute patterns
- **Non-autoregressive models** - Different token generation patterns

See [Architecture Limitations](../design/architecture-limitations.md) for details.

### Does WVA support cost optimization?

Yes! WVA can minimize infrastructure costs while meeting SLO requirements by considering accelerator unit costs:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: wva-configmap-accelerator-costs
data:
  accelerator-costs.yaml: |
    accelerators:
      - name: "A100"
        unitCost: 3.06
      - name: "L40S"
        unitCost: 1.28
```

See the [cost optimization examples](../../config/samples/variantautoscaling-with-cost.yaml).

## Integration

### Can I use WVA with custom metrics?

Yes! WVA publishes metrics to Prometheus that can be consumed by any system that reads Prometheus metrics, including:

- Horizontal Pod Autoscaler (HPA)
- KEDA (Kubernetes Event-Driven Autoscaling)
- Custom controllers

### Does WVA work with service meshes?

Yes, WVA is compatible with service meshes like Istio. Ensure:

1. The WVA controller can reach Prometheus
2. Prometheus can scrape inference server pods
3. mTLS settings allow metric collection

### Can I integrate WVA with my CI/CD pipeline?

Yes! Use GitOps tools like ArgoCD or Flux to manage VariantAutoscaling CRs alongside your deployments:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-inference-service
spec:
  source:
    path: manifests/
    # Include both Deployment and VariantAutoscaling
```

## Performance

### What's the expected reconciliation frequency?

Default reconciliation occurs every **30 seconds**. Configure via Helm:

```yaml
reconciliation:
  interval: 30s
```

### How much overhead does WVA add?

WVA has minimal overhead:
- **Memory**: ~50-100MB per controller
- **CPU**: <0.1 core during steady state
- **Network**: Prometheus queries every reconciliation cycle

### How many inference servers can one WVA controller manage?

A single WVA controller can manage:
- **50-100 VariantAutoscaling CRs** in typical deployments
- Scales horizontally by running multiple controllers with namespace isolation

See [Multi-Controller Isolation](multi-controller-isolation.md) for large-scale deployments.

## Contributing

### How can I contribute to WVA?

We welcome contributions! See the [Contributing Guide](../../CONTRIBUTING.md) for:
- Development setup
- Coding standards
- PR process
- Community meetings

### Where can I report bugs or request features?

- **Bugs**: [Open a GitHub Issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- **Features**: Create a feature request issue
- **Discussions**: Use GitHub Discussions for questions

### How do I run the test suite?

```bash
# Unit tests
make test

# E2E tests (requires cluster)
make test-e2e

# Saturation-based E2E tests
make test-e2e-saturation
```

See [Testing Guide](../developer-guide/testing.md) for details.

## Additional Resources

- [Quick Start Demo](../tutorials/demo.md)
- [Parameter Estimation Guide](../tutorials/parameter-estimation.md)
- [Community Meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)
- [llm-d Infrastructure](https://github.com/llm-d-incubation/llm-d-infra)
- [Design Discussions](https://docs.google.com/document/d/1iGHqdxRUDpiKwtJFr5tMCKM7RF6fbTfZBL7BTn6UkwA/edit?tab=t.0#heading=h.mdte0lq44ul4)

---

**Can't find what you're looking for?**

Please [open an issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues) with your question. We'll add it to the FAQ!
