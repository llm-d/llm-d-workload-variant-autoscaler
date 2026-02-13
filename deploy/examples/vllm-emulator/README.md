# vLLM Emulator Deployment

This directory contains deployment manifests for the vLLM emulator, used for testing WVA without real GPU hardware.

## Overview

The vLLM emulator simulates vLLM server behavior including:
- OpenAI-compatible API endpoints
- Prometheus metrics (vllm:* metrics)
- Configurable latency and throughput
- Request queueing simulation

## Deployment

### Quick Deploy

```bash
# Deploy emulator with default configuration
./deploy.sh
```

### Manual Deployment

**1. Deploy emulator:**
```bash
kubectl apply -f vllme-setup/vllme-deployment-with-service-and-servicemon.yaml
```

**2. Create VariantAutoscaling resource:**
```bash
kubectl apply -f vllme-setup/vllme-variantautoscaling.yaml
```

**3. Deploy with llm-d integration:**
```bash
kubectl apply -f integration_llm-d/vllme-inferencemodel.yaml
```

## Configuration

### Emulator Deployment

Key configuration in `vllme-setup/vllme-deployment-with-service-and-servicemon.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vllme-deployment
  namespace: llm-d-sim
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: vllme
        image: <vllme-image>
        ports:
        - containerPort: 8000
          name: http
        env:
        - name: MODEL_NAME
          value: "default/default"
```

### VariantAutoscaling Resource

Example in `vllme-setup/vllme-variantautoscaling.yaml`:

```yaml
apiVersion: llmd.ai/v1alpha1
kind: VariantAutoscaling
metadata:
  name: vllme-deployment
  namespace: llm-d-sim
spec:
  modelName: "default/default"
  serviceClass: "Premium"
  acceleratorType: "A100"
  minReplicas: 1
  maxBatchSize: 256
```

## Testing

### Access the Emulator

**Port-forward service:**
```bash
kubectl port-forward -n llm-d-sim svc/vllme-service 8000:80
```

**Check metrics endpoint:**
```bash
curl http://localhost:8000/metrics
```

Should see metrics like:
```
vllm:requests_count_total
vllm:requests_duration_seconds
vllm:queue_size
```

### Generate Load

Use the load generator from `tools/vllm-emulator`:

```bash
cd ../../../tools/vllm-emulator

# Install dependencies
pip install -r requirements.txt

# Run load test
python loadgen.py \
  --model default/default \
  --rate '[[120, 60]]' \
  --url http://localhost:8000/v1 \
  --content 50
```

**Load patterns:**
```bash
# Constant load: 60 requests/min for 120 seconds
--rate '[[120, 60]]'

# Increasing load: 60 req/min, then 80 req/min
--rate '[[120, 60], [120, 80]]'

# Spike pattern
--rate '[[60, 20], [60, 100], [60, 20]]'
```

## Monitoring

### Watch Scaling

```bash
# Watch deployments
watch kubectl get deploy -n llm-d-sim

# Watch VariantAutoscaling status
watch kubectl get variantautoscalings.llmd.ai -n llm-d-sim

# View controller decisions
kubectl logs -n workload-variant-autoscaler-system \
  -l control-plane=controller-manager -f
```

### Prometheus Queries

Access Prometheus:
```bash
kubectl port-forward -n workload-variant-autoscaler-monitoring \
  svc/prometheus-operated 9090:9090
```

**Example queries:**
```promql
# Request rate (per minute)
sum(rate(vllm:requests_count_total[1m])) * 60

# Average latency
vllm:requests_duration_seconds

# Queue size
vllm:queue_size

# WVA optimized replicas
wva_optimized_replicas{deployment="vllme-deployment"}
```

## Integration with llm-d

### Using EPP (Inference Scheduler)

Deploy with llm-d infrastructure:

```bash
# Apply integration manifests
kubectl apply -f integration_llm-d/
```

### ARM64 GAIE Simulation

For ARM64 clusters with GAIE simulator:

```bash
# Deploy with GAIE values
helm install ... -f integration_llm-d/arm64-gaie-sim-values.yaml
```

## Directory Structure

```
vllm-emulator/
├── README.md
├── deploy.sh                          # Deployment script
├── vllme-setup/                       # Standalone setup
│   ├── vllme-deployment-with-service-and-servicemon.yaml
│   └── vllme-variantautoscaling.yaml
├── integration_llm-d/                 # llm-d integration
│   ├── arm64-gaie-sim-values.yaml
│   └── vllme-inferencemodel.yaml
└── prometheus-operator/               # Prometheus setup
    ├── prometheus-deploy-all-in-one.yaml
    └── prometheus-tls-values.yaml
```

## Prometheus Setup

### Deploy Prometheus Operator

```bash
kubectl apply -f prometheus-operator/prometheus-deploy-all-in-one.yaml
```

### With TLS

```bash
helm install kube-prometheus-stack \
  -f prometheus-operator/prometheus-tls-values.yaml \
  -n workload-variant-autoscaler-monitoring \
  prometheus-community/kube-prometheus-stack
```

## Troubleshooting

### Emulator Not Starting

```bash
# Check pod status
kubectl get pods -n llm-d-sim

# View logs
kubectl logs -n llm-d-sim deployment/vllme-deployment

# Check events
kubectl describe pod -n llm-d-sim -l app=vllme
```

### Metrics Not Appearing

```bash
# Verify ServiceMonitor
kubectl get servicemonitor -n llm-d-sim

# Check Prometheus targets
# Access Prometheus UI and check Status -> Targets

# Verify metrics endpoint
kubectl exec -n llm-d-sim deploy/vllme-deployment -- curl localhost:8000/metrics
```

### Load Generator Issues

```bash
# Test connectivity
curl http://localhost:8000/health

# Check if port-forward is active
lsof -i :8000

# Verify request format
curl -X POST http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "default/default",
    "messages": [{"role": "user", "content": "test"}]
  }'
```

## Clean Up

```bash
# Delete emulator deployment
kubectl delete -f vllme-setup/

# Delete integration resources
kubectl delete -f integration_llm-d/

# Delete namespace (removes everything)
kubectl delete namespace llm-d-sim
```

## Next Steps

- [Load Generator Documentation (GuideLLM)](../../../docs/tutorials/guidellm-sample.md)
- [Testing Guide](../../../docs/developer-guide/testing.md)
- [HPA Integration](../../../docs/integrations/hpa-integration.md)
- [Kind Emulator Setup](../../kind-emulator/README.md)

