# Changelog for v0.5.0

This document details the key changes and improvements introduced in WVA v0.5.0, released as part of PR #549.

## Major Features

### 1. Pending Replica Awareness & Cascade Scaling Prevention

**Problem Solved:**
Previously, the saturation engine could repeatedly trigger scale-up for the same variant before previous scale-up operations completed, leading to over-provisioning.

**Solution:**
WVA now tracks pending replicas (pods that exist but are not yet ready) per variant and blocks scale-up for variants with pending replicas.

**Technical Details:**
- Added `PendingReplicas` field to `VariantReplicaState` interface
- `PendingReplicas = CurrentReplicas - ReadyReplicas`
- Scale-up variant selection skips variants with `PendingReplicas > 0`
- Per-variant tracking allows other variants to scale up if eligible

**Timeline Impact:**

*Before (without protection):*
```
T+0s:  Saturation detected → Scale variant-1: 2 → 3
T+30s: Pod not ready yet, saturation persists → Scale: 3 → 4
T+60s: Pods still starting → Scale: 4 → 5
T+90s: All ready, but over-provisioned by 2-3 replicas
```

*After (with protection):*
```
T+0s:  Saturation detected → Scale variant-1: 2 → 3 (PendingReplicas=1)
T+30s: variant-1 skipped (has pending), variant-2 scaled if cheaper
T+90s: variant-1 ready (PendingReplicas=0), eligible again
```

**Benefits:**
- ✅ Prevents excessive scale-up during model loading (2-7 minutes)
- ✅ Reduces infrastructure costs
- ✅ Maintains cost-optimized scaling across variants

