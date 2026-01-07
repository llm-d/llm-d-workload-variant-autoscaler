# Frequently Asked Questions (FAQ)

## General Questions

### What is Workload-Variant-Autoscaler (WVA)?

WVA is a Kubernetes controller that performs intelligent autoscaling for LLM inference model servers based on saturation metrics. It optimizes replica counts for given request traffic loads while meeting Service Level Objectives (SLOs).

### How is WVA different from standard HPA/KEDA?

WVA focuses on **saturation-based scaling** specifically designed for LLM inference workloads. Unlike traditional autoscalers that react to CPU/memory metrics:

- **Saturation Awareness**: WVA monitors KV cache utilization and queue depth to understand actual server capacity
- **Inference-Specific Metrics**: Tracks TTFT (Time To First Token), ITL (Inter-Token Latency), and token generation rates
- **Proactive Scaling**: Uses queueing theory models to predict optimal replica counts before SLO violations
- **Works with HPA/KEDA**: WVA exposes custom metrics that HPA/KEDA consume for actual scaling decisions

### What model architectures does WVA support?

WVA is designed for standard transformer-based LLM architectures. See [Architecture Limitations](../design/architecture-limitations.md) for important details if you're using:

- Hybrid State Space Models (HSSM) like Mamba
- Mixture of Experts (MoE) architectures
- Non-standard attention mechanisms

## Installation & Setup

### What are the prerequisites for installing WVA?

- **Kubernetes**: v1.31.0+ (or OpenShift 4.18+)
- **Helm**: 3.x
- **Prometheus**: For metrics collection
- **HPA or KEDA**: For actual scaling execution
- **GPU support**: Optional - works with emulated GPUs for testing

### Can I test WVA without physical GPUs?

Yes! WVA includes a Kind-based emulator that works on Mac (Apple Silicon/Intel) and Windows:

```bash
make deploy-llm-d-wva-emulated-on-kind
```

This creates a local cluster with emulated GPUs and the full WVA stack. See [Local Development Guide](../../deploy/kind-emulator/README.md).

### Do I need to install llm-d infrastructure?

