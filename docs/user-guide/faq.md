# Frequently Asked Questions (FAQ)

## General Questions

### What is Workload-Variant-Autoscaler (WVA)?

WVA is a Kubernetes controller that performs intelligent autoscaling for inference model servers based on saturation detection. It monitors KV cache utilization and queue depths to determine optimal replica counts for given request traffic loads.

### How does WVA differ from standard HPA?

WVA provides model-aware autoscaling specifically designed for LLM inference workloads. Unlike standard HPA which scales based on CPU/memory, WVA understands inference-specific metrics like KV cache saturation and queue depth, enabling more intelligent scaling decisions.

### What platforms does WVA support?

- Kubernetes v1.31.0+
- OpenShift 4.18+
- Local development with Kind (GPU emulation supported)

Works on Mac (Apple Silicon/Intel), Linux, and Windows with no physical GPUs required for development and testing.

## Installation & Setup

### Do I need physical GPUs to test WVA?

No! WVA includes a Kind emulator that works without physical GPUs. Perfect for development and testing:

```bash
make deploy-wva-emulated-on-kind CREATE_CLUSTER=true DEPLOY_LLM_D=true
```

### How do I upgrade WVA to a new version?

**Important:** Helm doesn't automatically update CRDs. Manually apply CRDs first:

```bash
# 1. Apply updated CRDs
kubectl apply -f charts/workload-variant-autoscaler/crds/

# 2. Upgrade Helm release
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  [your-values...]
```

