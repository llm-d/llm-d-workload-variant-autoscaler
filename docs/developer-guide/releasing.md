# Release Process

This guide describes the release process for Workload-Variant-Autoscaler (WVA).

## Overview

WVA follows semantic versioning (SemVer) with releases tagged as `vX.Y.Z`. The release process includes building container images, publishing Helm charts, and updating documentation.

## Release Types

- **Major (vX.0.0):** Breaking changes, major new features
- **Minor (vX.Y.0):** New features, non-breaking API changes
- **Patch (vX.Y.Z):** Bug fixes, minor improvements

## Prerequisites

Before creating a release:

- [ ] All tests passing on main branch
- [ ] Documentation updated
- [ ] CHANGELOG updated with release notes
- [ ] CRD changes documented (if applicable)
- [ ] Breaking changes clearly documented

## Release Checklist

### 1. Prepare Release Branch (for major/minor releases)

```bash
# Create release branch
git checkout -b release/v0.6.0
git push origin release/v0.6.0
```

For patch releases, work directly on the release branch or main.

### 2. Update Version Numbers

Update version references in:

- `charts/workload-variant-autoscaler/Chart.yaml` - `version` and `appVersion`
- `Makefile` - `VERSION` variable (if present)
- Any hardcoded version strings in documentation

```bash
# Example: Update Chart.yaml
version: 0.6.0
appVersion: "v0.6.0"
```

### 3. Update CHANGELOG

Create or update the changelog file:

```bash
# Create changelog for this version
cat > docs/CHANGELOG-v0.6.0.md << 'EOF'
# Changelog v0.6.0

## Release Highlights

- New feature X
- Improved Y
- Fixed Z

## Breaking Changes

- API change: field X renamed to Y
- Migration guide: ...

## New Features

- Feature 1 (#PR-number)
- Feature 2 (#PR-number)

## Bug Fixes

- Fixed issue A (#PR-number)
- Fixed issue B (#PR-number)

## Dependencies

- Updated dependency X to vY.Z

## Upgrade Notes

...
EOF
```

### 4. Create Release Commit

```bash
git add .
git commit -m "chore: prepare release v0.6.0"
git push origin release/v0.6.0
```

### 5. Create Pull Request

Create a PR from the release branch to main:

```markdown
**Title:** Release v0.6.0

**Description:**
This PR prepares the release of WVA v0.6.0.

## Changes
- Updated version to v0.6.0
- Updated CHANGELOG
- [List any other changes]

## Release Checklist
- [x] All tests passing
- [x] Documentation updated
- [x] CHANGELOG complete
- [x] Breaking changes documented

/cc @maintainers
```

### 6. Merge and Tag

After PR approval:

```bash
# Merge PR to main
# Then create and push tag
git checkout main
git pull origin main

# Create annotated tag
git tag -a v0.6.0 -m "Release v0.6.0"
git push origin v0.6.0
```

### 7. Automated Release Process

The CI/CD pipeline automatically:

1. **Builds container image** and pushes to `quay.io/llm-d/workload-variant-autoscaler:v0.6.0`
2. **Packages Helm chart** and publishes to chart repository
3. **Creates GitHub release** with release notes

Monitor the release workflow:

```bash
# Check GitHub Actions
# Navigate to: https://github.com/llm-d-incubation/workload-variant-autoscaler/actions
```

### 8. Verify Release

After automation completes:

```bash
# Verify image
docker pull quay.io/llm-d/workload-variant-autoscaler:v0.6.0

# Verify Helm chart
helm repo update
helm search repo workload-variant-autoscaler --version 0.6.0

# Test installation
helm install wva-test workload-variant-autoscaler/workload-variant-autoscaler \
  --version 0.6.0 \
  --namespace wva-test \
  --create-namespace \
  --dry-run
```

### 9. Update GitHub Release

Edit the auto-created GitHub release:

1. Navigate to [Releases](https://github.com/llm-d-incubation/workload-variant-autoscaler/releases)
2. Click "Edit" on the new release
3. Add release notes from CHANGELOG
4. Attach any release artifacts (if applicable)
5. Mark as "Latest release"

### 10. Announce Release

- Update README badges if needed
- Post announcement in community channels
- Update documentation site (if applicable)

## Hotfix Releases

For critical bug fixes:

```bash
# Create hotfix branch from release tag
git checkout -b hotfix/v0.5.1 v0.5.0

# Make fix
git commit -m "fix: critical issue description"

# Tag and push
git tag -a v0.5.1 -m "Release v0.5.1 - Hotfix"
git push origin v0.5.1

# Merge back to main
git checkout main
git merge hotfix/v0.5.1
git push origin main
```

## CRD Changes

**Critical:** When CRDs change:

1. Document breaking changes prominently
2. Provide migration guide in CHANGELOG
3. Update CRD documentation
4. Test upgrade path from previous version

Example migration note:

```markdown
## Breaking Changes

### CRD API Changes

The `VariantAutoscaling` CRD has a new required field `scaleTargetRef`.

**Migration:**
```yaml
# Old (v0.4.x)
spec:
  modelID: "meta/llama-3.1-8b"

# New (v0.5.x)
spec:
  scaleTargetRef:
    kind: Deployment
    name: llama-8b
  modelID: "meta/llama-3.1-8b"
```

Users must manually update their VariantAutoscaling resources after upgrading.
```

## Post-Release Tasks

- [ ] Monitor for issues in first 24-48 hours
- [ ] Respond to user questions/issues promptly
- [ ] Update roadmap/project boards
- [ ] Archive release branch (for major/minor releases)

## Rollback Procedure

If critical issues are discovered:

```bash
# Quick rollback with Helm
helm rollback workload-variant-autoscaler -n workload-variant-autoscaler-system

# Or redeploy previous version
helm upgrade workload-variant-autoscaler \
  workload-variant-autoscaler/workload-variant-autoscaler \
  --version 0.5.0 \
  --namespace workload-variant-autoscaler-system
```

For image-level rollback:

```bash
# Revert to previous image
kubectl set image deployment/workload-variant-autoscaler-controller-manager \
  manager=quay.io/llm-d/workload-variant-autoscaler:v0.5.0 \
  -n workload-variant-autoscaler-system
```

## Release Cadence

- **Major releases:** As needed (breaking changes, major features)
- **Minor releases:** Monthly or as features are ready
- **Patch releases:** As needed for bug fixes

## Version Support

- **Latest major version:** Full support
- **Previous major version:** Security fixes for 6 months
- **Older versions:** Best effort, community support

## Troubleshooting Release Issues

### Container Build Fails

```bash
# Check CI logs
# Verify Dockerfile syntax
# Test local build:
make docker-build IMG=test:latest
```

### Helm Chart Validation Fails

```bash
# Validate chart locally
helm lint charts/workload-variant-autoscaler

# Test installation
helm install wva-test ./charts/workload-variant-autoscaler --dry-run --debug
```

### Tag Push Rejected

```bash
# If tag already exists
git tag -d v0.6.0
git push origin :refs/tags/v0.6.0

# Then recreate and push
git tag -a v0.6.0 -m "Release v0.6.0"
git push origin v0.6.0
```

## References

- [Semantic Versioning](https://semver.org/)
- [GitHub Releases](https://github.com/llm-d-incubation/workload-variant-autoscaler/releases)
- [Helm Chart Repository](https://github.com/llm-d-incubation/helm-charts)

---

**Questions?** Contact the maintainers or open a discussion.
