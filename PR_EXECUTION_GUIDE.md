# PR Execution Guide

Quick reference for creating and managing the 6 PRs to upstream/main.

## Prerequisites

```bash
# Ensure you're on the latest
git fetch upstream
git fetch origin

# Verify starting point
git log upstream/main..origin/refactor/single-variant-clean --oneline | wc -l
# Should show 45 commits
```

---

## PR1: API & CRD Modernization

### Create Branch
```bash
git checkout upstream/main
git checkout -b pr/api-crd-modernization

# Cherry-pick commits
git cherry-pick e46bd4b  # refactor: implement single-variant CRD architecture
git cherry-pick d17b4d2  # feat(api,controller,helm): add scaleTargetRef and conflict resolution
git cherry-pick 1c94016  # refactor(api): restructure LastUpdate as struct
git cherry-pick 7a5e129  # fix(ci): update CRD field verification

# Test
make test
make test-e2e  # If applicable

# Push
git push origin pr/api-crd-modernization
```

### Create PR
- **Title**: `refactor(api): modernize CRD with single-variant architecture and scaleTargetRef`
- **Base**: `upstream/main`
- **Description**: Use PR_DECOMPOSITION_PLAN.md PR1 section

---

## PR2: Controller/Optimizer Refactor

### Create Branch
```bash
git checkout pr/api-crd-modernization
git checkout -b pr/controller-optimizer-refactor

# Cherry-pick commits
git cherry-pick aa18216  # feat: add namespace awareness to configuration maps

# NOTE: Need to manually split these commits:
# For 702432f, 30ab963, 2d77cd6 - extract only non-scale-to-zero parts

# Option 1: Interactive rebase to split
git cherry-pick -n 702432f
# Manually stage only non-scale-to-zero files
git add <non-scale-to-zero-files>
git commit -m "feat(controller): refactor controller architecture (non scale-to-zero)"

# Option 2: Cherry-pick and revert scale-to-zero parts
git cherry-pick 702432f
git revert --no-commit 702432f
# Restore only non-scale-to-zero files
git checkout HEAD~1 -- <non-scale-to-zero-files>
git commit -m "Revert scale-to-zero parts from 702432f"

# Continue with other commits
git cherry-pick 233b4b0  # fix(controller): fix critical controller issues
git cherry-pick 853b875  # test(controller): add unit tests
git cherry-pick a25269c  # test(e2e-openshift): fix VA lookup

# Test
make test
make test-e2e

# Push
git push origin pr/controller-optimizer-refactor
```

### Files to Include (non-scale-to-zero only):
- Controller: conflict detection, conflict resolution, namespace-aware ConfigMaps
- Exclude: retention logic, scale-to-zero paths, ModelMetricsCache retention tracking

### Create PR
- **Title**: `refactor(controller): add conflict resolution and namespace-aware configuration`
- **Base**: `pr/api-crd-modernization` (or `upstream/main` after PR1 merges)

---

## PR3: Metrics Rename

### Create Branch
```bash
git checkout pr/api-crd-modernization  # or upstream/main after PR1 merges
git checkout -b pr/metrics-rename

# Single commit
git cherry-pick 05d8909  # refactor(metrics): rename inferno to wva

# Test
make test
make test-e2e

# Push
git push origin pr/metrics-rename
```

### Create PR
- **Title**: `refactor(metrics): rename inferno_* metrics to wva_* with improved labels`
- **Base**: `pr/api-crd-modernization` (or after PR1/PR2 merge)

---

## PR4: Collector & Utilities

### Create Branch
```bash
git checkout pr/metrics-rename  # or upstream/main after PR3 merges
git checkout -b pr/collector-utilities

# Cherry-pick non-retention cache parts
git cherry-pick -n 10c54f0  # Only non-retention parts
# Manually exclude retention-specific code
git add internal/collector/model_metrics_cache.go
git add internal/collector/model_metrics_cache_test.go
# Exclude: internal/collector/scale_to_zero_metrics_cache.go
git commit -m "feat(collector): add model metrics cache for Prometheus query optimization"

# Test
make test

# Push
git push origin pr/collector-utilities
```

