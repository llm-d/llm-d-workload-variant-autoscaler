# GitHub Agentic Workflows

The Workload-Variant-Autoscaler repository uses **GitHub Agentic Workflows (gh-aw)** to automate documentation updates, workflow creation, and debugging tasks. These AI-powered workflows help maintain consistency and reduce manual maintenance overhead.

## What are Agentic Workflows?

Agentic workflows are AI-powered GitHub Actions that use natural language instructions to automate repository tasks. They combine GitHub Actions' automation capabilities with AI agents that can read, write, and interact with your repository.

## Available Workflows

### Update Docs

**Trigger**: Automatic on every push to `main` branch  
**Purpose**: Keeps documentation synchronized with code changes

This workflow:
- Analyzes code changes in each commit
- Identifies documentation gaps or outdated content
- Creates draft pull requests with documentation updates
- Follows Diátaxis framework (tutorials, how-to guides, reference, explanation)
- Maintains consistent style (precise, concise, developer-friendly)

**Workflow file**: `.github/workflows/update-docs.md`

### Assign Docs PR to Original Author

**Trigger**: Automatic when a PR is opened by `github-actions[bot]` with title starting with `docs:`  
**Purpose**: Assigns documentation PRs to the author of the original PR that triggered the docs update

This workflow:
- Detects documentation PRs created by the Update Docs workflow
- Extracts the original PR number from the docs PR body or branch name
- Assigns the docs PR to the original PR author (not the merger)
- Adds a comment with attribution for transparency
- Falls back to the most recently merged PR if metadata lookup fails

**Workflow file**: `.github/workflows/assign-docs-pr.yml`

This ensures accountability and visibility for documentation changes, making it clear who should review and merge the docs update.

### Create Agentic Workflow

**Trigger**: Manual via custom agent  
**Purpose**: Interactive workflow designer to create new agentic workflows

This agent helps you:
- Define workflow triggers (issues, PRs, schedules, slash commands)
- Select appropriate tools and MCP servers
- Configure security and network access
- Generate workflow markdown files

**Agent file**: `.github/agents/create-agentic-workflow.agent.md`

### Debug Agentic Workflow

**Trigger**: Manual via custom agent  
**Purpose**: Debug and refine existing workflows

Use this agent to:
- Analyze workflow logs
- Audit workflow runs
- Improve workflow performance
- Troubleshoot failures

**Agent file**: `.github/agents/debug-agentic-workflow.agent.md`

## Installing gh-aw CLI

The `gh-aw` CLI extension is required to work with agentic workflows:

```bash
# Install gh-aw extension
curl -fsSL https://raw.githubusercontent.com/githubnext/gh-aw/refs/heads/main/install-gh-aw.sh | bash

# Verify installation
gh aw version
```

## Working with Agentic Workflows

### Workflow File Format

Agentic workflows use markdown files with YAML frontmatter:

```markdown
---
on:
  push:
    branches: [main]

permissions:
  contents: read
  pull-requests: write

tools:
  github:
    toolsets: [all]
  bash: [":*"]

timeout-minutes: 15
---

# Workflow Title

Natural language instructions for what the AI should do.
Use GitHub context like ${{ github.repository }}.
```

### Compiling Workflows

**⚠️ Critical**: Workflow markdown files must be compiled to GitHub Actions YAML before they run.

```bash
# Compile all workflows in .github/workflows/
gh aw compile

# Compile a specific workflow
gh aw compile update-docs

# Compile with strict security validation
gh aw compile --strict

# Remove orphaned lock files
gh aw compile --purge
```

**Compilation process**:
- `.github/workflows/example.md` → `.github/workflows/example.lock.yml`
- Dependencies are resolved and merged
- Tool configurations are processed
- GitHub Actions syntax is generated

### Creating a New Workflow

1. **Create workflow markdown file**:
   ```bash
   # Create in .github/workflows/
   touch .github/workflows/my-workflow.md
   ```

2. **Define frontmatter and instructions**:
   ```markdown
   ---
   on:
     issues:
       types: [opened]
   
   permissions:
     issues: write
   
   tools:
     github:
       allowed:
         - add_issue_comment
   ---
   
   # My Workflow
   
   When an issue is opened, analyze it and add a helpful comment.
   ```

3. **Compile the workflow**:
   ```bash
   gh aw compile my-workflow
   ```

4. **Commit both files**:
   ```bash
   git add .github/workflows/my-workflow.md
   git add .github/workflows/my-workflow.lock.yml
   git commit -m "feat: add my-workflow agentic workflow"
   ```

### Modifying Existing Workflows

1. **Edit the markdown file** (`.md`), not the lock file (`.lock.yml`)
2. **Recompile** after changes:
   ```bash
   gh aw compile workflow-name
   ```
3. **Commit both files** (the `.md` source and updated `.lock.yml`)

**Never edit `.lock.yml` files directly** - they are generated and will be overwritten.

## Workflow Configuration

### Triggers

Common trigger patterns:

```yaml
# Push to branches
on:
  push:
    branches: [main, develop]

# Pull requests
on:
  pull_request:
    types: [opened, synchronize]

# Issues
on:
  issues:
    types: [opened, labeled]

# Scheduled
on:
  schedule:
    - cron: "0 14 * * 1-5"  # 2 PM UTC, weekdays only

# Manual dispatch
on:
  workflow_dispatch:

# Slash commands (mentions)
on:
  command: "/my-command"
```

### Permissions

Grant minimal required permissions:

