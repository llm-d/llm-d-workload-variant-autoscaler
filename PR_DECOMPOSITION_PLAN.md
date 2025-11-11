# PR Decomposition Plan: refactor/single-variant-clean → upstream/main

## Overview
Total commits to split: 45 commits
Target: 6 PRs in specific order to minimize risk and review complexity

---

## PR1: API & CRD Modernization
**Branch**: `pr/api-crd-modernization`
**Target**: First to merge (foundation for all other PRs)

### Commits to Include:
1. `e46bd4b` - refactor: implement single-variant CRD architecture with enhanced features
2. `d17b4d2` - feat(api,controller,helm): add scaleTargetRef and conflict resolution with arbitration
3. `1c94016` - refactor(api): restructure LastUpdate as struct with NumReplicasChanged delta field
4. `7a5e129` - fix(ci): update CRD field verification for nested LastUpdateInfo structure

### Scope:
- ✅ Single-variant CRD spec/status structure
- ✅ Add `scaleTargetRef` field (Kind, Name, APIVersion)
- ✅ Add `minReplicas` and `maxReplicas` fields
- ✅ LastUpdate struct with `NumReplicasChanged` delta field
- ✅ Make `variantCost` optional (default "10")
- ✅ CRD validation updates
- ✅ Generated code (zz_generated.deepcopy.go)
- ✅ API type tests
- ✅ Update CRD YAML manifests
- ✅ Basic Helm chart updates for CRD
- ✅ Sample YAML updates
- ✅ Documentation: CRD reference, user guide

### What to Stub/Defer:
- ⏸️ Controller logic stays simple (no retention, no advanced conflict resolution yet)
- ⏸️ Scale-to-zero specific fields can be added but not activated
- ⏸️ Conflict arbitration logic deferred to PR2

### Files Changed (~35 files):
- `api/v1alpha1/variantautoscaling_types.go`
- `api/v1alpha1/zz_generated.deepcopy.go`
- `api/v1alpha1/variantautoscaling_types_test.go`
- `config/crd/bases/llmd.ai_variantautoscalings.yaml`
- `config/samples/*.yaml`
- `charts/workload-variant-autoscaler/crds/*.yaml`
- `docs/user-guide/crd-reference.md`
- `docs/features/replica-bounds.md` (new)

---

## PR2: Controller/Optimizer Refactor (Non Scale-to-Zero)
**Branch**: `pr/controller-optimizer-refactor`
**Target**: Second to merge (depends on PR1)

### Commits to Include:
1. `aa18216` - feat: add namespace awareness to configuration maps
2. `d17b4d2` - feat(api,controller,helm): add scaleTargetRef and conflict resolution (controller parts)
3. `30ab963` - fix(helm): correct values.yaml structure (non-retention parts)
4. `2d77cd6` - fix(controller): handle VAs without deployments and retention period (non-retention parts)
5. `702432f` - feat: controller refactoring (SPLIT: non scale-to-zero parts only)
6. `233b4b0` - fix(controller): fix critical controller issues and add comprehensive test coverage
7. `853b875` - test(controller): add unit tests to improve controller coverage from 53.9% to 66.9%
8. `a25269c` - test(e2e-openshift): fix VA lookup to use scaleTargetRef instead of deployment name

### Scope:
- ✅ Namespace-aware ConfigMap handling
- ✅ Conflict detection (`detectDuplicateDeploymentTargets`)
- ✅ Conflict resolution with arbitration (`resolveDeploymentConflicts`)
- ✅ Conflict status conditions update (`updateConflictConditions`)
- ✅ Updated reconcile pipeline structure
- ✅ scaleTargetRef integration in controller
- ✅ Optimizer interface updates
- ✅ Actuator wiring for scaleTargetRef
- ✅ Internal interfaces cleanup
- ✅ Unit tests for conflict resolution
- ✅ Unit tests for helper functions (non-retention)
- ✅ Fallback allocation logic (non-retention paths)