### Create PR
- **Title**: `feat(collector): implement thread-safe model metrics cache`
- **Base**: `pr/metrics-rename` (or after PR3 merges)

---

## PR5: CI/E2E Infrastructure

### Create Branch
```bash
git checkout pr/api-crd-modernization  # Can be parallel with PR4
git checkout -b pr/ci-e2e-improvements

# Cherry-pick commits
git cherry-pick 00c0db6  # test(e2e): fix ConfigMap race condition
git cherry-pick bc926f3  # fix(e2e): complete HPA metric discovery
git cherry-pick e4e26b2  # feat(e2e): enable HPA tests for KIND (infrastructure only)
git cherry-pick f8e7d81  # refactor(e2e): make HPA tests independent
git cherry-pick 6e24c96  # fix(test): prevent panic

# May need to remove scale-to-zero test logic, keep only infrastructure
git cherry-pick -n e4e26b2
# Edit test files to stub out scale-to-zero specific test cases
git add test/e2e/e2e_hpa_scale_to_zero_test.go  # infrastructure only
git commit -m "feat(e2e): add HPA test infrastructure for KIND clusters"

# Test
make test-e2e

# Push
git push origin pr/ci-e2e-improvements
```

### Create PR
- **Title**: `test(e2e): improve test reliability and HPA integration`
- **Base**: Can merge after PR1, independent of PR2-4

---

## PR6: Scale-to-Zero (Final)

### Create Branch
```bash
# Wait for PR1-5 to merge first!
git checkout upstream/main
git pull upstream main
git checkout -b pr/scale-to-zero

# Cherry-pick ALL scale-to-zero commits in order
git cherry-pick 39a4579  # scale_to_zero configuration
git cherry-pick ed970a9  # Default retention 10m
git cherry-pick 7b4fdf0  # scale-to-zero per model config
git cherry-pick c64259f  # fix ConfigMap key validation
git cherry-pick af4ba88  # error fixes
git cherry-pick a8bc26a  # improve ConfigMap format
git cherry-pick 167192c  # add retention period validation
git cherry-pick 8a9e6f0  # add internal per-model request tracking
git cherry-pick 96d4274  # implement zero-rate handling
git cherry-pick fd2ada9  # fix zero-rate handling
git cherry-pick 0cbe7d2  # test: edge cases for zero-rate
git cherry-pick be27723  # test: ConfigMap integration
git cherry-pick e23310b  # test: E2E integration
git cherry-pick 12bc4f7  # fix(solver): zero-load allocations

# Cherry-pick retention-specific parts from split commits
# May need manual intervention for 702432f, 30ab963, 2d77cd6

# Cherry-pick all ConfigMap update commits
git cherry-pick 4cb4efd 654e8b5 dafd74e cd55adc 8a58765 bc89c56 d2f9dd8 5e89f87 940ab48

# Add scale-to-zero specific cache
git cherry-pick <retention parts of 10c54f0>

# Test extensively
make test
make test-e2e
make test-e2e-openshift

# Push
git push origin pr/scale-to-zero
```

### Create PR
- **Title**: `feat(scale-to-zero): implement model-level retention-based scale-to-zero`
- **Base**: `upstream/main` (after PR1-5 merge)
- **Description**:
  - Complete scale-to-zero implementation
  - Retention period logic
  - Per-model configuration
  - Comprehensive tests
  - Breaking changes clearly documented

---

## Handling Split Commits

### For commits that need splitting (702432f, 30ab963, 2d77cd6):

**Method 1: Interactive Rebase**
```bash
git cherry-pick -n <commit-hash>
git reset HEAD

# Stage only files for current PR
git add <file1> <file2>
git commit -m "Partial commit message (PR X specific)"

# Save remaining changes for later
git stash
```