Yes, WVA is designed to work with [llm-d infrastructure](https://github.com/llm-d-incubation/llm-d-infra) which provides:

- Model server deployments (vLLM, TGI, etc.)
- Prometheus metrics collection
- Service mesh integration

You can also integrate WVA with existing inference deployments that expose compatible metrics.

### How do I upgrade WVA?

**Important**: Helm doesn't automatically update CRDs. Always apply CRDs first:

```bash
# Apply updated CRDs
kubectl apply -f charts/workload-variant-autoscaler/crds/

# Then upgrade the Helm release
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  [your-values...]
```

See [Upgrading](../../README.md#upgrading) for version-specific breaking changes.

## Configuration & Usage

### When should I create the VariantAutoscaling CR?

WVA handles creation order gracefully:

- **Before deployment**: WVA waits for the deployment to appear
- **After deployment**: WVA starts monitoring immediately
- **After deletion**: Status updates immediately; resumes when deployment returns

**Recommendation**: Create after deployment is warmed up to avoid initial scale-down.

### What's the minimum configuration needed?

```yaml
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: my-model-autoscaler
  namespace: llm-inference
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: my-model-deployment
  modelID: "meta/llama-3.1-8b"
```

WVA auto-discovers accelerator types and uses default scaling parameters. See [Configuration Guide](configuration.md) for advanced options.

### How do I configure HPA to work with WVA?

WVA exposes metrics that HPA consumes. Basic HPA configuration:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: my-model-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: my-model-deployment
  minReplicas: 1
  maxReplicas: 10
  metrics:
  - type: Object
    object:
      metric:
        name: wva_desired_replicas
      describedObject:
        apiVersion: llmd.ai/v1alpha1
        kind: VariantAutoscaling
        name: my-model-autoscaler
      target:
        type: Value
        value: "1"
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 240
    scaleUp:
      stabilizationWindowSeconds: 120
```

See [HPA Integration](../integrations/hpa-integration.md) for complete details.

### How often does WVA reconcile?

Default: **60 seconds** (configurable via `wva.reconcileInterval` Helm value)

Adjust based on your needs:
- **Faster response**: 30s (increases API load)
- **Stable workloads**: 120s (reduces overhead)

## Multi-Controller & Advanced Features

### Can I run multiple WVA controllers in one cluster?

Yes! WVA supports multi-controller isolation using the `wva.controllerInstance` Helm value:

```bash
helm install wva-test-1 ./charts/workload-variant-autoscaler \
  --set wva.controllerInstance=test-1
  
helm install wva-test-2 ./charts/workload-variant-autoscaler \
  --set wva.controllerInstance=test-2
```

Each controller only manages VAs with matching `wva.llmd.ai/controller-instance` labels. See [Multi-Controller Isolation](multi-controller-isolation.md).

### What is scale-to-zero support?

Currently **experimental** (`wva.scaleToZero=false` by default). When enabled, WVA can scale deployments to zero replicas during idle periods. Enable with:

```bash
helm upgrade wva ./charts/workload-variant-autoscaler \
  --set wva.scaleToZero=true
```

**Note**: Requires cold-start handling and may increase initial latency.

## Troubleshooting

### WVA isn't scaling my deployment

**Check these common issues**:

1. **HPA not configured**: WVA emits metrics; HPA/KEDA performs scaling
   ```bash
   kubectl get hpa -n <namespace>
   ```

2. **Metrics not available**: Verify Prometheus integration
   ```bash
   kubectl logs -n workload-variant-autoscaler-system deployment/wva-controller-manager
   ```

3. **VariantAutoscaling status**: Check for errors
   ```bash
   kubectl describe variantautoscaling <name> -n <namespace>
   ```

4. **Controller instance mismatch**: Ensure VA label matches controller
   ```bash
   kubectl get va <name> -o jsonpath='{.metadata.labels.wva\.llmd\.ai/controller-instance}'
   ```

### Metrics show as "no data" in Prometheus

**Verify**:

1. ServiceMonitor is deployed and scraped:
   ```bash
   kubectl get servicemonitor -n workload-variant-autoscaler-system
   ```

2. WVA metrics endpoint is accessible:
   ```bash
   kubectl port-forward -n workload-variant-autoscaler-system svc/wva-metrics-service 8443:8443
   curl -k https://localhost:8443/metrics
   ```

3. Prometheus has proper RBAC and TLS certificates

See [Prometheus Integration](../integrations/prometheus.md) for detailed troubleshooting.

### Scaling is too aggressive or too slow

**Tune HPA behavior**:

- **Too aggressive**: Increase `stabilizationWindowSeconds` (default: 240s down, 120s up)
- **Too slow**: Decrease `reconcileInterval` or adjust `periodSeconds` in HPA policies

Example conservative scaling:

```yaml
behavior:
  scaleDown:
    stabilizationWindowSeconds: 600  # Wait 10 minutes
    policies:
    - type: Pods
      value: 1
      periodSeconds: 300  # Max 1 pod per 5 minutes
```

### How do I debug WVA controller issues?

1. **Check controller logs**:
   ```bash
   kubectl logs -n workload-variant-autoscaler-system deployment/wva-controller-manager -f
   ```

2. **Inspect VariantAutoscaling status**:
   ```bash
   kubectl get variantautoscaling <name> -n <namespace> -o yaml
   ```

3. **Verify Prometheus connectivity**:
   ```bash
   kubectl exec -n workload-variant-autoscaler-system deployment/wva-controller-manager -- \
     curl -k https://prometheus-service:9090/api/v1/query?query=up
   ```

4. **Enable verbose logging** (requires controller restart with debug flags)

See [Debugging Guide](../developer-guide/debugging.md) for advanced techniques.

## Metrics & Monitoring

### What metrics does WVA expose?

**Key metrics**:

- `wva_desired_replicas`: Optimal replica count calculated by WVA
- `wva_current_replicas`: Current replica count
- `wva_kv_cache_utilization`: KV cache usage percentage
- `wva_queue_depth`: Request queue depth
- `wva_ttft_ms`: Time To First Token (milliseconds)
- `wva_itl_ms`: Inter-Token Latency (milliseconds)
- `wva_reconciliation_duration_seconds`: Controller reconciliation time

All metrics include `controller_instance` label for multi-controller environments.

See [Prometheus Metrics](../integrations/prometheus.md) for complete list.

### How do I monitor WVA health?

Check these indicators:

1. **Controller availability**: Deployment health
   ```bash
   kubectl get deployment -n workload-variant-autoscaler-system
   ```

2. **Reconciliation errors**: Check conditions
   ```bash
   kubectl get variantautoscaling -A -o jsonpath='{range .items[*]}{.metadata.name}: {.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}'
   ```

3. **Metric freshness**: Verify last update timestamp in Prometheus

See [Metrics & Health Monitoring](../metrics-health-monitoring.md).

## Performance & Optimization

### What's the overhead of running WVA?

**Resource usage** (typical):
- **CPU**: ~100m baseline, spikes to ~500m during reconciliation
- **Memory**: ~256Mi baseline, ~512Mi under load
- **Network**: Minimal - only Prometheus queries and K8s API calls

**Recommendations**:
- Set resource requests/limits appropriately
- Adjust `reconcileInterval` for workload variability
- Use controller instance isolation for testing to avoid interference

### Can WVA handle high-traffic production workloads?

Yes, WVA is designed for production use with:

- **Efficient reconciliation**: Only processes changed resources
- **Background metric collection**: Non-blocking Prometheus queries
- **Metric caching**: Reduces repeated API calls
- **Leader election**: Multiple replicas for HA

**Production best practices**:
- Use HPA stabilization windows (120s+ for scale-up, 240s+ for scale-down)
- Monitor reconciliation duration metrics
- Set appropriate min/max replica bounds
- Test scaling behavior under load (see [Testing Guide](../developer-guide/testing.md))

### How do I optimize for cost vs. performance?

WVA includes `variantCost` field in VariantAutoscaling spec to influence scaling decisions:

```yaml
spec:
  variantCost: "15.5"  # Cost per replica (arbitrary units)
```

**Higher cost** = WVA prefers fewer replicas (may sacrifice latency)
**Lower cost** = WVA scales more aggressively (better latency, higher cost)

Combine with HPA min/max bounds for hard limits.

## Contributing & Community

### How can I contribute to WVA?

See [Contributing Guide](../../CONTRIBUTING.md) for:

- Development setup
- Code standards
- PR process
- Testing requirements

Join [llm-d community meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4) to get involved.

### Where can I get help?

- **Documentation**: Start with [README](../../README.md) and [User Guide](installation.md)
- **GitHub Issues**: [Open an issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- **Community**: Join llm-d Slack workspace
- **Discussions**: [GitHub Discussions](https://github.com/llm-d-incubation/workload-variant-autoscaler/discussions)

### How do I report a bug or request a feature?

1. **Search existing issues**: Check if already reported
2. **Provide details**: Include:
   - WVA version (`kubectl get deployment wva-controller-manager -o jsonpath='{.spec.template.spec.containers[0].image}'`)
   - Kubernetes version
   - Deployment configuration
   - Relevant logs and error messages
3. **Minimal reproducible example**: If possible
4. **Label appropriately**: Use GitHub issue templates

---

**Still have questions?** Open a [GitHub Discussion](https://github.com/llm-d-incubation/workload-variant-autoscaler/discussions) or check the [User Guide](installation.md).
