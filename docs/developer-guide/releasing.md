# Release Process

This guide describes the release process for Workload-Variant-Autoscaler (WVA).

## Release Types

WVA follows [Semantic Versioning](https://semver.org/):

- **Major (x.0.0)**: Breaking API changes, significant architectural changes
- **Minor (0.x.0)**: New features, non-breaking enhancements
- **Patch (0.0.x)**: Bug fixes, security patches

## Release Cadence

- **Minor releases**: Every 4-6 weeks
- **Patch releases**: As needed for critical bugs or security issues
- **Major releases**: When necessary for breaking changes

## Prerequisites

### Required Tools

- Git with repository write access
- Go 1.24.0+
- Docker with registry push access
- Helm 3.x
- kubectl
- GitHub CLI (`gh`)
- GPG key for signing commits and tags

### Repository Setup

```bash
# Clone repository
git clone https://github.com/llm-d-incubation/workload-variant-autoscaler.git
cd workload-variant-autoscaler

# Configure GPG signing
git config user.signingkey <your-gpg-key-id>
git config commit.gpgsign true
git config tag.gpgsign true
```

## Release Process

### 1. Prepare Release Branch

```bash
# Ensure you're on the latest main
git checkout main
git pull origin main

# Create release branch
git checkout -b release-v0.x.0
```

### 2. Update Version Numbers

Update version references in the following files:

**charts/workload-variant-autoscaler/Chart.yaml:**
```yaml
version: 0.x.0
appVersion: "0.x.0"
```

**Makefile:**
```makefile
VERSION ?= 0.x.0
```

**README.md:** Update installation examples if necessary

**CHANGELOG.md:** Create or update changelog entry

```bash
# Commit version updates
git add charts/workload-variant-autoscaler/Chart.yaml Makefile README.md CHANGELOG.md
git commit -s -m "chore: bump version to v0.x.0"
```

### 3. Update CHANGELOG

Create a comprehensive changelog following [Keep a Changelog](https://keepachangelog.com/) format:

**CHANGELOG.md:**
```markdown
## [0.x.0] - YYYY-MM-DD

### Added
- New feature descriptions
- New configuration options

### Changed
- Behavior changes
- API changes

### Deprecated
- Features marked for removal

### Removed
- Deleted features

### Fixed
- Bug fixes

### Security
- Security fixes
```

```bash
git add CHANGELOG.md
git commit -s -m "docs: update CHANGELOG for v0.x.0"
```

### 4. Run Pre-Release Checks

```bash
# Run all tests
make test

# Run e2e tests (if applicable)
make test-e2e

# Lint code
make lint

# Verify CRDs generate correctly
make manifests
git diff --exit-code config/crd/bases/

# Build and verify image
make docker-build IMG=ghcr.io/llm-d-incubation/workload-variant-autoscaler:v0.x.0

# Helm lint
helm lint charts/workload-variant-autoscaler

# Verify documentation links
make check-docs  # If this target exists
```

### 5. Create Release Pull Request

```bash
# Push release branch
git push origin release-v0.x.0

# Create PR using GitHub CLI
gh pr create \
  --title "Release v0.x.0" \
  --body "Release v0.x.0

See CHANGELOG.md for full details.

**Release Checklist:**
- [ ] Version numbers updated
- [ ] CHANGELOG updated
- [ ] All tests passing
- [ ] Documentation updated
- [ ] CRDs validated
- [ ] Docker images build successfully
- [ ] Helm chart linted" \
  --label "release"
```

### 6. Review and Merge

1. Wait for CI checks to pass
2. Request reviews from maintainers
3. Address review feedback
4. Merge PR to main (squash merge preferred)

### 7. Create and Push Git Tag

```bash
# Checkout main after PR merge
git checkout main
git pull origin main

# Create signed tag
git tag -s v0.x.0 -m "Release v0.x.0"

# Push tag to trigger release workflow
git push origin v0.x.0
```

### 8. Build and Push Release Artifacts

The CI/CD pipeline (`.github/workflows/ci-release.yaml`) automatically:

1. **Builds Docker images**:
   - `ghcr.io/llm-d-incubation/workload-variant-autoscaler:v0.x.0`
   - `ghcr.io/llm-d-incubation/workload-variant-autoscaler:latest`

2. **Packages Helm chart**:
   - Creates `.tgz` chart archive
   - Updates Helm repository index

3. **Creates GitHub Release**:
   - Generates release notes
   - Attaches artifacts

Monitor the workflow:
```bash
gh run watch
```

### 9. Verify Release

```bash
# Verify Docker image is available
docker pull ghcr.io/llm-d-incubation/workload-variant-autoscaler:v0.x.0

# Verify Helm chart
helm repo add wva https://llm-d-incubation.github.io/workload-variant-autoscaler
helm repo update
helm search repo wva/workload-variant-autoscaler --version v0.x.0

# Test installation on clean cluster
kind create cluster --name wva-release-test
helm install wva-test wva/workload-variant-autoscaler \
  --version v0.x.0 \
  --namespace wva-system \
  --create-namespace

# Verify deployment
kubectl get pods -n wva-system
kubectl get crd variantautoscalings.llmd.ai
```

### 10. Update GitHub Release

Edit the auto-generated GitHub release:

1. Navigate to [Releases](https://github.com/llm-d-incubation/workload-variant-autoscaler/releases)
2. Click "Edit" on the new release
3. Update release notes with highlights from CHANGELOG
4. Attach additional artifacts if needed
5. Mark as "Latest release"

Example release notes structure:
```markdown
## What's New in v0.x.0

[Brief overview of major features/changes]

### 🎉 New Features
- Feature 1
- Feature 2

### 🐛 Bug Fixes
- Fix 1
- Fix 2

### 📖 Documentation
- Doc improvement 1

### ⚠️ Breaking Changes
[If any]

### 📦 Installation

**Helm:**
\`\`\`bash
helm repo add wva https://llm-d-incubation.github.io/workload-variant-autoscaler
helm install workload-variant-autoscaler wva/workload-variant-autoscaler \
  --version v0.x.0 \
  --namespace workload-variant-autoscaler-system \
  --create-namespace
\`\`\`

**Full Changelog**: https://github.com/llm-d-incubation/workload-variant-autoscaler/compare/v0.y.0...v0.x.0
```

### 11. Announce Release

1. **Update documentation site** (if applicable)
2. **Post to community channels**:
   - Slack workspace
   - Mailing lists
   - Social media
3. **Update related repositories**:
   - llm-d-infra
   - Example repositories

Example announcement:
```
🎉 Workload-Variant-Autoscaler v0.x.0 is now available!

Highlights:
- [Major feature 1]
- [Major feature 2]
- [Important fix]

📖 Full release notes: https://github.com/llm-d-incubation/workload-variant-autoscaler/releases/tag/v0.x.0

📦 Install: helm install wva wva/workload-variant-autoscaler --version v0.x.0
```

## Hotfix Releases

For critical bug fixes or security patches:

```bash
# Create hotfix branch from latest release tag
git checkout -b hotfix-v0.x.1 v0.x.0

# Apply fixes
git cherry-pick <commit-sha>  # Or make direct fixes

# Update version to patch release
# Edit Chart.yaml, Makefile, etc.

# Update CHANGELOG
# Add patch release section

# Commit
git commit -s -m "fix: critical bug description"

# Create PR targeting main
gh pr create --title "Hotfix v0.x.1" --label "hotfix"

# After merge, tag and release
git checkout main
git pull origin main
git tag -s v0.x.1 -m "Hotfix v0.x.1"
git push origin v0.x.1
```

## Release Artifacts

Each release produces:

1. **Docker Images**:
   - Controller image with version tag
   - Controller image with `latest` tag

2. **Helm Chart**:
   - Chart package (`.tgz`)
   - Updated Helm repository index

3. **GitHub Release**:
   - Release notes
   - Source code archives (`.tar.gz`, `.zip`)
   - Attached binaries (if applicable)

4. **Documentation**:
   - Updated docs site (if applicable)
   - Release announcement

## Post-Release Tasks

### Update Documentation

```bash
# Update installation docs with new version
# Update compatibility matrix
# Update quickstart guides
```

### Monitor Release

1. **Watch for issues**:
   - Monitor GitHub issues for new reports
   - Check community channels for problems

2. **Track adoption**:
   - Monitor Docker image pulls
   - Track Helm chart downloads

3. **Prepare next release**:
   - Create milestone for next release
   - Prioritize issues and features

### Backporting

For critical fixes that need to go to older versions:

```bash
# Create backport branch from older release
git checkout -b backport-v0.y.1 v0.y.0

# Cherry-pick fix
git cherry-pick <commit-sha>

# Follow hotfix release process
```

## Release Checklist Template

Copy this checklist for each release:

```markdown
## Release v0.x.0 Checklist

### Pre-Release
- [ ] All tests passing on main
- [ ] No blocking issues in milestone
- [ ] Documentation reviewed and updated
- [ ] Breaking changes documented
- [ ] Migration guide created (if needed)

### Release Preparation
- [ ] Release branch created
- [ ] Version numbers updated (Chart.yaml, Makefile, etc.)
- [ ] CHANGELOG.md updated
- [ ] Release PR created and reviewed
- [ ] PR merged to main

### Release Execution
- [ ] Git tag created and pushed
- [ ] CI/CD pipeline completed successfully
- [ ] Docker images published
- [ ] Helm chart published
- [ ] GitHub release created

### Post-Release
- [ ] Release verified on clean cluster
- [ ] GitHub release notes updated
- [ ] Announcement posted to community
- [ ] Documentation site updated
- [ ] Next milestone created

### Issues During Release
- [ ] No issues / Document any issues here
```

## Rollback Procedure

If a release has critical issues:

1. **Immediate actions**:
   ```bash
   # Mark GitHub release as pre-release
   gh release edit v0.x.0 --prerelease
   
   # Update Helm chart to point to previous stable version
   # (via Helm repository management)
   ```

2. **Issue hotfix**:
   - Follow hotfix release process
   - Increment patch version

3. **Communication**:
   - Post issue notice to community
   - Update release notes with known issues

## Security Releases

For security vulnerabilities:

1. **Private disclosure**: Handle vulnerability reports privately
2. **Develop fix**: Create fix in private branch
3. **Coordinate release**: Time release with disclosure
4. **Expedited process**: Fast-track release process
5. **Security advisory**: Publish GitHub Security Advisory
6. **Notification**: Alert users via security mailing list

## Version Support Policy

- **Latest minor version**: Full support
- **Previous minor version**: Security fixes only (6 months)
- **Older versions**: Community support only

## Troubleshooting Releases

### CI/CD Pipeline Failure

```bash
# Check workflow status
gh run list --workflow=ci-release.yaml

# View logs
gh run view <run-id> --log

# Re-run if transient failure
gh run rerun <run-id>
```

### Docker Image Push Failure

```bash
# Verify credentials
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin

# Manual push (if needed)
docker tag workload-variant-autoscaler:v0.x.0 ghcr.io/llm-d-incubation/workload-variant-autoscaler:v0.x.0
docker push ghcr.io/llm-d-incubation/workload-variant-autoscaler:v0.x.0
```

### Helm Chart Publish Failure

```bash
# Package chart manually
helm package charts/workload-variant-autoscaler

# Update index
helm repo index . --url https://llm-d-incubation.github.io/workload-variant-autoscaler

# Push to gh-pages branch
git checkout gh-pages
git add .
git commit -m "Release v0.x.0"
git push origin gh-pages
```

## Related Documentation

- [Contributing Guide](../../CONTRIBUTING.md)
- [Development Guide](development.md)
- [Testing Guide](testing.md)
- [CI/CD Workflows](../../.github/workflows/)

---

**Questions?** Contact the maintainers team or open a discussion on GitHub.