**Documentation:**
- [Saturation Analyzer - Cascade Scaling Prevention](saturation-analyzer.md#cascade-scaling-prevention)
- [Saturation Scaling Config](saturation-scaling-config.md#how-scale-up-triggers-work)

### 2. Prometheus Configuration via Environment Variables

**Enhancement:**
WVA now supports Prometheus configuration through environment variables, providing more flexible deployment options.

**Configuration Methods:**

1. **Environment Variables (New, Recommended):**
   ```yaml
   env:
   - name: PROMETHEUS_BASE_URL
     value: "https://prometheus-k8s.monitoring.svc:9091"
   - name: PROMETHEUS_TLS_INSECURE_SKIP_VERIFY
     value: "false"
   - name: PROMETHEUS_CA_CERT_PATH
     value: "/etc/prometheus-certs/ca.crt"
   ```

2. **ConfigMap (Existing, Fallback):**
   ```yaml
   data:
     PROMETHEUS_BASE_URL: "https://prometheus-k8s.monitoring.svc:9091"
     PROMETHEUS_TLS_INSECURE_SKIP_VERIFY: "false"
   ```

**Configuration Priority:**
1. Environment variables (checked first)
2. ConfigMap values (fallback)
3. Error if neither provides `PROMETHEUS_BASE_URL`

**Available Environment Variables:**
- `PROMETHEUS_BASE_URL` - Prometheus server URL (required)
- `PROMETHEUS_TLS_INSECURE_SKIP_VERIFY` - Skip TLS verification (dev/test only)
- `PROMETHEUS_CA_CERT_PATH` - CA certificate path
- `PROMETHEUS_CLIENT_CERT_PATH` - Client certificate for mTLS
- `PROMETHEUS_CLIENT_KEY_PATH` - Client private key for mTLS
- `PROMETHEUS_SERVER_NAME` - Expected server name in TLS certificate
- `PROMETHEUS_BEARER_TOKEN` - Bearer token authentication

**Benefits:**
- ✅ Easier configuration in containerized environments
- ✅ Better secret management (use Kubernetes Secrets via env)
- ✅ Simpler Helm chart customization
- ✅ Backward compatible (ConfigMap still supported)

**Documentation:**
- [Prometheus Integration](integrations/prometheus.md#configuration)
- [Configuration Guide - Environment Variables](user-guide/configuration.md#environment-variables)

### 3. PromQL Injection Prevention

**Security Enhancement:**
Added automatic parameter escaping and validation to prevent PromQL injection attacks.

**Protection Mechanisms:**

1. **Parameter Escaping:**
   - All query parameters automatically escaped before use in PromQL
   - Backslashes: `\` → `\\`
   - Double quotes: `"` → `\"`

2. **Namespace Validation:**
   - Namespace values validated before PromQL construction
   - Prevents malicious label matchers

**Example Attack Prevention:**
```go
// Malicious input attempt
namespace := `prod",malicious="attack`

// WVA automatically escapes
escapedNamespace := EscapePromQLValue(namespace)
// Result: `prod\",malicious=\"attack`

// Safe query
query := fmt.Sprintf(`vllm_kv_cache_usage{namespace="%s"}`, escapedNamespace)
// Prometheus treats as literal string, injection blocked
```

**Why This Matters:**
- Prevents unauthorized access to metrics from other namespaces
- Blocks label injection attacks
- Ensures multi-tenant isolation

**Implementation:**
- `internal/collector/v2/query_template.go`: `EscapePromQLValue()` function
- `internal/collector/v2/prometheus_source.go`: Automatic escaping in `executeQuery()`

**Documentation:**
- [Prometheus Integration - PromQL Injection Prevention](integrations/prometheus.md#promql-injection-prevention)

## Minor Improvements

### Helper Functions

**`getVariantKey()` Function:**
- Added namespace-safe variant identification
- Format: `namespace/variantName`
- Prevents collisions when multiple namespaces have deployments with same name

**Location:** `internal/engines/saturation/engine.go`

### Type Safety Improvements

**Prometheus Query Results:**
- Added safe type assertions for Prometheus query results
- Prevents runtime panics from unexpected metric types
- Better error handling and logging

## E2E Test Improvements

### Load Generation Tuning

**Enhancements:**
- Tuned load generation parameters for consistent ~2-3 replica scale-up
- Added per-model token configuration for sustained saturation testing
- Improved test stability and reliability

**Impact:**
- More predictable E2E test outcomes
- Better validation of saturation-based scaling
- Reduced flaky test failures

## Breaking Changes

### Removed `modelProfile` Field from VariantAutoscaling CRD

**Impact:** The `modelProfile` field has been removed from the VariantAutoscaling CRD specification as it was never part of the actual CRD schema and caused validation errors.

**What Was Removed:**
- `spec.modelProfile` field and all its nested accelerator configuration
- Performance parameters (alpha, beta, gamma, delta) for model-based optimization
- Accelerator-specific configurations in VariantAutoscaling resources

**Current Supported Fields:**
```yaml
spec:
  scaleTargetRef:     # Required - target deployment reference
    kind: Deployment
    name: my-deployment
  modelID: "..."      # Required - model identifier
  variantCost: "10.0" # Optional - cost per replica (default: "10.0")
```

**Migration Required:**
If you have VariantAutoscaling resources with `modelProfile` fields, remove them before upgrading:

```bash
# Check for resources with modelProfile
kubectl get va -A -o yaml | grep -A 5 modelProfile

# Edit and remove modelProfile sections
kubectl edit va <resource-name> -n <namespace>
```

**Why This Change:**
- The `modelProfile` field was never defined in the CRD schema (api/v1alpha1/variantautoscaling_types.go)
- Kubernetes rejected resources containing this undefined field
- The model-based optimization feature that would have used these parameters is not currently implemented
- Current implementation uses saturation-based scaling, which doesn't require performance profiling

**References:**
- PR #564: Remove deprecated modelProfile field
- Issue #563: Helm chart contains deprecated VA CRD fields
- Deprecation notice: [Parameter Estimation Tutorial](tutorials/parameter-estimation.md)

## Upgrade Notes

1. **⚠️ REQUIRED: Remove `modelProfile` Field:**
   - **Action Required:** Review and update all VariantAutoscaling resources before upgrading
   - Remove any `spec.modelProfile` sections from your YAML manifests
   - Verify resources after upgrade: `kubectl get va -A -o yaml | grep modelProfile`
   - If found, edit and remove the field: `kubectl edit va <name> -n <namespace>`
   - See "Breaking Changes" section above for details

2. **Pending Replicas Tracking:**
   - Automatically enabled, no configuration required
   - May observe different scaling behavior during pod startup periods
   - This is expected and prevents over-provisioning

3. **Prometheus Configuration:**
   - Existing ConfigMap configuration continues to work
   - Consider migrating to environment variables for better secret management
   - See [Prometheus Integration docs](integrations/prometheus.md)

4. **Security:**
   - PromQL injection prevention is automatic
   - No action required, but validates multi-tenant deployment security

## Testing

All changes include comprehensive unit tests:
- `internal/saturation/analyzer_test.go` - Pending replica handling
- `internal/config/prometheus_test.go` - Environment variable config
- `internal/collector/v2/query_template.go` - Query escaping

E2E tests updated:
- `test/e2e-saturation-based/` - Enhanced load generation
- `test/e2e-openshift/` - Cross-platform validation

## Contributors

- Andrew Anderson (@clubanderson)

## References

- PR #549: https://github.com/llm-d-incubation/workload-variant-autoscaler/pull/549
- Commit: 14e2bd88 - fix: pending-aware scaling and E2E test stability improvements

---

For detailed implementation, see:
- [Saturation Analyzer Documentation](saturation-analyzer.md)
- [Prometheus Integration](integrations/prometheus.md)
- [Configuration Guide](user-guide/configuration.md)