```yaml
permissions:
  contents: read      # Read repository content
  issues: write       # Create/update issues
  pull-requests: write # Create/update PRs
  discussions: write  # Create/update discussions
```

### Tools

Available tool categories:

```yaml
tools:
  # GitHub API access
  github:
    toolsets: [all]  # or specify allowed operations
    allowed:
      - create_issue
      - add_issue_comment
  
  # File operations
  edit:      # Edit files
  view:      # View files
  
  # Web access
  web-fetch:  # Fetch web content
  web-search: # Search the web
  
  # Shell commands (whitelist patterns)
  bash:
    - "git status"
    - "make:*"
    - "gh label:*"
  
  # Browser automation
  playwright:
```

### Network Access

Control network access for security:

```yaml
# Default: localhost only
network: defaults

# Allow specific ecosystems
network:
  - node    # npm registry
  - python  # PyPI
  - go      # Go modules

# Allow specific domains
network:
  - "api.example.com"
  - "*.github.com"
```

## Security Best Practices

1. **Minimal Permissions**: Grant only required permissions
2. **Tool Whitelisting**: Use fine-grained tool access controls
3. **Network Restrictions**: Limit network access to necessary domains
4. **Fork Protection**: Configure fork allowlists for `pull_request` triggers
5. **Strict Compilation**: Use `gh aw compile --strict` to validate security

### Fork Protection

By default, workflows block all forks and only allow same-repo PRs:

```yaml
on:
  pull_request:
    forks: ["*"]  # Allow all forks
    # or
    forks: ["llm-d/*"]  # Allow organization forks only
```

### Manual Approval

Require manual approval for sensitive operations:

```yaml
on:
  issues:
    types: [labeled]
  manual-approval: true  # Requires environment protection rules

environment: production  # Must have protection rules configured
```

## Debugging Workflows

### View Workflow Runs

```bash
# List recent runs
gh run list --workflow=update-docs.lock.yml

# View specific run
gh run view <run-id>

# View logs
gh run view <run-id> --log
```

### Common Issues

**Workflow not triggering:**
- Verify `.lock.yml` file exists
- Check trigger conditions match event
- Ensure permissions are granted

**Compilation fails:**
- Run `gh aw compile --strict` for detailed errors
- Check YAML frontmatter syntax
- Verify tool configurations are valid

**Workflow fails during execution:**
- Check workflow run logs in GitHub Actions
- Verify required tools are available
- Check network/permission restrictions

## MCP Servers

Model Context Protocol (MCP) servers provide reusable capabilities:

```yaml
mcp-servers:
  my-custom-server:
    command: "node"
    args: ["path/to/mcp-server.js"]
    env:
      API_KEY: ${{ secrets.API_KEY }}
```

Inspect available MCP servers:

```bash
# List all MCP servers
gh aw mcp inspect

# Inspect specific server
gh aw mcp inspect --server my-custom-server

# List tools provided by server
gh aw mcp inspect --server my-custom-server --tool
```

## Advanced Features

### Workflow Expiration

Workflows can expire after a specified time:

```yaml
on:
  push:
    branches: [main]
  stop-after: +1mo  # Expires after 1 month
```

Supported time units: `h` (hours), `d` (days), `mo` (months)

### Emoji Reactions

Add emoji reactions to triggering items:

```yaml
on:
  issues:
    types: [opened]
  reaction: "👀"  # Adds eyes emoji to new issues
```

### Custom Run Names

Customize workflow run names:

```yaml
run-name: "Update docs for ${{ github.event.head_commit.message }}"
```

### Safe Outputs

Control how workflows create GitHub resources:

```yaml
safe-outputs:
  create-pull-request:
    draft: true  # Always create draft PRs
  create-issue:
    labels: ["automated"]
  create-discussion:
    category: "Announcements"
```

## Linting and Validation

Run security scanners on workflows:

```bash
# Run actionlint (includes shellcheck)
gh aw compile --actionlint

# Run Zizmor security scanner
gh aw compile --zizmor

# Run Poutine supply chain analyzer
gh aw compile --poutine

# Run all scanners with strict mode
gh aw compile --strict --actionlint --zizmor --poutine
```

## Resources

- **Official Documentation**: [gh-aw GitHub Repository](https://github.com/githubnext/gh-aw)
- **Schema Reference**: `.github/aw/github-agentic-workflows.md`
- **Agent Templates**: `.github/agents/`
- **Example Workflows**: `.github/workflows/*.md`

## Getting Help

- Review the [gh-aw documentation](https://github.com/githubnext/gh-aw)
- Check `.github/aw/github-agentic-workflows.md` for schema details
- Use the `create-agentic-workflow` agent for interactive guidance
- Open issues in the [gh-aw repository](https://github.com/githubnext/gh-aw/issues)

## Best Practices

1. **Keep workflows focused**: Each workflow should have a single, clear purpose
2. **Use descriptive names**: Workflow files and titles should clearly indicate their function
3. **Test locally**: Use `gh aw compile --strict` before committing
4. **Document workflows**: Add clear natural language instructions
5. **Version control**: Always commit both `.md` and `.lock.yml` files
6. **Security first**: Use minimal permissions and tool access
7. **Regular maintenance**: Review and update workflows as repository needs change
8. **Monitor runs**: Check workflow execution logs regularly for issues

---

**Note**: The gh-aw extension was upgraded from v0.31.10 to v0.33.12 on December 22, 2025, to fix npm permissions issues in the sandbox container.
