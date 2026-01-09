# Release Process

This guide documents the release process for Workload-Variant-Autoscaler (WVA).

## Release Cadence

WVA follows a time-based release schedule:

- **Minor releases**: Every 2-3 months (0.x.0)
- **Patch releases**: As needed for bug fixes (0.x.y)
- **Pre-releases**: Alpha/beta versions for testing new features

## Release Roles

- **Release Manager**: Coordinates the release process
- **Maintainers**: Approve PRs and review release checklist
- **Contributors**: Submit PRs and test releases

## Release Workflow

### 1. Pre-Release Planning (T-2 weeks)

**Actions:**
- [ ] Create release milestone in GitHub
- [ ] Identify features/fixes for the release
- [ ] Review and merge pending PRs
- [ ] Update dependencies and security patches
- [ ] Ensure CI/CD is green

**Communication:**
- Announce release timeline in community meeting
- Post release plan in GitHub Discussions

### 2. Code Freeze (T-1 week)

**Actions:**
- [ ] Freeze main branch for new features
- [ ] Create release branch: `release-v0.x`
- [ ] Only bug fixes and documentation allowed
- [ ] Run full test suite including E2E tests

```bash
# Create release branch
git checkout main
git pull origin main
git checkout -b release-v0.5.0
git push origin release-v0.5.0
```

### 3. Release Candidate Testing (T-5 days)

**Actions:**
- [ ] Tag release candidate: `v0.x.0-rc.1`
- [ ] Build and publish RC images
- [ ] Deploy RC to test environments
- [ ] Run smoke tests and E2E tests
- [ ] Document known issues

```bash
# Tag release candidate
git tag -a v0.5.0-rc.1 -m "Release candidate 0.5.0-rc.1"
git push origin v0.5.0-rc.1

# Trigger CI to build images
```

**Testing checklist:**
- [ ] Unit tests pass
- [ ] E2E tests pass (Kubernetes)
- [ ] E2E tests pass (OpenShift)
- [ ] E2E tests pass (Kind emulator)
- [ ] Performance benchmarks acceptable
- [ ] Documentation builds correctly

### 4. Release Documentation (T-3 days)

**Actions:**
- [ ] Update CHANGELOG.md with all changes
- [ ] Review and update documentation
- [ ] Generate API documentation
- [ ] Update Helm chart version and documentation
- [ ] Prepare release notes

**Documentation checklist:**
- [ ] README.md updated
- [ ] Installation guides current
- [ ] Breaking changes documented
- [ ] Migration guide (if needed)
- [ ] Helm chart README updated
- [ ] API reference regenerated

### 5. Final Release (T-0)

**Actions:**
- [ ] Merge release branch to main
- [ ] Tag final release: `v0.x.0`
- [ ] Build and publish release images
- [ ] Publish Helm chart
- [ ] Create GitHub release
- [ ] Announce release

```bash
# Merge release branch
git checkout main
git merge release-v0.5.0
git push origin main

# Tag final release
git tag -a v0.5.0 -m "Release v0.5.0"
git push origin v0.5.0

# GitHub release will trigger CI/CD to build and publish
```

### 6. Post-Release (T+1 day)

**Actions:**
- [ ] Monitor for issues
- [ ] Update project boards
- [ ] Close release milestone
- [ ] Plan next release
- [ ] Update community on release status

## Versioning

