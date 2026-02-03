# Release Guide

This guide covers the release process for Workload-Variant-Autoscaler.

## Release Process Overview

WVA follows semantic versioning (SemVer 2.0.0) and uses automated CI/CD pipelines for releases.

### Version Numbering

- **Major version (X.0.0)**: Breaking changes, major architectural changes
- **Minor version (0.X.0)**: New features, backwards-compatible enhancements
- **Patch version (0.0.X)**: Bug fixes, documentation updates

## Pre-Release Checklist

Before creating a release, ensure:

- [ ] All tests pass on main branch (`make test` and `make test-e2e`)
- [ ] Documentation is up-to-date
- [ ] CHANGELOG is updated with all changes since last release
- [ ] CRD changes are documented (if applicable)
- [ ] Breaking changes are clearly noted
- [ ] Version numbers updated in relevant files

## Release Steps

### 1. Prepare the Release

**Update Changelog:**

Create or update `docs/CHANGELOG-vX.Y.Z.md`:

```markdown
# Changelog v0.6.0

## Features

- Added support for custom saturation thresholds (#123)
- Improved multi-controller isolation (#145)

## Bug Fixes

- Fixed metric emission during Prometheus outages (#156)
- Resolved race condition in deployment detection (#167)

## Breaking Changes

- Environment variable `PROM_URL` renamed to `PROMETHEUS_BASE_URL`
  - Migration: Update deployment manifests to use new variable name

## Deprecations

- `inferenceEngine` field deprecated in favor of auto-detection
  - Will be removed in v0.7.0
```

**Update Version References:**

```bash
# Update image tags in Helm chart
vim charts/workload-variant-autoscaler/Chart.yaml
# Update appVersion: "0.6.0"
# Update version: "0.6.0"

# Update image tags in documentation
find docs/ -name "*.md" -exec sed -i 's/:v0.5.0/:v0.6.0/g' {} \;
```

**Commit Changes:**

```bash
git checkout -b release/v0.6.0
git add docs/CHANGELOG-v0.6.0.md charts/
git commit -m "chore: prepare release v0.6.0"
git push origin release/v0.6.0
```

### 2. Create Pull Request

Create a PR from `release/v0.6.0` to `main`:

- Title: "Release v0.6.0"
- Description: Link to changelog and highlight key changes
- Request reviews from maintainers

### 3. Tag the Release

After PR is merged:

```bash
# Fetch latest main
git checkout main
git pull origin main

# Create annotated tag
git tag -a v0.6.0 -m "Release v0.6.0

Features:
- Added support for custom saturation thresholds
- Improved multi-controller isolation

Bug Fixes:
- Fixed metric emission during Prometheus outages
- Resolved race condition in deployment detection

See docs/CHANGELOG-v0.6.0.md for full details."

# Push tag
git push origin v0.6.0
```

### 4. Automated Release Pipeline

Pushing a tag triggers the automated release workflow (`.github/workflows/ci-release.yaml`):

1. **Build & Test**: Runs full test suite
2. **Build Images**: Creates multi-arch container images
3. **Push Images**: Pushes to container registry (ghcr.io)
4. **Create GitHub Release**: Generates release notes from changelog
5. **Publish Helm Chart**: Updates Helm chart repository

Monitor the workflow:

```bash
# Check workflow status
gh workflow view ci-release.yaml
gh run list --workflow=ci-release.yaml
```

### 5. Verify the Release

**Check Container Images:**

```bash
# Pull and verify image
docker pull ghcr.io/llm-d/workload-variant-autoscaler:v0.6.0
docker inspect ghcr.io/llm-d/workload-variant-autoscaler:v0.6.0

# Verify multi-arch support
docker manifest inspect ghcr.io/llm-d/workload-variant-autoscaler:v0.6.0
```

**Check Helm Chart:**

```bash
# Update Helm repo
helm repo update

# Verify chart version
helm search repo workload-variant-autoscaler --version 0.6.0
```

**Test Installation:**

```bash
# Test fresh installation
helm install wva workload-variant-autoscaler/workload-variant-autoscaler \
  --version 0.6.0 \
  --namespace wva-test \
  --create-namespace \
  --dry-run
```

### 6. Announce the Release

**Update GitHub Release:**

