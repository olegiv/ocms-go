# Claude Code Extensions for oCMS

This directory contains specialized AI agents and slash commands tailored for oCMS development.

## Directory Structure

```
.claude/
├── agents/              # Specialized AI agents for complex tasks
│   ├── test-runner.md
│   ├── db-manager.md
│   ├── api-developer.md
│   ├── module-developer.md
│   ├── security-auditor.md
│   ├── code-quality-auditor.md
│   └── frontend-developer.md
├── commands/            # Quick slash commands for common workflows
│   ├── test.md
│   ├── build.md
│   ├── migrate.md
│   ├── sqlc-generate.md
│   ├── dev-server.md
│   ├── api-test.md
│   ├── code-quality.md
│   ├── security-audit.md
│   ├── clean.md
│   ├── commit-prepare.md
│   ├── commit-do.md
│   ├── templui-add.md
│   └── templui-list.md
└── settings.local.json  # Local settings (gitignored)
```

## Agents

Agents are specialized AI assistants for complex, multi-step tasks.

### test-runner
Runs Go tests, debugs failures, and adds test coverage.
- Handles test execution with proper environment setup
- Analyzes test failures and suggests fixes
- Creates new test cases following project patterns

### db-manager
Manages database migrations (goose) and SQLC code generation.
- Creates and applies database migrations
- Regenerates SQLC Go code from SQL queries
- Maintains database schema consistency

### api-developer
Develops and tests REST API endpoints.
- Creates new API endpoints with authentication
- Tests API functionality with real HTTP requests
- Debugs API issues and validates responses

### module-developer
Creates and manages the module system.
- Builds new modules with proper structure
- Implements module hooks and lifecycle
- Adds i18n translations for modules

### security-auditor
Reviews source, configuration and existing scanner output with read-only tools.
Returns Markdown findings and suggested fixes to the caller, who saves any
separate section reports.

### frontend-developer
Creates and modifies admin UI components using templ and templUI.
- Enforces templUI-first policy for all new components
- Manages templUI CLI workflow (init, add, update, list)
- Handles embedded asset integration (Tailwind CSS v4, JS, `//go:embed`)
- Knows import path patterns and theme variable mappings
- Guides JavaScript component integration into admin layout

## Commands

Commands are quick, predefined workflows for common tasks.

### /test
Run all tests with coverage reporting.

### /build
Build production binary with asset compilation.

### /migrate
Check and apply database migrations.

### /sqlc-generate
Regenerate SQLC code from SQL queries.

### /dev-server
Start development server with hot reload.

### /api-test
Test API endpoints with curl requests.

### /security-audit
Hands off to the terminal launcher, which runs the audit and saves its report.

### /code-quality
Run code quality checks for the Go project, including CSP template safety and high-risk DOM-XSS sink checks (`innerHTML`, redirect sinks).

### /clean
Clean build artifacts and temp files.

### /commit-prepare
Review changes and prepare a commit message draft.

### /commit-do
Hand off the prepared commit to the terminal helper for final approval.

### /templui-add
Add templUI components to the project. Handles CLI installation, `.templui.json` initialization, component download, Tailwind CSS source scanning, and post-install steps.

### /templui-list
List available templUI components or fetch documentation for a specific component. Shows installed vs available status.

## Usage

### Using Agents

Invoke agents with `@agent-name` followed by your task:

```
@test-runner Run all tests and report any failures

@db-manager Create a migration to add a comments table

@api-developer Add a new endpoint for user statistics

@module-developer Create a notification module with email hooks

@security-auditor Review the authentication code and return your findings

@frontend-developer Build a confirmation dialog for page deletion using templUI
```

### Using Commands

Invoke commands with `/command-name`:

```
/test

/build

/migrate

/sqlc-generate

/dev-server

/api-test

/code-quality

/security-audit

/clean

/commit-prepare

/commit-do

/templui-list

/templui-add button card
```

### Using Codex-Compatible Command Wrapper

If you are running in Codex, use the local wrapper that mirrors the
Claude slash-command workflow:

Note: Codex UI does not register these Claude slash commands, so typing
`/code-quality`, `/security-audit`, `/commit-prepare`, or `/commit-do`
in Codex may show `No commands`.

```bash
./scripts/codex-commands code-quality
./scripts/codex-commands security-audit
./scripts/codex-commands commit-prepare
./scripts/codex-commands commit-do

# Explicit local fallback scripts:
./scripts/codex-commands code-quality-local
./scripts/codex-commands security-audit-local
./scripts/codex-commands commit-prepare-local
./scripts/codex-commands commit-do-local

# Equivalent Make targets:
make code-quality
make security-audit
make commit-prepare
make commit-do
make code-quality-local
make security-audit-local
make commit-prepare-local
make commit-do-local

# Or ask Codex in chat to run them:
run code-quality
run security-audit
run commit-prepare
run commit-do
```