WVA follows [Semantic Versioning](https://semver.org/):

- **MAJOR** (1.0.0): Incompatible API changes
- **MINOR** (0.x.0): New features, backward compatible
- **PATCH** (0.x.y): Bug fixes, backward compatible

**Current version**: v0.x.x (pre-1.0)
- Breaking changes allowed in minor versions
- Will follow strict semver after v1.0.0

## Build Artifacts

### Container Images

Images are built and published to:
- **Quay.io**: `quay.io/llm-d/workload-variant-autoscaler:v0.x.0`

Tags:
- `v0.x.0` - Specific version
- `v0.x` - Latest patch for minor version
- `latest` - Latest stable release
- `main` - Latest from main branch (development)

### Helm Charts

Helm charts are published to:
- **GitHub Releases**: Packaged chart tarballs
- **Helm Repository**: (planned)

Chart versions match WVA versions: `chart-v0.x.0`

## Release Checklist

Use this checklist for each release:

```markdown
## Release v0.x.0 Checklist

### Pre-Release
- [ ] Create release milestone
- [ ] Update dependencies
- [ ] Security audit
- [ ] CI/CD green
- [ ] Community notification

### Code Freeze
- [ ] Create release branch
- [ ] Feature freeze announced
- [ ] Full test run

### RC Testing
- [ ] Tag RC
- [ ] Deploy to test environments
- [ ] E2E tests (K8s, OpenShift, Kind)
- [ ] Performance benchmarks
- [ ] Known issues documented

### Documentation
- [ ] CHANGELOG.md updated
- [ ] Breaking changes documented
- [ ] Migration guide (if needed)
- [ ] API docs regenerated
- [ ] Helm chart docs updated

### Release
- [ ] Merge release branch
- [ ] Tag release
- [ ] Build images
- [ ] Publish Helm chart
- [ ] Create GitHub release
- [ ] Release notes published

### Post-Release
- [ ] Monitor for issues
- [ ] Update project boards
- [ ] Close milestone
- [ ] Plan next release
```

## Release Notes Template

```markdown
# Workload-Variant-Autoscaler v0.x.0

Release date: YYYY-MM-DD

## 🚀 What's New

- Feature 1: Brief description
- Feature 2: Brief description

## 🐛 Bug Fixes

- Fix 1: Brief description
- Fix 2: Brief description

## 📚 Documentation

- Documentation improvement 1
- Documentation improvement 2

## ⚠️ Breaking Changes

- Breaking change 1 with migration instructions
- Breaking change 2 with migration instructions

## 🔧 Installation

### Helm
\`\`\`bash
helm upgrade -i workload-variant-autoscaler oci://quay.io/llm-d/charts/workload-variant-autoscaler \
  --version v0.x.0 \
  --namespace workload-variant-autoscaler-system \
  --create-namespace
\`\`\`

### Manual CRD Upgrade
\`\`\`bash
kubectl apply -f https://github.com/llm-d-incubation/workload-variant-autoscaler/releases/download/v0.x.0/crds.yaml
\`\`\`

## 📦 Assets

- Container Image: `quay.io/llm-d/workload-variant-autoscaler:v0.x.0`
- Helm Chart: `workload-variant-autoscaler-v0.x.0.tgz`
- Source Code: `Source code (zip)`, `Source code (tar.gz)`

## 📖 Upgrade Guide

See the [Installation Guide](https://github.com/llm-d-incubation/workload-variant-autoscaler/blob/main/docs/user-guide/installation.md#upgrading) for upgrade instructions.

## 🙏 Contributors

Thanks to all contributors for this release! (GitHub will auto-generate this list)

## 🔗 Links

- [Documentation](https://github.com/llm-d-incubation/workload-variant-autoscaler/tree/main/docs)
- [Quick Start](https://github.com/llm-d-incubation/workload-variant-autoscaler/blob/main/docs/quickstart.md)
- [Installation Guide](https://github.com/llm-d-incubation/workload-variant-autoscaler/blob/main/docs/user-guide/installation.md)

**Full Changelog**: https://github.com/llm-d-incubation/workload-variant-autoscaler/compare/v0.x-1.0...v0.x.0
```

## Hotfix Releases

For critical bugs or security issues:

1. **Create hotfix branch** from release tag
   ```bash
   git checkout -b hotfix-v0.5.1 v0.5.0
   ```

2. **Apply fix and test**
   ```bash
   # Make changes
   git commit -m "fix: critical bug"
   
   # Run tests
   make test test-e2e
   ```

3. **Merge to main and release branch**
   ```bash
   git checkout main
   git merge hotfix-v0.5.1
   git push origin main
   
   git checkout release-v0.5
   git merge hotfix-v0.5.1
   git push origin release-v0.5
   ```

4. **Tag and release**
   ```bash
   git tag -a v0.5.1 -m "Hotfix release v0.5.1"
   git push origin v0.5.1
   ```

## Release Automation

### GitHub Actions

WVA uses GitHub Actions for CI/CD:

- **`.github/workflows/ci-release.yaml`** - Build and publish releases
- **`.github/workflows/helm-release.yaml`** - Package and publish Helm charts

### Triggering Releases

Releases are triggered by pushing tags:

```bash
git tag -a v0.x.0 -m "Release v0.x.0"
git push origin v0.x.0
```

The CI workflow will:
1. Build Docker images
2. Run security scans (Trivy)
3. Publish to Quay.io
4. Package Helm chart
5. Create GitHub release

## CRD Versioning

When updating CRDs:

1. **Update API version** in `api/vXalphaY/`
2. **Provide conversion webhooks** for API migrations
3. **Document migration** in release notes
4. **Test upgrade path** from previous version

**Important**: Helm does NOT automatically upgrade CRDs. Users must manually apply CRD updates.

Document this clearly in release notes:
```bash
# Upgrade CRDs first
kubectl apply -f https://github.com/llm-d-incubation/workload-variant-autoscaler/releases/download/v0.x.0/crds.yaml

# Then upgrade Helm release
helm upgrade workload-variant-autoscaler ...
```

## Testing Strategy

### Pre-Release Testing

- [ ] Unit tests: `make test`
- [ ] E2E tests: `make test-e2e`
- [ ] OpenShift E2E: `make test-e2e-openshift`
- [ ] Kind emulator: `make deploy-llm-d-wva-emulated-on-kind`
- [ ] Performance benchmarks
- [ ] Upgrade testing (previous version → new version)

### Test Environments

Maintain test environments for:
- Kubernetes 1.31, 1.32
- OpenShift 4.18, 4.19
- Kind (latest)

## Communication

### Channels

- **GitHub Releases**: Official release announcements
- **GitHub Discussions**: Release planning and feedback
- **Community Meetings**: Release updates
- **Slack/Discord**: Real-time communication

### Timeline Communication

- **T-2 weeks**: Announce upcoming release
- **T-1 week**: Feature freeze notification
- **T-3 days**: RC testing call for volunteers
- **T-0**: Release announcement
- **T+1 day**: Post-release update

## Rollback Procedure

If critical issues are discovered post-release:

1. **Immediate**: Update release notes with known issues
2. **Short-term**: Provide workaround instructions
3. **Medium-term**: Prepare hotfix release
4. **If severe**: Recommend users revert to previous version

```bash
# Revert to previous version
helm upgrade workload-variant-autoscaler ... --version v0.4.0
```

## Additional Resources

- [Semantic Versioning](https://semver.org/)
- [Keep a Changelog](https://keepachangelog.com/)
- [GitHub Release Documentation](https://docs.github.com/en/repositories/releasing-projects-on-github)
- [Helm Chart Versioning](https://helm.sh/docs/topics/charts/#charts-and-versioning)

## Questions?

For questions about the release process:
- Open a [GitHub Discussion](https://github.com/llm-d-incubation/workload-variant-autoscaler/discussions)
- Ask in community meetings
- Contact maintainers

---

**Note**: This release process is continuously refined. Please [contribute improvements](../../CONTRIBUTING.md)!
