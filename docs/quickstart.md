# Quick Start Guide

Get started with Workload-Variant-Autoscaler (WVA) in under 10 minutes! This guide walks you through deploying WVA and creating your first autoscaling inference workload.

## What You'll Build

By the end of this guide, you'll have:
- ✅ WVA controller running in your cluster
- ✅ A vLLM inference server deployment
- ✅ Automatic scaling based on saturation metrics
- ✅ Integration with HPA or KEDA

## Prerequisites

Choose your environment:

### Option A: Production Cluster
- Kubernetes 1.31+ or OpenShift 4.18+
- kubectl/oc CLI configured
- Cluster admin access
- Helm 3.x installed

### Option B: Local Testing (No GPU Required!)
- Docker Desktop or Podman
- Kind CLI
- 8GB+ RAM available

**New to Kubernetes?** Option B (local testing) is recommended for learning.

## Step 1: Install WVA (5 minutes)

### Option A: Production Installation

```bash
# 1. Clone the repository
git clone https://github.com/llm-d-incubation/workload-variant-autoscaler.git
cd workload-variant-autoscaler

# 2. Install with Helm
helm install workload-variant-autoscaler ./charts/workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system \
  --create-namespace \
  --set prometheus.url=http://prometheus-service.monitoring:9090

# 3. Verify installation
kubectl get pods -n workload-variant-autoscaler-system
```

**Expected output:**
```
NAME                                                     READY   STATUS    RESTARTS   AGE
workload-variant-autoscaler-controller-manager-xxx-xxx   1/1     Running   0          30s
```

### Option B: Local Development

```bash
# 1. Clone the repository
git clone https://github.com/llm-d-incubation/workload-variant-autoscaler.git
cd workload-variant-autoscaler

# 2. Deploy complete stack with emulated GPUs
make deploy-llm-d-wva-emulated-on-kind

# This installs:
# - Kind cluster with GPU emulation
# - WVA controller
# - Prometheus and monitoring
# - vLLM emulator for testing
```

**Expected output:**
```
✓ Creating kind cluster
✓ Installing Prometheus
✓ Installing WVA controller
✓ Deploying vLLM emulator
✓ Setup complete!
```

Wait 2-3 minutes for all pods to be ready:
```bash
kubectl get pods -A
```

## Step 2: Deploy an Inference Server (2 minutes)

### For Production (Option A)

Create a vLLM deployment:

```bash
kubectl create namespace llm-inference

kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: llama-8b-server
  namespace: llm-inference
  labels:
    app: llama-8b
spec:
  replicas: 1
  selector:
    matchLabels:
      app: llama-8b
  template:
    metadata:
      labels:
        app: llama-8b
    spec:
      containers:
      - name: vllm
        image: vllm/vllm-openai:latest
        args:
          - --model
          - meta-llama/Meta-Llama-3.1-8B
          - --max-model-len
          - "4096"
          - --gpu-memory-utilization
          - "0.9"
        ports:
        - containerPort: 8000
          name: http
        resources:
          limits:
            nvidia.com/gpu: 1
---
apiVersion: v1
kind: Service
metadata:
  name: llama-8b-service
  namespace: llm-inference
spec:
  selector:
    app: llama-8b
  ports:
  - port: 8000
    targetPort: 8000
EOF
```

Wait for the deployment to be ready:
```bash
kubectl wait --for=condition=available --timeout=300s \
  deployment/llama-8b-server -n llm-inference
```

### For Local Testing (Option B)

The vLLM emulator is already deployed! Verify it's running:

```bash
kubectl get pods -n llm-d-system -l app=vllm-emulator
```

## Step 3: Create a VariantAutoscaling Resource (1 minute)

Create a VariantAutoscaling CR to enable WVA for your inference server:

### For Production (Option A)

```bash
kubectl apply -f - <<EOF
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: llama-8b-autoscaler
  namespace: llm-inference
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: llama-8b-server
  modelID: "meta-llama/Meta-Llama-3.1-8B"
  variantCost: "10.0"
EOF
```

### For Local Testing (Option B)

