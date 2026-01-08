# Release Process

This guide describes the release process for Workload-Variant-Autoscaler (WVA) maintainers.

## Release Checklist

### Pre-Release (1-2 weeks before)

- [ ] Review and merge outstanding PRs
- [ ] Ensure all CI checks pass on main branch
- [ ] Run full E2E test suite
- [ ] Update documentation for new features
- [ ] Review and update CHANGELOG.md
- [ ] Check for security vulnerabilities
- [ ] Test upgrade path from previous version

### Version Bump

WVA follows [Semantic Versioning](https://semver.org/):

- **MAJOR**: Breaking API changes
- **MINOR**: New features, backwards compatible
- **PATCH**: Bug fixes, backwards compatible

#### Update Version Numbers

1. **Chart version** in `charts/workload-variant-autoscaler/Chart.yaml`:
   ```yaml
   version: X.Y.Z
   appVersion: X.Y.Z
   ```

2. **Go module version** (if applicable):
   ```bash
   git tag vX.Y.Z
   ```

3. **Documentation references**:
   - Update version in README.md examples
   - Update any hardcoded version numbers

### Testing

#### Local Testing

```bash
# Run unit tests
make test

# Run E2E tests (saturation-based)
make test-e2e-saturation

# Run E2E tests (OpenShift)
make test-e2e-openshift
```

See [Testing Guide](testing.md) for comprehensive test instructions.

#### Helm Chart Testing

```bash
# Lint the chart
helm lint charts/workload-variant-autoscaler

# Test installation
kind create cluster --name wva-release-test
helm install wva-test charts/workload-variant-autoscaler \
  --namespace wva-system --create-namespace \
  --dry-run --debug

# Test upgrade from previous version
helm install wva-prev charts/workload-variant-autoscaler \
  --version <previous-version> \
  --namespace wva-system --create-namespace
  
kubectl apply -f charts/workload-variant-autoscaler/crds/
helm upgrade wva-prev charts/workload-variant-autoscaler \
  --namespace wva-system

# Cleanup
kind delete cluster --name wva-release-test
```

#### Container Image Testing

```bash
# Build and test the image
make docker-build IMG=ghcr.io/llm-d-incubation/workload-variant-autoscaler:vX.Y.Z

# Run container locally
docker run --rm ghcr.io/llm-d-incubation/workload-variant-autoscaler:vX.Y.Z --help

# Test image on cluster
make deploy IMG=ghcr.io/llm-d-incubation/workload-variant-autoscaler:vX.Y.Z
```

### Create Release Branch

For major/minor releases:

```bash
git checkout main
git pull origin main
git checkout -b release-X.Y
git push origin release-X.Y
```

Patch releases are cut from the existing release branch.

### Update CHANGELOG

Create or update `CHANGELOG.md` with:

```markdown
## [X.Y.Z] - YYYY-MM-DD

### Added
- New feature descriptions

### Changed
- Changes to existing functionality

### Deprecated
- Features marked for removal

### Removed
- Removed features

### Fixed
- Bug fixes

### Security
- Security fixes

### Breaking Changes
- **Important**: List any breaking changes
- Migration instructions
```

Commit the changelog:

```bash
git add CHANGELOG.md
git commit -m "Update CHANGELOG for vX.Y.Z"
git push origin release-X.Y
```

### Build and Push Artifacts

#### Container Images

The CI/CD pipeline automatically builds and pushes images when a tag is created:

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

Images are published to:
- `ghcr.io/llm-d-incubation/workload-variant-autoscaler:vX.Y.Z`
- `ghcr.io/llm-d-incubation/workload-variant-autoscaler:latest` (for latest stable release)

#### Helm Chart

The Helm chart is automatically packaged and published by the CI/CD pipeline.

Verify the chart is available:

```bash
helm repo add wva https://llm-d-incubation.github.io/workload-variant-autoscaler
helm repo update
helm search repo wva/workload-variant-autoscaler --version X.Y.Z
```

### Create GitHub Release

1. Go to [Releases](https://github.com/llm-d-incubation/workload-variant-autoscaler/releases)
2. Click "Draft a new release"
3. Select the tag `vX.Y.Z`
4. Title: "Release vX.Y.Z"
5. Description:
   - Summary of changes (from CHANGELOG)
   - Installation instructions
   - Upgrade notes
   - Known issues
   - Contributors

Example template:

```markdown
# Release vX.Y.Z

## What's New

[Brief summary of major changes]

## Installation

### Helm (Recommended)

\`\`\`bash
helm repo add wva https://llm-d-incubation.github.io/workload-variant-autoscaler
helm repo update
helm install workload-variant-autoscaler wva/workload-variant-autoscaler \
  --version X.Y.Z \
  --namespace workload-variant-autoscaler-system \
  --create-namespace
\`\`\`

### Container Image

\`\`\`
ghcr.io/llm-d-incubation/workload-variant-autoscaler:vX.Y.Z
\`\`\`

## Upgrading

### CRD Updates

**Important**: Helm does not automatically update CRDs. Apply them manually:

\`\`\`bash
kubectl apply -f https://github.com/llm-d-incubation/workload-variant-autoscaler/releases/download/vX.Y.Z/crds.yaml

helm upgrade workload-variant-autoscaler wva/workload-variant-autoscaler \
  --version X.Y.Z \
  --namespace workload-variant-autoscaler-system
\`\`\`

### Breaking Changes

[List any breaking changes and migration instructions]

## Full Changelog

See [CHANGELOG.md](https://github.com/llm-d-incubation/workload-variant-autoscaler/blob/vX.Y.Z/CHANGELOG.md)

## Contributors

Thanks to all contributors who made this release possible!
[List of contributors]

## Resources

- [Documentation](https://github.com/llm-d-incubation/workload-variant-autoscaler/tree/vX.Y.Z/docs)
- [Installation Guide](https://github.com/llm-d-incubation/workload-variant-autoscaler/blob/vX.Y.Z/docs/user-guide/installation.md)
- [Upgrade Guide](https://github.com/llm-d-incubation/workload-variant-autoscaler/blob/vX.Y.Z/README.md#upgrading)
```

6. Attach artifacts (if any):
   - CRD YAML bundle
   - Helm chart tarball (if not automated)

7. Publish the release

### Post-Release

#### Announce Release

- [ ] Post to llm-d Slack channel
- [ ] Update project README badges (if needed)
- [ ] Tweet/blog post (if applicable)
- [ ] Update llm-d documentation

#### Merge Back

Merge release branch back to main:

```bash
git checkout main
git merge release-X.Y --no-ff
git push origin main
```

#### Monitor

- [ ] Check GitHub Actions for any failures
- [ ] Monitor container registry for successful image push
- [ ] Verify Helm chart availability
- [ ] Watch for user reports/issues

#### Update Documentation Site

If using GitHub Pages or similar:

```bash
# Update docs site with new version
cd docs-site
npm run build
# Deploy to GitHub Pages or hosting platform
```

## Patch Releases

For patch releases (X.Y.Z where Z > 0):

1. Checkout the release branch:
   ```bash
   git checkout release-X.Y
   git pull origin release-X.Y
   ```

2. Cherry-pick bug fixes from main:
   ```bash
   git cherry-pick <commit-hash>
   ```

3. Update version numbers and CHANGELOG

4. Follow the same release process from "Create Release Branch" onwards

## Hotfix Process

For critical security or bug fixes:

1. Create hotfix branch from the release tag:
   ```bash
   git checkout -b hotfix-X.Y.Z+1 vX.Y.Z
   ```

2. Apply the fix

3. Test thoroughly

4. Fast-track the release process:
   - Update version to X.Y.Z+1
   - Create minimal CHANGELOG entry
   - Tag and release immediately
   - Notify users via GitHub release and Slack

5. Merge hotfix back to release branch and main

## Release Automation

### GitHub Actions Workflow

The release workflow (`.github/workflows/ci-release.yaml`) automatically:

1. Builds container images
2. Runs security scans (Trivy)
3. Pushes images to GHCR
4. Packages Helm chart
5. Creates GitHub release draft

### Triggering the Workflow

The workflow triggers on:
- Tag push matching `v*.*.*`
- Manual trigger via `workflow_dispatch`

### Required Secrets

Ensure these GitHub secrets are configured:
- `GITHUB_TOKEN` (automatic)
- Any additional registry credentials

## Version Support Policy

- **Latest stable release**: Full support
- **Previous minor release**: Security fixes only (6 months)
- **Older releases**: Community support via GitHub issues

## Rollback Procedure

If a release has critical issues:

1. **Immediate**: Yank the release on GitHub (mark as pre-release)

2. **Container Images**: Re-tag previous stable version as `latest`
   ```bash
   docker pull ghcr.io/llm-d-incubation/workload-variant-autoscaler:vX.Y.Z-1
   docker tag ghcr.io/llm-d-incubation/workload-variant-autoscaler:vX.Y.Z-1 \
              ghcr.io/llm-d-incubation/workload-variant-autoscaler:latest
   docker push ghcr.io/llm-d-incubation/workload-variant-autoscaler:latest
   ```

3. **Helm Chart**: Deprecate the chart version

4. **Communication**: Post prominent warning in release notes and Slack

5. **Fix**: Create hotfix release ASAP

## Checklist for First-Time Releasers

- [ ] Review this document thoroughly
- [ ] Ensure you have necessary repository permissions
- [ ] Verify GitHub Actions secrets are configured
- [ ] Test release process on a fork first
- [ ] Have a maintainer review your release plan
- [ ] Coordinate with team on release timing

## References

- [Semantic Versioning](https://semver.org/)
- [Keep a Changelog](https://keepachangelog.com/)
- [GitHub Releases Guide](https://docs.github.com/en/repositories/releasing-projects-on-github)
- [Helm Chart Versioning](https://helm.sh/docs/topics/charts/#the-chart-yaml-file)

## Questions?

Contact the maintainers team or ask in the #wva-dev Slack channel.
