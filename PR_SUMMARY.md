# PR Decomposition Summary

## Quick Overview

**Total Commits**: 45 commits from `origin/refactor/single-variant-clean`
**Target**: Split into 6 sequential PRs to `upstream/main`
**Estimated Timeline**: 4-6 weeks

---

## PR Breakdown

| PR # | Name | Commits | Files | Risk | Review Time | Dependencies |
|------|------|---------|-------|------|-------------|--------------|
| **PR1** | API & CRD Modernization | 4 | ~35 | 🟢 Low | 3-5 days | None |
| **PR2** | Controller/Optimizer Refactor | 7-8 | ~25 | 🟡 Medium | 5-7 days | PR1 |
| **PR3** | Metrics Rename | 1 | ~25 | 🟢 Low | 2-3 days | PR1 |
| **PR4** | Collector & Utilities | 1-2 | ~10 | 🟢 Low | 3-4 days | PR3 |
| **PR5** | CI/E2E Infrastructure | 5 | ~8 | 🟢 Low | 2-3 days | PR1 |
| **PR6** | Scale-to-Zero | ~23 | ~30 | 🔴 High | 7-10 days | ALL |

---

## Commit Distribution

### PR1: API & CRD Modernization (4 commits)
```
e46bd4b  refactor: implement single-variant CRD architecture
d17b4d2  feat(api,controller,helm): add scaleTargetRef
1c94016  refactor(api): restructure LastUpdate struct
7a5e129  fix(ci): update CRD field verification
```
**Purpose**: Foundation - new CRD structure that everything else builds on

---

### PR2: Controller/Optimizer Refactor (7-8 commits)
```
aa18216  feat: add namespace awareness to configuration maps
702432f  feat: controller refactoring (SPLIT - non-STZ parts)
30ab963  fix(helm): values.yaml structure (SPLIT - non-STZ parts)
2d77cd6  fix(controller): handle VAs without deployments (SPLIT)
233b4b0  fix(controller): critical controller issues
853b875  test(controller): add unit tests (coverage +13%)
a25269c  test(e2e-openshift): fix VA lookup
```
**Purpose**: Controller modernization, conflict resolution, improved reliability

---

### PR3: Metrics Rename (1 commit)
```
05d8909  refactor(metrics): rename inferno_* → wva_*
```
**Purpose**: Rebrand metrics, improve HPA/KEDA integration

---

### PR4: Collector & Utilities (1-2 commits)
```
10c54f0  fix: ModelMetricsCache (SPLIT - non-retention parts)
e46bd4b  (cache implementation - may already be in PR1)
```
**Purpose**: Performance optimization via Prometheus query caching

---

### PR5: CI/E2E Infrastructure (5 commits)
```
00c0db6  test(e2e): fix ConfigMap race, ShareGPT baseline
bc926f3  fix(e2e): HPA metric discovery integration
e4e26b2  feat(e2e): enable HPA tests for KIND (infra only)
f8e7d81  refactor(e2e): make HPA tests independent
6e24c96  fix(test): prevent panic in test cleanup
```
**Purpose**: Test reliability and HPA integration infrastructure

---

### PR6: Scale-to-Zero (~23 commits)
```
39a4579  scale_to_zero configuration
ed970a9  Default retention 10m
7b4fdf0  scale-to-zero per model config
c64259f  fix ConfigMap key validation
af4ba88  error fixes
a8bc26a  feat: improve ConfigMap format for STZ
167192c  feat: add retention period validation
8a9e6f0  feat: per-model request tracking over retention
10c54f0  fix: ModelMetricsCache (SPLIT - retention parts)
96d4274  feat: zero-rate handling in optimizer
fd2ada9  fix: zero-rate handling bugs
0cbe7d2  test: edge cases for zero-rate
be27723  test: STZ ConfigMap integration
e23310b  test: comprehensive E2E for STZ flow
12bc4f7  fix(solver): zero-load allocations
702432f  feat: controller refactoring (SPLIT - STZ parts)
30ab963  fix(helm): values.yaml (SPLIT - retention parts)
2d77cd6  fix(controller): handle retention period (SPLIT)
+ 5 more ConfigMap update commits
```
**Purpose**: Complete scale-to-zero feature with retention logic

---

## Merge Flow Diagram

```
upstream/main
    ↓
[PR1: API & CRD] ────────────────────┐
    ↓                                │
[PR2: Controller/Optimizer]          │
    ↓                                │
[PR3: Metrics Rename] ───────────────┤
    ↓                                │
[PR4: Collector/Utils]               │
    ↓                                │
[PR5: CI/E2E] (can be parallel) ─────┤
    ↓                                ↓
[PR6: Scale-to-Zero] ← ALL PREVIOUS PRs
    ↓
upstream/main (complete)
```