**Method 2: Patch Files**
```bash
# Create patch from original commit
git format-patch -1 <commit-hash> --stdout > temp.patch

# Edit patch file to remove unwanted hunks
# Apply edited patch
git apply temp.patch
git commit -m "Partial commit message"
```

---

## Testing Checklist for Each PR

### Before Pushing:
- [ ] `make test` passes
- [ ] `make lint` passes (if exists)
- [ ] `make manifests` and commit any generated files
- [ ] Manual smoke test of affected functionality
- [ ] Review git diff to ensure no unwanted changes

### For E2E-heavy PRs (PR5, PR6):
- [ ] `make test-e2e` passes
- [ ] Test on KIND cluster
- [ ] Test on OpenShift (if applicable)

### For API changes (PR1):
- [ ] CRD YAML generation: `make manifests`
- [ ] Deep copy code generation
- [ ] Verify no breaking changes to existing CRDs

---

## PR Submission Checklist

Each PR should have:
- [ ] Clear title following conventional commits
- [ ] Detailed description from PR_DECOMPOSITION_PLAN.md
- [ ] Link to design doc (if applicable)
- [ ] Breaking changes section (if applicable)
- [ ] Testing section describing what was tested
- [ ] Screenshots/examples (for user-facing changes)
- [ ] Migration guide (for breaking changes)
- [ ] Updated documentation

---

## Dependency Management

### If PR1 is merged before creating PR2:
```bash
git checkout pr/controller-optimizer-refactor
git rebase upstream/main
# Resolve conflicts if any
git push -f origin pr/controller-optimizer-refactor
```

### If creating all PRs upfront (before any merge):
- Keep PR2 based on PR1 branch
- Keep PR3 based on PR1 branch
- Keep PR4 based on PR3 branch
- Keep PR5 based on PR1 branch
- Keep PR6 based on upstream/main (create last, after all others merge)

---

## Timeline & Order

### Week 1-2: Foundation
1. Create & submit PR1 (API & CRD)
2. Create & submit PR2 (Controller/Optimizer) - in parallel
3. Create & submit PR3 (Metrics) - in parallel

### Week 2-3: Utilities & Testing
4. Create & submit PR4 (Collector/Utils) - after PR3 review starts
5. Create & submit PR5 (CI/E2E) - in parallel with PR4

### Week 3-4: Scale-to-Zero
6. Wait for PR1-5 to merge
7. Create & submit PR6 (Scale-to-Zero)
8. Extensive review & testing of PR6

### Week 4-6: Reviews & Iterations
- Address review comments
- Rebase as needed
- Merge in order

---

## Tips & Best Practices

### Communication:
- Tag reviewers in each PR
- Link related PRs in descriptions
- Update PR descriptions as design evolves
- Provide context for why changes were split

### Rebasing:
- Keep branches up to date with base
- Rebase frequently to avoid conflicts
- Test after each rebase

### Commit Hygiene:
- Keep commits focused and atomic
- Write clear commit messages
- Avoid mixing refactoring with feature work
- Squash fixup commits before final merge

### Code Review:
- Respond to comments promptly
- Mark conversations as resolved
- Update PR description with significant changes
- Request re-review after major updates

---

## Rollback Plan

If issues are discovered after merge:

### For PR1-5 (Low Risk):
- Create revert PR if critical
- Or create fix PR if minor

### For PR6 (High Risk):
- Feature flag: Can disable scale-to-zero via ConfigMap
- Revert if critical production issues
- Fix forward if non-critical

---

## Success Criteria

### Each PR:
- ✅ All CI checks pass
- ✅ At least 2 approvals from maintainers
- ✅ No unresolved review comments
- ✅ Documentation updated
- ✅ Tests cover new code paths

### Final (after PR6 merges):
- ✅ Full feature parity with origin/refactor/single-variant-clean
- ✅ All tests passing on upstream/main
- ✅ Documentation complete
- ✅ Release notes prepared