### Running Claude Command Directly from CLI

You can also run the Claude slash-command workflow directly from shell:

```bash
./scripts/codex/proxy-claude-command.sh /commit-prepare
./scripts/codex/proxy-claude-command.sh /commit-do
./scripts/codex/proxy-claude-command.sh /code-quality
./scripts/codex/proxy-claude-command.sh /security-audit
```

`commit-prepare` tests the staged tree by default. To prepare an exact
selection without changing the real index, use
`./scripts/codex-commands commit-prepare -- FILE ...` with individual
repository-relative files. Put `quality` or `q` before `--` to also run
`golangci-lint` in the snapshot. Preparation runs the asset build and full
test gate in a fresh snapshot with the recorded shared tooling revision.
Claude drafts from the tested diff with tools disabled. The local variant
uses the same gate and saves a draft for you to edit.

`commit-do` and `commit-do-local` run the same terminal command. Stage the
files listed at preparation, then review and approve the displayed draft by
typing `YES`. The command verifies the prepared parent and staged tree,
reruns the snapshot gate, and keeps Git's hooks enabled. An active commit hook
other than the shipped read-only checks requires a guarded commit workflow.

`make security-audit` saves Claude's final Markdown in a new
`.audit/run-*/report.md`. `make security-audit-local` runs the dependency
scanners directly and saves its summary and logs in its own run directory.
Both launchers archive regular top-level `security-audit-YYYY-MM-DD_HH-MM-SS.md`
reports with their matching `govulncheck` and `npm-audit` logs. This also
includes manually produced reports with that name. Other files and
directories, including `TODO.md`, section notes and prior runs, stay in place.
If preparation was interrupted and no audit is active, run
`rmdir -- .audit/.archive.lock` before retrying.
When you ask `@security-auditor` for a section review, save its returned
Markdown yourself if the section needs a separate file. The proxy's available
checks are listed in `scripts/codex/security-audit-proxy.md`.

Code quality and audit sessions use `dontAsk` with command-specific tool
permissions. The proxy reports checks that require manual execution.
Run snapshot preparation only for trusted code. A fresh snapshot controls
which source is tested, and the proxy allowlist is a best-effort guard.

## When to Use Which

**Use Agents when:**
- Task is complex and multi-step
- Need specialized knowledge (testing, security, APIs, etc.)
- Want detailed analysis and recommendations
- Building new features or debugging issues

**Use Commands when:**
- Task is straightforward and common
- Want quick execution of predefined workflow
- Running standard development operations
- Need consistent, repeatable results

## Examples

### Testing Workflow
```
# Quick test run
/test

# Detailed test analysis with debugging
@test-runner Debug why the cache tests are failing
```

### Database Workflow
```
# Quick migration apply
/migrate

# Create new migration with proper structure
@db-manager Create a migration for a webhooks table with proper indexes
```

### API Development
```
# Quick API test
/api-test

# Build new API endpoint
@api-developer Add a POST endpoint for creating comments with validation
```

### Security Audit
```
# Fresh dependency scan in a terminal
make security-audit-local

# Read-only section review in Claude Code
@security-auditor Review all authentication flows and check for vulnerabilities
```

### templUI Workflow
```
# List available components
/templui-list

# Get docs for a specific component
/templui-list datepicker

# Add components to the project
/templui-add button card dialog

# Complex UI task using agent
@frontend-developer Build a confirmation dialog for page deletion using templUI
```

## Customization

You can create additional agents and commands by adding markdown files to the respective directories:

**Agent format** (`.claude/agents/my-agent.md`):
```markdown
---
name: my-agent
description: What this agent does and when to use it. Include examples.
model: sonnet
---

[Agent instructions and context]
```

**Command format** (`.claude/commands/my-command.md`):
```markdown
[Command description and steps to execute]
```

## Documentation

Full documentation for Claude Code extensions is in `CLAUDE.md`

## Generated By

These extensions were automatically generated by analyzing the oCMS project's:
- Tech stack (Go 1.27.1, chi router, SQLC, goose, SQLite)
- Build tools (Make, npm, SCSS compilation, go test)
- Frontend dependencies (htmx, Alpine.js via npm)
- Development workflows (testing, migrations, API development)
- Module system architecture
- Security requirements

They are tailored specifically for oCMS development patterns and best practices.