```bash
kubectl apply -f - <<EOF
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: vllm-emulator-autoscaler
  namespace: llm-d-system
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: vllm-emulator
  modelID: "meta-llama/Meta-Llama-3.1-8B-Instruct"
  variantCost: "1.0"
EOF
```

**Verify the resource was created:**
```bash
kubectl get variantautoscaling -A
```

## Step 4: Enable Automatic Scaling (2 minutes)

WVA publishes optimization metrics to Prometheus. Use HPA or KEDA to read these metrics and scale your deployment.

### Using HPA (Recommended)

```bash
# For Production (adjust namespace)
NAMESPACE=llm-inference
DEPLOYMENT=llama-8b-server

# For Local Testing
# NAMESPACE=llm-d-system
# DEPLOYMENT=vllm-emulator

kubectl apply -f - <<EOF
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: ${DEPLOYMENT}-hpa
  namespace: ${NAMESPACE}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: ${DEPLOYMENT}
  minReplicas: 1
  maxReplicas: 10
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 120
    scaleUp:
      stabilizationWindowSeconds: 60
  metrics:
  - type: Pods
    pods:
      metric:
        name: wva_desired_replicas
      target:
        type: AverageValue
        averageValue: "1"
EOF
```

**Note**: This requires prometheus-adapter to expose WVA metrics to HPA. See [HPA Integration](integrations/hpa-integration.md) for setup.

### Using KEDA (Alternative)

```bash
kubectl apply -f - <<EOF
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: ${DEPLOYMENT}-scaler
  namespace: ${NAMESPACE}
spec:
  scaleTargetRef:
    name: ${DEPLOYMENT}
  minReplicaCount: 1
  maxReplicaCount: 10
  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus-service.monitoring:9090
      metricName: wva_desired_replicas
      threshold: "1"
      query: |
        wva_desired_replicas{deployment="${DEPLOYMENT}"}
EOF
```

## Step 5: Verify Everything Works

### Check WVA Status

```bash
# View VariantAutoscaling status
kubectl get variantautoscaling -A -o wide

# Get detailed status
kubectl describe variantautoscaling <name> -n <namespace>
```

**Expected fields in status:**
- `currentAlloc`: Current deployment state (replicas, metrics)
- `desiredOptimizedAlloc`: WVA's scaling recommendation
- `actuation`: Status of metric publishing

### Check Scaling Metrics

```bash
# Port-forward WVA metrics service
kubectl port-forward -n workload-variant-autoscaler-system \
  svc/workload-variant-autoscaler-controller-manager-metrics-service 8443:8443

# In another terminal, query metrics
curl -k https://localhost:8443/metrics | grep wva_desired_replicas
```

**Example output:**
```
wva_desired_replicas{deployment="llama-8b-server",namespace="llm-inference"} 2.0
```

### Monitor HPA

```bash
# Watch HPA scaling decisions
kubectl get hpa -n <namespace> -w

# Check HPA events
kubectl describe hpa <hpa-name> -n <namespace>
```

## Step 6: Generate Load and Watch It Scale (Optional)

### For Local Testing

The Kind emulator includes a load generator:

```bash
# Start load generator
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: load-generator
  namespace: llm-d-system
spec:
  containers:
  - name: loader
    image: curlimages/curl:latest
    command: ["/bin/sh"]
    args:
      - -c
      - |
        while true; do
          curl -X POST http://vllm-emulator:8000/v1/completions \
            -H "Content-Type: application/json" \
            -d '{"model": "meta-llama/Meta-Llama-3.1-8B-Instruct", "prompt": "Hello", "max_tokens": 50}'
          sleep 0.1
        done
EOF
```

Watch scaling in action:
```bash
# Terminal 1: Watch replicas
kubectl get deployment vllm-emulator -n llm-d-system -w

# Terminal 2: Watch VariantAutoscaling
kubectl get variantautoscaling -n llm-d-system -w

# Terminal 3: Watch HPA
kubectl get hpa -n llm-d-system -w
```

### For Production