---

## Key Features by PR

### PR1 Delivers:
✅ Single-variant CRD architecture
✅ `scaleTargetRef` field
✅ `minReplicas` / `maxReplicas`
✅ `LastUpdate` struct with delta tracking
✅ Updated CRD validation

### PR2 Delivers:
✅ Namespace-aware ConfigMaps
✅ Conflict detection & arbitration
✅ Improved controller structure
✅ Better error handling
✅ Enhanced test coverage (+13%)

### PR3 Delivers:
✅ `wva_*` metric names
✅ Improved metric labels (target_name, target_kind)
✅ Better HPA/KEDA integration
✅ Migration guide

### PR4 Delivers:
✅ Thread-safe metrics cache
✅ Reduced Prometheus query load
✅ Dynamic TTL
✅ Comprehensive cache tests

### PR5 Delivers:
✅ Fixed ConfigMap race conditions
✅ ShareGPT baseline improvements
✅ HPA metric discovery
✅ KIND test infrastructure
✅ Independent E2E tests

### PR6 Delivers:
✅ Model-level retention configuration
✅ Namespace overrides
✅ Scale-to-zero logic
✅ Cheapest-variant enforcement
✅ Zero-rate handling
✅ Comprehensive STZ tests
✅ Full documentation

---

## Commits Requiring Manual Splitting

⚠️ **3 commits must be split** between PRs:

| Original Commit | Split Into | Reason |
|-----------------|------------|--------|
| `702432f` | PR2 + PR6 | Contains both general refactoring AND scale-to-zero logic |
| `30ab963` | PR2 + PR6 | Contains both general Helm fixes AND retention period fixes |
| `2d77cd6` | PR2 + PR6 | Contains both VA-without-deployment AND retention logic |

**How to Split**: See PR_EXECUTION_GUIDE.md "Handling Split Commits" section

---

## Risk Assessment

### Low Risk ✅ (PR1, PR3, PR4, PR5)
- Well-tested functionality
- Mostly additive changes
- Clear rollback path
- Independent features

### Medium Risk ⚠️ (PR2)
- Controller logic changes
- Conflict resolution is new
- Needs careful review
- Good test coverage mitigates risk

### High Risk 🔴 (PR6)
- New scaling behavior
- Complex retention logic
- Affects production workloads
- **Mitigation**: Feature flaggable, extensive tests, last to merge

---

## Testing Strategy

### Unit Tests:
- PR1: API validation, deep copy
- PR2: Controller logic, conflict resolution, helper functions
- PR3: Metric emission
- PR4: Cache operations, thread safety
- PR5: Test utilities
- PR6: Retention logic, scale-to-zero paths, edge cases

### Integration Tests:
- PR2: Controller reconciliation
- PR4: Cache integration with controller
- PR5: E2E workflows, HPA integration
- PR6: Full scale-to-zero flow, retention scenarios

### E2E Tests:
- PR5: Infrastructure setup
- PR6: Complete scale-to-zero scenarios (KIND + OpenShift)

---

## Documentation Updates

| PR | Documentation |
|----|---------------|
| PR1 | CRD reference, User guide, Replica bounds feature doc |
| PR2 | Conflict resolution feature doc |
| PR3 | Prometheus integration, Metrics labeling architecture, Migration guide |
| PR4 | Metrics caching developer guide (non-retention) |
| PR5 | E2E test README |
| PR6 | Scale-to-zero feature doc, Retention sections in caching guide |

---

## Breaking Changes

### PR1:
- ⚠️ CRD structure change (multi-variant → single-variant)
- Migration path: One VA per variant instead of array

### PR3:
- ⚠️ Metric names changed (`inferno_*` → `wva_*`)
- ⚠️ Metric labels changed
- Migration path: Update Prometheus Adapter, HPA/KEDA configs

### PR6:
- ⚠️ New scaling behavior (scale-to-zero)
- Migration path: Opt-in via ConfigMap, default disabled

---

## Success Metrics

After all PRs merge:

✅ **Feature Parity**: All features from `origin/refactor/single-variant-clean` on `upstream/main`
✅ **Test Coverage**: ≥ 60% controller coverage, all E2E tests passing
✅ **Performance**: Reduced Prometheus queries via caching
✅ **Reliability**: Conflict resolution prevents multi-VA issues
✅ **Scalability**: Scale-to-zero reduces idle resource consumption
✅ **Documentation**: Complete user and developer guides

---

## Next Steps

1. Review PR_DECOMPOSITION_PLAN.md for detailed breakdown
2. Review PR_EXECUTION_GUIDE.md for step-by-step commands
3. Start with PR1 creation
4. Follow merge order strictly
5. Celebrate after PR6 merges! 🎉