1. Go to [Releases](https://github.com/llm-d-incubation/workload-variant-autoscaler/releases)
2. Find the auto-created release
3. Edit to add:
   - Highlights and key features
   - Upgrade instructions
   - Breaking change warnings
   - Links to documentation

**Example Release Notes:**

```markdown
## Workload-Variant-Autoscaler v0.6.0

### Highlights

🎉 Custom saturation thresholds - Configure KV cache and queue depth thresholds per model
🔧 Improved multi-controller isolation - Better support for multi-tenant environments
🐛 Enhanced stability - Fixed several race conditions and edge cases

### Upgrade Instructions

**Breaking Changes:**
- Environment variable `PROM_URL` renamed to `PROMETHEUS_BASE_URL`
  
Update your Helm values or deployment manifests:
```yaml
env:
  - name: PROMETHEUS_BASE_URL  # Changed from PROM_URL
    value: "https://prometheus.example.com"
```

**CRD Updates:**
```bash
# Apply updated CRDs before upgrading
kubectl apply -f https://github.com/llm-d-incubation/workload-variant-autoscaler/releases/download/v0.6.0/crds.yaml

# Upgrade Helm release
helm upgrade workload-variant-autoscaler workload-variant-autoscaler/workload-variant-autoscaler \
  --version 0.6.0 \
  --namespace workload-variant-autoscaler-system
```

### Full Changelog

See [CHANGELOG-v0.6.0.md](docs/CHANGELOG-v0.6.0.md) for complete details.
```

**Notify Community:**

- Post announcement in GitHub Discussions
- Share in community Slack channels
- Update documentation website (if applicable)

## Hotfix Releases

For critical bug fixes:

```bash
# Create hotfix branch from release tag
git checkout -b hotfix/v0.6.1 v0.6.0

# Make fix
git commit -m "fix: critical bug in metric emission"

# Update changelog
echo "## v0.6.1 - Hotfix\n\n- Fixed critical bug in metric emission" >> docs/CHANGELOG-v0.6.1.md

# Create PR to main
git push origin hotfix/v0.6.1

# After merge, tag
git tag -a v0.6.1 -m "Hotfix v0.6.1: Critical metric emission fix"
git push origin v0.6.1
```

## Release Artifacts

Each release produces:

1. **Container Images** (multi-arch):
   - `ghcr.io/llm-d/workload-variant-autoscaler:v0.6.0`
   - `ghcr.io/llm-d/workload-variant-autoscaler:latest` (updated)

2. **Helm Chart**:
   - Chart version `0.6.0`
   - Published to Helm repository

3. **GitHub Release**:
   - Source code (zip and tar.gz)
   - CRD manifests
   - Release notes

## Versioning Guidelines

### When to Bump Major Version

- Breaking API changes in VariantAutoscaling CRD
- Removal of deprecated features
- Major architectural changes requiring migration
- Incompatible configuration changes

### When to Bump Minor Version

- New features (new CRD fields, new metrics, new modes)
- Backwards-compatible enhancements
- New integration support
- Deprecation notices (features marked for removal)

### When to Bump Patch Version

- Bug fixes
- Documentation updates
- Performance improvements (no API changes)
- Security patches

## Release Calendar

WVA follows a time-based release schedule:

- **Minor releases**: Every 6-8 weeks
- **Patch releases**: As needed for critical fixes
- **Major releases**: When significant breaking changes accumulate

## Rollback Procedure

If a release has critical issues:

```bash
# Tag a rollback release
git tag -a v0.6.2 <previous-good-commit> -m "Rollback to v0.6.1"
git push origin v0.6.2

# Update documentation to recommend rollback
# Notify community via GitHub issue and Slack
```

## Post-Release Tasks

After each release:

- [ ] Monitor GitHub issues for release-related bugs
- [ ] Update project roadmap
- [ ] Plan next release milestones
- [ ] Review and close completed GitHub projects/milestones
- [ ] Update metrics and adoption tracking

## CI/CD Configuration

Release automation is configured in:

- `.github/workflows/ci-release.yaml` - Main release workflow
- `.github/workflows/helm-release.yaml` - Helm chart publishing
- `Makefile` - Build and version targets
- `charts/workload-variant-autoscaler/Chart.yaml` - Chart version

### Environment Variables

Release workflows use these secrets (configured in GitHub repository settings):

- `GITHUB_TOKEN` - Automatic token for GitHub API
- `GHCR_TOKEN` - GitHub Container Registry authentication
- `HELM_REPO_TOKEN` - Helm repository access (if using external repo)

## Troubleshooting Releases

### Build Failures

```bash
# Test build locally
make docker-build IMG=test/wva:v0.6.0
make test
make test-e2e
```

### Image Push Failures

```bash
# Verify registry credentials
echo $GHCR_TOKEN | docker login ghcr.io -u <username> --password-stdin

# Test push manually
docker push ghcr.io/llm-d/workload-variant-autoscaler:v0.6.0
```

### Helm Chart Issues

```bash
# Lint chart
helm lint charts/workload-variant-autoscaler

# Package and test
helm package charts/workload-variant-autoscaler
helm install test-release ./workload-variant-autoscaler-0.6.0.tgz --dry-run
```

## Security Considerations

- All release artifacts are signed with GPG (future enhancement)
- Container images are scanned for vulnerabilities before release
- Dependencies are checked for known CVEs
- Security advisories are published via GitHub Security Advisories

## Additional Resources

- [Semantic Versioning](https://semver.org/)
- [GitHub Releases Best Practices](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases)
- [Helm Chart Versioning](https://helm.sh/docs/topics/charts/#charts-and-versioning)
- [Container Image Tagging](https://docs.docker.com/engine/reference/commandline/tag/)

## Questions?

Contact the maintainers:
- Open a [GitHub Discussion](https://github.com/llm-d-incubation/workload-variant-autoscaler/discussions)
- Attend community meetings
- Check [CONTRIBUTING.md](../../CONTRIBUTING.md)

---

**Next Release:** See [Roadmap](https://github.com/llm-d-incubation/workload-variant-autoscaler/milestones) for planned features