### What to Stub/Defer:
- ⏸️ Retention period logic → PR6
- ⏸️ Scale-to-zero specific paths → PR6
- ⏸️ ModelMetricsCache retention tracking → PR6

### Files Changed (~25 files):
- `internal/controller/variantautoscaling_controller.go`
- `internal/controller/variantautoscaling_controller_test.go`
- `internal/controller/variantautoscaling_controller_test_new.go`
- `internal/controller/variantautoscaling_fixes_test.go`
- `internal/actuator/actuator.go`
- `internal/optimizer/optimizer.go`
- `internal/interfaces/types.go`
- `charts/workload-variant-autoscaler/values.yaml`
- `docs/features/conflict-resolution.md` (new)

---

## PR3: Metrics & Telemetry Refresh
**Branch**: `pr/metrics-rename`
**Target**: Third to merge (independent of PR2, but easier after)

### Commits to Include:
1. `05d8909` - refactor(metrics): rename inferno metrics to wva and update labels

### Scope:
- ✅ Rename `inferno_*` → `wva_*` metrics
- ✅ Label changes: remove `variant_name`, `variant_id`; add `target_name`, `target_kind`
- ✅ Update metric constants
- ✅ Update metrics emitter
- ✅ Update Prometheus Adapter configs
- ✅ Update HPA/KEDA examples
- ✅ Update Helm charts
- ✅ Update deployment scripts
- ✅ Update E2E test helpers
- ✅ Documentation updates
- ✅ Migration guide

### Files Changed (~25 files):
- `internal/constants/metrics.go`
- `internal/metrics/metrics.go`
- `internal/actuator/actuator.go`
- `config/samples/prometheus-adapter-*.yaml`
- `config/samples/hpa-*.yaml`
- `charts/workload-variant-autoscaler/templates/hpa.yaml`
- `deploy/*/install.sh`
- `test/utils/e2eutils.go`
- `test/e2e/e2e_test.go`
- `docs/integrations/prometheus.md`
- `docs/architecture/metrics-labeling.md` (new)

---

## PR4: Collector & Utility Foundations
**Branch**: `pr/collector-utilities`
**Target**: Fourth to merge (depends on PR3 for metric names)

### Commits to Include:
1. `10c54f0` - fix: critical bug fixes and comprehensive tests for ModelMetricsCache (non-retention parts)
2. Parts of `e46bd4b` - Model metrics cache implementation (already in PR1 but ensure wiring)

### Scope:
- ✅ ModelMetricsCache (general Prometheus query caching, NOT retention-specific)
- ✅ Thread-safe cache with RWMutex
- ✅ Dynamic TTL based on reconciliation interval
- ✅ Cache cleanup mechanism
- ✅ Internal metrics parsing utilities
- ✅ Utils backoff helpers
- ✅ Comprehensive cache tests (non-retention)
- ✅ Wire cache into controller (with feature flag for scale-to-zero)

### What to Stub/Defer:
- ⏸️ ScaleToZeroMetricsCache → PR6
- ⏸️ Retention-specific cache tracking → PR6
- ⏸️ Per-model request tracking over retention period → PR6

### Files Changed (~10 files):
- `internal/collector/collector.go`
- `internal/collector/collector_cache_test.go`
- `internal/collector/model_metrics_cache.go`
- `internal/collector/model_metrics_cache_test.go`
- `internal/utils/utils.go`
- `internal/controller/variantautoscaling_controller.go` (cache wiring)
- `docs/developer-guide/metrics-caching.md` (general caching, non-retention)

---

## PR5: CI/E2E Infrastructure (Non Scale-to-Zero)
**Branch**: `pr/ci-e2e-improvements`
**Target**: Fifth to merge (can be parallel with PR4)

