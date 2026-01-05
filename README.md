# Workload-Variant-Autoscaler (WVA)

[![Go Report Card](https://goreportcard.com/badge/github.com/llm-d-incubation/workload-variant-autoscaler)](https://goreportcard.com/report/github.com/llm-d-incubation/workload-variant-autoscaler)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)


The Workload-Variant-Autoscaler (WVA) is a Kubernetes controller that performs intelligent autoscaling for inference model servers based on saturation. The high-level details of the algorithm where we explore only capacity are [here](https://github.com/llm-d-incubation/workload-variant-autoscaler/blob/main/docs/saturation-scaling-config.md ). It determines optimal replica counts for given request traffic loads for inference servers.
<!--
<![Architecture](docs/design/diagrams/inferno-WVA-design.png)>
-->
## Key Features

- **Intelligent Autoscaling**: Optimizes replica count and GPU allocation based on inference server saturation
- **Cost Optimization**: Minimizes infrastructure costs while meeting SLO requirements
<!-- 
- **Performance Modeling**: Uses queueing theory (M/M/1/k, M/G/1 models) for accurate latency and throughput prediction
- **Multi-Model Support**: Manages multiple models with different service classes and priorities -->

## Quick Start

### Prerequisites

- Kubernetes v1.31.0+ (or OpenShift 4.18+)
- Helm 3.x
- kubectl

### Install with Helm (Recommended)

```bash
# Add the WVA Helm repository (when published)
helm upgrade -i workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  --set-file prometheus.caCert=/tmp/prometheus-ca.crt \
  --set variantAutoscaling.accelerator=L40S \
  --set variantAutoscaling.modelID=unsloth/Meta-Llama-3.1-8B \
  --set vllmService.enabled=true \
  --set vllmService.nodePort=30000
  --create-namespace
```

### Try it Locally with Kind (No GPU Required!)

```bash
# Deploy WVA with llm-d infrastructure on a local Kind cluster
make deploy-llm-d-wva-emulated-on-kind

# This creates a Kind cluster with emulated GPUs and deploys:
# - WVA controller
# - llm-d infrastructure (simulation mode)
# - Prometheus and monitoring stack
# - vLLM emulator for testing
```

**Works on Mac (Apple Silicon/Intel) and Windows** - no physical GPUs needed!
Perfect for development and testing with GPU emulation.

See the [Installation Guide](docs/user-guide/installation.md) for detailed instructions.

## Documentation

### User Guide
- [Installation Guide](docs/user-guide/installation.md)
- [Configuration](docs/user-guide/configuration.md)
- [CRD Reference](docs/user-guide/crd-reference.md)
- [Troubleshooting](docs/user-guide/troubleshooting.md)

<!-- 

### Tutorials
- [Quick Start Demo](docs/tutorials/demo.md)
- [Parameter Estimation](docs/tutorials/parameter-estimation.md)
- [vLLM Server Setup](docs/tutorials/vllm-samples.md)
-->
### Integrations
- [HPA Integration](docs/integrations/hpa-integration.md)
- [KEDA Integration](docs/integrations/keda-integration.md)
- [Prometheus Metrics](docs/integrations/prometheus.md)

<!-- 

### Design & Architecture
- [Architecture Overview](docs/design/modeling-optimization.md)
- [**Architecture Limitations**](docs/design/architecture-limitations.md) - **Important:** Read this if using HSSM, MoE, or non-standard architectures
- [Architecture Diagrams](docs/design/diagrams/) - Visual architecture and workflow diagrams
-->
<!-- 
### Developer Guide
- [Development Setup](docs/developer-guide/development.md)
- [Contributing](CONTRIBUTING.md)
-->
### Deployment Options
- [Kubernetes Deployment](deploy/kubernetes/README.md)
- [OpenShift Deployment](deploy/openshift/README.md)
- [Local Development (Kind Emulator)](deploy/kind-emulator/README.md)

## Architecture

WVA consists of several key components:

- **Reconciler**: Kubernetes controller that manages VariantAutoscaling resources
- **Collector**: Gathers cluster state and vLLM server metrics
<!-- 
- **Model Analyzer**: Performs per-model analysis using queueing theory
- **Optimizer**: Makes global scaling decisions across models
-->
- **Optimizer**: Capacity model provides saturation based scaling based on threshold
- **Actuator**: Emits metrics to Prometheus and updates deployment replicas

<!-- 
For detailed architecture information, see the [design documentation](docs/design/modeling-optimization.md).
-->
## How It Works

1. Platform admin deploys llm-d infrastructure (including model servers) and waits for servers to warm up and start serving requests
2. Platform admin creates a `VariantAutoscaling` CR for the running deployment
3. WVA continuously monitors request rates and server performance via Prometheus metrics
<!-- 
4. Model Analyzer estimates latency and throughput using queueing models
5. Optimizer solves for minimal cost allocation meeting all SLOs
-->
4. Capacity model obtains KV cache utilization and queue depth of inference servers with slack capacity to determine replicas
5. Actuator emits optimization metrics to Prometheus and updates VariantAutoscaling status
6. External autoscaler (HPA/KEDA) reads the metrics and scales the deployment accordingly

**Important Notes**:
<!-- 
- Create the VariantAutoscaling CR **only after** your deployment is warmed up to avoid immediate scale-down
-->
- Configure HPA stabilization window (recommend 120s+) for gradual scaling behavior
- WVA updates the VA status with current and desired allocations every reconciliation cycle

## Example

```yaml
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: llama-8b-autoscaler
  namespace: llm-inference
spec:
  modelId: "meta/llama-3.1-8b"
```

More examples in [config/samples/](config/samples/).

## Upgrading

### CRD Updates

**Important:** Helm does not automatically update CRDs during `helm upgrade`. When upgrading WVA to a new version with CRD changes, you must manually apply the updated CRDs first:

```bash
# Apply the latest CRDs before upgrading
kubectl apply -f charts/workload-variant-autoscaler/crds/

# Then upgrade the Helm release
helm upgrade workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  [your-values...]
```

### Breaking Changes

#### v0.5.0 (upcoming)
- **VariantAutoscaling CRD**: Added `scaleTargetRef` field to explicitly specify the target deployment. If not set, the controller infers the target from the `modelID` field.

### Verifying CRD Version

To check if your cluster has the latest CRD schema:

```bash
# Check the CRD fields
kubectl get crd variantautoscalings.llmd.ai -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties}' | jq 'keys'
```

## Contributing

We welcome contributions! See the llm-d Contributing Guide for guidelines.

Join the [llm-d autoscaling community meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4) to get involved.

## License

Apache 2.0 - see [LICENSE](LICENSE) for details.

## Related Projects

- [llm-d infrastructure](https://github.com/llm-d-incubation/llm-d-infra)
- [llm-d main repository](https://github.com/llm-d-incubation/llm-d)

## References

- [Saturation based design discussion](https://docs.google.com/document/d/1iGHqdxRUDpiKwtJFr5tMCKM7RF6fbTfZBL7BTn6UkwA/edit?tab=t.0#heading=h.mdte0lq44ul4)

---

For detailed documentation, visit the [docs](docs/) directory.