Use a benchmarking tool like [GuideLLM](tutorials/guidellm-sample.md) or hey:

```bash
# Install hey
go install github.com/rakyll/hey@latest

# Send requests
hey -z 60s -c 10 \
  -m POST \
  -H "Content-Type: application/json" \
  -d '{"model":"meta-llama/Meta-Llama-3.1-8B","prompt":"Hello","max_tokens":100}' \
  http://<service-url>:8000/v1/completions
```

## What's Next?

### Learn More

- 📖 [Configuration Guide](user-guide/configuration.md) - Customize WVA behavior
- 📖 [CRD Reference](user-guide/crd-reference.md) - Complete API documentation
- 📖 [Saturation Scaling](saturation-scaling-config.md) - Understand the optimization model
- 📖 [Metrics & Monitoring](metrics-health-monitoring.md) - Monitor WVA performance

### Tutorials

- 🎓 [Parameter Estimation](tutorials/parameter-estimation.md) - Tune for your models
- 🎓 [vLLM Integration](tutorials/vllm-samples.md) - Advanced vLLM configuration
- 🎓 [Cost Optimization](tutorials/demo.md) - Minimize infrastructure costs

### Production Checklist

Before deploying to production:

- [ ] Configure Prometheus with adequate retention
- [ ] Set up proper TLS certificates (OpenShift)
- [ ] Configure HPA stabilization windows (120s+ recommended)
- [ ] Set up monitoring and alerting on WVA metrics
- [ ] Test scaling behavior under load
- [ ] Review [Architecture Limitations](design/architecture-limitations.md)
- [ ] Configure multi-controller isolation for large deployments
- [ ] Set resource limits on WVA controller
- [ ] Configure backup and disaster recovery

### Integration Examples

- **GitOps**: Manage VariantAutoscaling with [ArgoCD](https://argo-cd.readthedocs.io/)
- **Service Mesh**: Integrate with [Istio](https://istio.io/)
- **Observability**: Export metrics to [Grafana](https://grafana.com/)
- **Cost Management**: Track costs with [Kubecost](https://www.kubecost.com/)

## Troubleshooting

### Common Issues

**Controller not starting:**
```bash
kubectl logs -n workload-variant-autoscaler-system \
  -l control-plane=controller-manager
```

**Metrics not showing up:**
- Verify Prometheus can scrape inference server metrics
- Check WVA can connect to Prometheus
- Ensure ServiceMonitor or scrape config is correct

**HPA not scaling:**
- Verify prometheus-adapter is installed and configured
- Check HPA can read custom metrics: `kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1"`

For detailed troubleshooting, see [Troubleshooting Guide](user-guide/troubleshooting.md).

## Cleanup

### Local Testing

```bash
# Delete the Kind cluster
kind delete cluster --name wva-dev
```

### Production

```bash
# Delete HPA
kubectl delete hpa <hpa-name> -n <namespace>

# Delete VariantAutoscaling
kubectl delete variantautoscaling <name> -n <namespace>

# Uninstall WVA
helm uninstall workload-variant-autoscaler \
  --namespace workload-variant-autoscaler-system

# Delete namespace (optional)
kubectl delete namespace workload-variant-autoscaler-system
```

## Getting Help

- 🐛 [Report Issues](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues)
- 💬 [GitHub Discussions](https://github.com/llm-d-incubation/workload-variant-autoscaler/discussions)
- 📚 [Full Documentation](README.md)
- 👥 [Community Meetings](https://join.slack.com/share/enQtOTg1MzkwODExNDI5Mi02NWQwOWEwOWM4Y2Y3MTc4OTQyY2Y1ZDVlZmU2MjBmZDUwNjJhZGM3MjY4ZTQ5OTdjZjgzMmI0NjI0ZTBhZTM4)

## Success!

You now have WVA running and automatically scaling your inference workloads based on saturation metrics! 🎉

Continue with the [User Guide](user-guide/installation.md) to learn more advanced features.

---

**Feedback:** Help us improve this guide by [opening an issue](https://github.com/llm-d-incubation/workload-variant-autoscaler/issues) with your experience!