### Commits to Include:
1. `00c0db6` - test(e2e): fix ConfigMap race condition, ShareGPT baseline, and increase load duration
2. `bc926f3` - fix(e2e): complete HPA metric discovery integration with Prometheus Adapter
3. `e4e26b2` - feat(e2e): enable HPA scale-to-zero tests for KIND clusters (infrastructure parts, not scale-to-zero logic)
4. `f8e7d81` - refactor(e2e): make HPA scale-to-zero tests fully independent for CI/CD
5. `6e24c96` - fix(test): prevent panic in HPA scale-to-zero test AfterAll when test is skipped

### Scope:
- ✅ Fix ConfigMap race condition in tests
- ✅ ShareGPT baseline adjustments
- ✅ Increase load duration for reliability
- ✅ HPA metric discovery integration
- ✅ Prometheus Adapter setup improvements
- ✅ KIND cluster setup for HPA tests
- ✅ Test independence and CI/CD readiness
- ✅ Test reliability improvements

### What to Defer:
- ⏸️ Scale-to-zero specific test logic → PR6
- ⏸️ Retention period test scenarios → PR6

### Files Changed (~8 files):
- `test/e2e/e2e_test.go`
- `test/e2e/e2e_suite_test.go`
- `test/e2e/e2e_hpa_scale_to_zero_test.go` (infrastructure only)
- `test/e2e-openshift/sharegpt_scaleup_test.go`
- `test/e2e-openshift/hpa_scale_to_zero_test.go` (infrastructure only)
- `test/utils/e2eutils.go`
- `.github/workflows/*` (if applicable)

---

## PR6: Scale-to-Zero (Last to Merge)
**Branch**: `pr/scale-to-zero`
**Target**: Last to merge (highest risk, most complex)

### Commits to Include:
ALL scale-to-zero specific commits:
1. `39a4579` - scale_to_zero configuration
2. `ed970a9` - Default retention 10m
3. `7b4fdf0` - scale-to-zero per model config
4. `c64259f` - fix ConfigMap key validation in controller tests
5. `af4ba88` - error fixes
6. `a8bc26a` - feat: improve ConfigMap format for scale-to-zero configuration
7. `167192c` - feat: improve ConfigMap format and add retention period validation
8. `8a9e6f0` - feat: add internal per-model request tracking over retention period
9. `10c54f0` - fix: critical bug fixes for ModelMetricsCache (RETENTION-SPECIFIC PARTS)
10. `96d4274` - feat: implement zero-rate handling in optimizer with intelligent variant selection
11. `fd2ada9` - fix: critical bug fixes and improvements for zero-rate handling optimizer
12. `0cbe7d2` - test: add comprehensive edge case tests for zero-rate handling
13. `be27723` - test: fix Scale-to-Zero ConfigMap integration tests
14. `e23310b` - test: add comprehensive E2E integration test for scale-to-zero flow
15. `12bc4f7` - fix(solver): set accelerator name in zero-load allocations to prevent solver failure
16. `30ab963` - fix(helm): correct values.yaml structure (RETENTION PARTS)
17. `2d77cd6` - fix(controller): handle VAs without deployments and retention period (RETENTION PARTS)
18. `702432f` - feat: controller refactoring (SCALE-TO-ZERO PARTS)
19. All ConfigMap sample updates (commits `4cb4efd` through `cd55adc`)
20. `664d2f1` - chore: remove FEATURE_SUMMARY.md file

### Scope:
- ✅ Model-level retention ConfigMap format and validation
- ✅ Namespace overrides for scale-to-zero config
- ✅ ScaleToZeroMetricsCache implementation
- ✅ Per-model request tracking over retention period
- ✅ Retention period logic in controller
- ✅ `applyRetentionPeriodScaling` function
- ✅ `isRetentionPeriodExceeded` checks
- ✅ Cheapest-variant enforcement when scaling to zero
- ✅ Zero-rate handling in optimizer
- ✅ Solver fixes for zero-load allocations
- ✅ Bootstrap fixes for scale-to-zero scenarios
- ✅ E2E tests for scale-to-zero flow
- ✅ OpenShift scale-to-zero tests
- ✅ KIND scale-to-zero tests
- ✅ ConfigMap integration tests
- ✅ Edge case tests for zero-rate handling
- ✅ Documentation: `docs/features/scale-to-zero.md`
- ✅ Documentation: retention-specific parts of `docs/developer-guide/metrics-caching.md`
- ✅ Sample: `config/samples/model-scale-to-zero-config.yaml`
- ✅ Prometheus Adapter tweaks for scale-to-zero metrics

