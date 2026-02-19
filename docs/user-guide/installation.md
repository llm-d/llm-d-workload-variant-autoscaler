# Installation Guide

This guide covers installing Workload-Variant-Autoscaler (WVA) on your Kubernetes cluster.

## Prerequisites

- Kubernetes v1.32.0 or later
- Helm 3.x
- kubectl configured to access your cluster
- Cluster admin privileges

## Installation Methods

### Option 1: Helm Installation (Recommended, on OpenShift)
Before running, be sure to delete all previous helm installations for `workload-variant-autoscaler` and `prometheus-adapter`. To list all helm charts installed in the cluster run `helm ls -A`.


#### Step 1: Setup Variables, Secret, Helm repo
```
export OWNER="llm-d"
export WVA_PROJECT="llm-d-workload-variant-autoscaler"
export WVA_RELEASE="v0.5.0"
export WVA_NS="workload-variant-autoscaler-system"
export MON_NS="openshift-user-workload-monitoring"

kubectl get secret thanos-querier-tls -n openshift-monitoring -o jsonpath='{.data.tls\.crt}' | base64 -d > /tmp/prometheus-ca.crt

git clone -b $WVA_RELEASE -- https://github.com/$OWNER/$WVA_PROJECT.git $WVA_PROJECT
cd $WVA_PROJECT
export WVA_PROJECT=$PWD
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
```

#### Step 2: Update prometheus-adapter To Export WVA Metrics
**Important:** The following helm upgrade command updates the global `prometheus-adapter` 
configmap. If this is a shared cluster then you might want to get the current
settings, manually append the values in `config/samples/prometheus-adapter-values-ocp.yaml`
then run helm upgrade with the appended values. Here's an example how to get the current
values: `kubectl get configmap prometheus-adapter -n $MON_NS -o yaml`

```
helm upgrade -i prometheus-adapter prometheus-community/prometheus-adapter \
  -n $MON_NS \
  -f config/samples/prometheus-adapter-values-ocp.yaml
```

#### Step 3: Install WVA Controller Into a Namespace
```
kubectl apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $WVA_NS
  labels:
    app.kubernetes.io/name: workload-variant-autoscaler
    control-plane: controller-manager
    openshift.io/user-monitoring: "true"
EOF

cd $WVA_PROJECT/charts
helm upgrade -i workload-variant-autoscaler ./workload-variant-autoscaler \
  -n $WVA_NS \
  --set-file wva.prometheus.caCert=/tmp/prometheus-ca.crt \
  --set controller.enabled=true \
  --set va.enabled=false \
  --set hpa.enabled=false \
  --set vllmService.enabled=false
```

#### Step 4: Add A Model To WVA Controller
After a WVA controller has been installed,
you can add one or more models running in LLMD namespaces as scale targets to the WVA controller. As an example, the following command adds model name `my-model-a` with model ID `meta-llama/Llama-3.1-8` running in `team-a` LLMD namespace. This command creates the corresponding VA, HPA resources in `team-a` namespace.
```
helm upgrade -i wva-model-a ./workload-variant-autoscaler \
  -n $WVA_NS \
  --set controller.enabled=false \
  --set va.enabled=true \
  --set hpa.enabled=true \
  --set llmd.namespace=team-a \
  --set llmd.modelName=my-model-a \
  --set llmd.modelID="meta-llama/Llama-3.1-8"
```
Here is an example to add another model to the same WVA controller:
```
helm upgrade -i wva-model-b ./workload-variant-autoscaler \
  -n $WVA_NS \
  --set controller.enabled=false \
  --set va.enabled=true \
  --set hpa.enabled=true \
  --set llmd.namespace=team-a \
  --set llmd.modelName=my-model-b \
  --set llmd.modelID="Qwen/Qwen3-0.6B"
```

**Verify the installation:**
```bash
kubectl get pods -n workload-variant-autoscaler-system
```

### Option 2: Kustomize Installation

Using kustomize for more control:

```bash
# Install CRDs
make install

# Deploy the controller
make deploy IMG=quay.io/llm-d/workload-variant-autoscaler:latest
```

### Option 3: Using Installation Scripts

For specific platforms:

**Kubernetes:**
```bash
cd deploy/kubernetes
./install.sh
```

**OpenShift:**
```bash
cd deploy/openshift
./install.sh
```

### Option 4: Local Development (Kind Emulator):
```bash
# Deploy WVA with llm-d infrastructure on a local Kind cluster
make deploy-wva-emulated-on-kind CREATE_CLUSTER=true DEPLOY_LLM_D=true

# This creates a Kind cluster with emulated GPUs and deploys:
# - WVA controller
# - llm-d infrastructure (simulation mode)
# - Prometheus and monitoring stack
# - vLLM emulator for testing
# See deploy/kind-emulator/README.md for detailed instructions
```

## Configuration

### Helm Values

Key configuration options:

```yaml
# custom-values.yaml
image:
  repository: quay.io/llm-d/workload-variant-autoscaler
  tag: latest
  pullPolicy: IfNotPresent

resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi

# Enable Prometheus monitoring
prometheus:
  enabled: true
  servicemonitor:
    enabled: true

# Optional: Multi-controller isolation
# Set a unique identifier for this controller instance
# Useful for parallel testing or multi-tenant environments
# See docs/user-guide/multi-controller-isolation.md
wva:
  controllerInstance: ""  # Leave empty for single controller
```

### ConfigMaps

WVA uses ConfigMaps for cluster configuration:

- **Service Classes**: SLO definitions for different service tiers

See [Configuration Guide](configuration.md) for details.

## Integrating with HPA/KEDA

WVA can work with existing autoscalers:

**For HPA integration:**
See [HPA Integration Guide](../integrations/hpa-integration.md)

**For KEDA integration:**
See [KEDA Integration Guide](../integrations/keda-integration.md)

## Verifying Installation

1. **Check controller is running:**
   ```bash
   kubectl get deployment -n workload-variant-autoscaler-system
   ```

2. **Verify CRDs are installed:**
   ```bash
   kubectl get crd variantautoscalings.llmd.ai
   ```

3. **Check controller logs:**
   ```bash
   kubectl logs -n workload-variant-autoscaler-system \
     deployment/workload-variant-autoscaler-controller-manager
   ```

## Uninstallation

**Helm:**
```bash
helm uninstall workload-variant-autoscaler -n workload-variant-autoscaler-system
```

**Kustomize:**
```bash
make undeploy
make uninstall  # Remove CRDs
```

## Troubleshooting

### Common Issues

**Controller not starting:**
- Check if CRDs are installed: `kubectl get crd`
- Verify RBAC permissions
- Check controller logs for errors

**Metrics not appearing:**
- Ensure Prometheus ServiceMonitor is created
- Verify Prometheus has proper RBAC to scrape metrics
- Check network policies aren't blocking metrics endpoint

**See Also:**
- [Configuration Guide](configuration.md)
- [Troubleshooting Guide](troubleshooting.md) (coming soon)
- [Developer Guide](../developer-guide/development.md)

## Next Steps

- [Configure your first VariantAutoscaling resource](configuration.md)
- [Follow the Quick Start Demo](../tutorials/demo.md)
- [Set up integration with HPA](../integrations/hpa-integration.md)