See the [Installation Guide](installation.md#upgrading) for details.

### Can I install WVA without Helm?

Yes, you can use Kustomize:

```bash
make install  # Install CRDs
make deploy IMG=quay.io/llm-d/workload-variant-autoscaler:latest
```

Or use platform-specific scripts in `deploy/kubernetes/` or `deploy/openshift/`.

## Configuration

### What should I create first: VariantAutoscaling or Deployment?

Either order works! WVA handles both cases gracefully:

- **Deployment first (recommended)**: VA starts monitoring immediately when created
- **VA first**: VA status indicates "DeploymentNotFound" until deployment exists, then automatically starts monitoring

See [Configuration Guide](configuration.md#deployment-lifecycle-management) for details.

### What happens if I delete a deployment while VA exists?

WVA automatically:
1. Updates VA status to indicate deployment not found
2. Clears stale metrics to prevent incorrect scaling
3. Keeps the VA resource (doesn't delete it)
4. Automatically resumes when deployment is recreated

No manual intervention required!

### How do I specify costs for different GPU types?

Use the `variantCost` field in your VariantAutoscaling spec:

```yaml
spec:
  modelID: "meta/llama-3.1-8b"
  variantCost: "80.0"  # Higher cost for H100
```

This helps WVA make cost-aware scaling decisions when multiple variants can handle the load.

### What's the difference between CAPACITY-ONLY and HYBRID mode?

- **CAPACITY-ONLY (default, recommended)**: Reactive scaling based on saturation detection. Fast (<30s), predictable, no model training needed.
- **HYBRID (experimental)**: Combines saturation detection with proactive model-based optimization. Slower (~60s), requires model training.

Most users should stick with CAPACITY-ONLY mode. See [Configuration Guide](configuration.md#operating-modes).

## Integration

### Can WVA work with existing HPA configurations?

Yes! WVA emits custom metrics that HPA can consume. See [HPA Integration Guide](../integrations/hpa-integration.md).

### Does WVA support KEDA?

Yes, KEDA can read WVA's custom metrics. See [KEDA Integration Guide](../integrations/keda-integration.md).

### How do I connect WVA to Prometheus?

Configure via environment variables in your deployment:

```yaml
env:
  - name: PROMETHEUS_BASE_URL
    value: "https://prometheus.example.com"
  - name: PROMETHEUS_CA_CERT_PATH
    value: "/etc/prometheus/ca.crt"
```

See [Prometheus Integration](../integrations/prometheus.md) for complete configuration.

## Operations

### How can I debug WVA if it's not scaling correctly?

1. **Check VA status**:
   ```bash
   kubectl get va <name> -o yaml
   kubectl describe va <name>
   ```

2. **View controller logs**:
   ```bash
   kubectl logs -n workload-variant-autoscaler-system \
     deployment/workload-variant-autoscaler-controller-manager -f
   ```

3. **Verify metrics are available**:
   ```bash
   kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/inferno_desired_replicas"
   ```

See [Debugging Guide](../developer-guide/debugging.md) for detailed troubleshooting.

### What metrics does WVA expose?

Key metrics include:
- `inferno_desired_replicas` - Target replica count
- `inferno_current_allocation` - Current resource allocation
- `wva_reconcile_duration_seconds` - Controller performance metrics

See [Prometheus Integration](../integrations/prometheus.md#metrics) for the complete list.

### How do I run multiple WVA controllers in the same cluster?

Use controller instance isolation:

```yaml
# values.yaml for each controller instance
wva:
  controllerInstance: "instance-a"  # unique per controller
```

See [Multi-Controller Isolation Guide](multi-controller-isolation.md) for details.

### What's the recommended HPA stabilization window?

We recommend at least 120 seconds for gradual scaling behavior:

```yaml
behavior:
  scaleUp:
    stabilizationWindowSeconds: 120
  scaleDown:
    stabilizationWindowSeconds: 300
```

## Model Support

### Which model architectures does WVA support?

WVA is optimized for **dense transformer architectures** (e.g., LLaMA, GPT). If you're using:
- Hybrid State Space Models (HSSM)
- Mixture of Experts (MoE) - e.g., Mixtral, DeepSeek
- Other optimized/custom architectures

**Read [Architecture Limitations](../design/architecture-limitations.md)** to understand important considerations.

### Can WVA handle multiple models simultaneously?

Yes! Create a separate VariantAutoscaling resource for each model/deployment:

```yaml
# Model 1
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: llama-8b-autoscaler
spec:
  scaleTargetRef:
    name: llama-8b
  modelID: "meta/llama-3.1-8b"
---
# Model 2
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: llama-70b-autoscaler
spec:
  scaleTargetRef:
    name: llama-70b
  modelID: "meta/llama-3.1-70b"
```

### What inference servers does WVA work with?

WVA is tested with vLLM but can work with any inference server that exposes compatible Prometheus metrics. Key metrics needed:
- KV cache utilization
- Queue depth/waiting requests
- Request rate

## Troubleshooting

### My VA shows "DeploymentNotFound" - what's wrong?

This means WVA can't find the target deployment. Check:

1. Deployment exists in the same namespace:
   ```bash
   kubectl get deployment <name> -n <namespace>
   ```

2. `scaleTargetRef.name` matches exactly:
   ```yaml
   spec:
     scaleTargetRef:
       name: my-deployment  # must match deployment name
   ```

3. VA and deployment are in the same namespace

### Metrics aren't available for my VA

Common causes:

1. **Prometheus not configured**: Check `PROMETHEUS_BASE_URL` environment variable
2. **ServiceMonitor missing**: Verify Prometheus can scrape your inference server
3. **Inference server not ready**: Check deployment pods are running

Check VA status:
```bash
kubectl get va <name> -o jsonpath='{.status.conditions[?(@.type=="MetricsAvailable")]}'
```

### HPA shows "unable to get external metric"

Ensure:
1. WVA controller is running and healthy
2. VA resource is created and Ready
3. HPA references the correct metric name and namespace
4. Prometheus adapter (or KEDA) is installed and configured

See [HPA Integration](../integrations/hpa-integration.md#troubleshooting).

### Scaling is too aggressive/conservative

Adjust HPA behavior policies:

```yaml
behavior:
  scaleUp:
    stabilizationWindowSeconds: 120  # Increase for less aggressive scale-up
    policies:
    - type: Percent
      value: 50  # Scale by max 50% at a time
      periodSeconds: 60
  scaleDown:
    stabilizationWindowSeconds: 300  # Longer window = more conservative scale-down
```

### Safety net keeps activating

If you see "Safety net activated" messages frequently:
1. Check Prometheus connectivity and authentication
2. Verify metrics are being scraped from inference servers
3. Check for network policies blocking metrics access
4. Review controller logs for underlying errors

The safety net prevents HPA from using stale metrics during failures.

## Performance & Optimization

### How often does WVA reconcile?

Default reconciliation happens:
- On deployment/VA changes (event-driven, immediate)
- On metrics updates from Prometheus
- Every reconciliation period (configurable)

Event-driven reconciliation ensures fast response to changes.

### What's the expected latency for scaling decisions?

- **CAPACITY-ONLY mode**: Typically <30 seconds from saturation detection to metric emission
- **HYBRID mode**: ~60 seconds (includes model-based optimization)

HPA stabilization windows add additional delay (recommended 120s+ for scale-up).

### How can I optimize costs?

1. Set appropriate `variantCost` values per GPU type
2. Use CAPACITY-ONLY mode (faster, more efficient)
3. Configure reasonable HPA scale-down policies
4. Monitor utilization and adjust thresholds in ConfigMaps

See [Configuration Guide](configuration.md#cost-optimization).

## Contributing

### How can I contribute to WVA?

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for guidelines on:
- Setting up development environment
- Running tests
- Submitting pull requests
- Documentation standards

### Where should I report bugs?

Open a [GitHub Issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues) with:
- WVA version
- Kubernetes/OpenShift version
- Steps to reproduce
- Relevant logs and VA status

### How do I request a new feature?

Open a GitHub Issue with the "enhancement" label describing:
- Use case and motivation
- Proposed solution
- Alternative approaches considered

## Additional Resources

- [Installation Guide](installation.md)
- [Configuration Guide](configuration.md)
- [Developer Guide](../developer-guide/development.md)
- [Architecture Documentation](../design/modeling-optimization.md)
- [Community Meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)

---

**Still have questions?** Open a [GitHub Discussion](https://github.com/llm-d-incubation/workload-variant-autoscaler/discussions) or join our community meetings!