### Files Changed (~30 files):
- `internal/controller/variantautoscaling_controller.go` (retention logic)
- `internal/collector/model_metrics_cache.go` (retention tracking)
- `internal/collector/scale_to_zero_metrics_cache.go` (new)
- `internal/optimizer/optimizer.go` (zero-rate handling)
- `internal/solver/greedy.go` (zero-load fixes)
- `internal/utils/utils.go` (scale-to-zero helpers)
- `internal/utils/scale_to_zero_test.go`
- `test/e2e/e2e_hpa_scale_to_zero_test.go` (full logic)
- `test/e2e-openshift/hpa_scale_to_zero_test.go` (full logic)
- `config/samples/model-scale-to-zero-config.yaml`
- `docs/features/scale-to-zero.md`
- `docs/developer-guide/metrics-caching.md` (retention sections)

---

## Merge Ordering & Dependencies

```
PR1 (API & CRD)
    ↓
PR2 (Controller/Optimizer) ← depends on PR1
    ↓
PR3 (Metrics) ← independent but easier after PR2
    ↓
PR4 (Collector/Utils) ← depends on PR3 (metric names)
    ↓
PR5 (CI/E2E) ← can be parallel with PR4
    ↓
PR6 (Scale-to-Zero) ← depends on ALL previous PRs
```

---

## Action Plan

### Step 1: Create PR1 Branch
```bash
git checkout upstream/main
git checkout -b pr/api-crd-modernization
git cherry-pick e46bd4b d17b4d2 1c94016 7a5e129
# Review, test, push
```

### Step 2: Create PR2 Branch (from PR1)
```bash
git checkout pr/api-crd-modernization
git checkout -b pr/controller-optimizer-refactor
git cherry-pick aa18216 <controller parts of d17b4d2> ...
# May need to manually split commits
```

### Step 3-6: Continue sequentially

---

## Notes & Warnings

### Commits Requiring Manual Splitting:
1. **`702432f`** - Contains both controller refactoring AND scale-to-zero
   - Need to split into two commits manually
   - PR2 gets: general refactoring, conflict resolution
   - PR6 gets: retention logic, scale-to-zero paths

2. **`30ab963`** - Helm fixes include both general and retention-specific
   - PR2 gets: general values.yaml structure fixes
   - PR6 gets: retention period bug fixes

3. **`2d77cd6`** - Handle VAs without deployments AND retention period
   - PR2 gets: VA without deployment handling
   - PR6 gets: retention period handling

### Testing Strategy:
- Each PR must pass all existing tests
- PR1-5: Feature-flag scale-to-zero paths as disabled
- PR6: Enable scale-to-zero and add comprehensive tests

### Risk Mitigation:
- PR1-5 should not change existing behavior (except improvements)
- PR6 is the only PR that introduces new scaling behavior
- Heavy focus on test coverage for PR6

---

## Estimated Timeline

| PR | Review Complexity | Est. Review Time | Risk Level |
|----|------------------|------------------|------------|
| PR1 | Medium | 3-5 days | Low (API changes, well-documented) |
| PR2 | High | 5-7 days | Medium (controller logic) |
| PR3 | Low | 2-3 days | Low (mechanical rename) |
| PR4 | Medium | 3-4 days | Low (utilities, well-tested) |
| PR5 | Low | 2-3 days | Low (test infrastructure) |
| PR6 | Very High | 7-10 days | High (new behavior, complex) |

**Total**: ~4-6 weeks if done sequentially, ~3-4 weeks with some parallelization

