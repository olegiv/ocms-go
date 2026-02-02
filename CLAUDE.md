## Build and Development Commands

```bash
# Run development server (requires OCMS_SESSION_SECRET env var)
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! make dev

# Run without rebuilding assets
make run

# Build production binary
make build

# Run tests (requires session secret)
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test ./...

# Run single package tests
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./internal/store/...

# Check for vulnerabilities
govulncheck ./...

# Build assets (install npm deps, compile SCSS)
make assets

# Update JS dependencies
npm update                # Update htmx, alpine.js in package.json
make assets               # Reinstall and copy to static/dist/js

# Database migrations
make migrate-up          # Apply migrations
make migrate-down        # Rollback
make migrate-create      # Create new migration
```

## Go Toolchain Requirements

**CRITICAL**: If you see this error during build or test:
```
compile: version "go1.X.Y" does not match go tool version "go1.X.Z"
```

**STOP IMMEDIATELY** and fix it before proceeding. This indicates a corrupted or mismatched Go installation.

**Diagnosis**:
```bash
go version                    # Reports go binary version
go env GOTOOLDIR              # Shows toolchain location
$(go env GOTOOLDIR)/compile -V  # Shows actual compiler version
```

**Fix**: The local Go installation must match the version in `go.mod`. Upgrade Go:
```bash
# Download from https://go.dev/dl/
curl -LO https://go.dev/dl/go<VERSION>.darwin-arm64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go<VERSION>.darwin-arm64.tar.gz
```

**NEVER** downgrade the Go version in `go.mod` to work around this issue.

## Code Generation

After modifying SQL queries or migrations, regenerate code:

```bash
sqlc generate            # Regenerate store/*.sql.go from queries/*.sql
templ generate           # Regenerate template Go code (if using templ)
```

## Git Pre-Commit Hook

This repository has a pre-commit hook at `.git/hooks/pre-commit` that blocks automated commits in non-interactive mode. It prevents Claude Code from committing on its own initiative.

**When the user explicitly requests a commit** (e.g., `/commit-do`, "commit these changes"), Claude Code must use `--no-verify` to bypass the hook. The user's explicit request IS the approval.

```bash
# User-requested commits: use --no-verify
git commit --no-verify -m "message"

# Manual commits in terminal: hook prompts for "YES"
git commit -m "message"
```

The hook source is in `.claude/shared/global/hooks/`.

## Architecture Overview

**Request Flow**: HTTP Request → chi router → middleware chain → handler → store (sqlc) → SQLite

### Key Architectural Patterns

1. **Database Layer (sqlc)**: All database access uses sqlc-generated code. To add/modify queries:
   - Write SQL in `internal/store/queries/*.sql` with sqlc annotations
   - Run `sqlc generate` to create type-safe Go code
   - Migrations live in `internal/store/migrations/` (goose format)

2. **Embedded Assets**: Templates and static files are embedded using `//go:embed` in `web/embed.go`. JS dependencies (htmx, Alpine.js) are managed via `package.json` and copied to `web/static/dist/js/` during build. Run `make assets` to compile SCSS and install JS deps. After modifying JS dependencies, use `go build -a` to force re-embedding.

   **Frontend JS Policy**: Always prefer official Alpine.js plugins (`@alpinejs/*`) over third-party libraries. For example, use `@alpinejs/sort` instead of Sortable.js, `@alpinejs/mask` instead of input mask libraries. This reduces external dependencies and ensures consistent integration with Alpine's reactivity system.

3. **Handler Pattern**: Each handler struct (in `internal/handler/`) receives `*sql.DB`, `*render.Renderer`, and `*scs.SessionManager`. Handlers call `store.New(db)` to get sqlc queries.

4. **Middleware Chain**: Protected routes use middleware in order:
   - `middleware.SecurityHeaders` - adds CSP, HSTS, X-Frame-Options, etc.
   - `middleware.CSRF` - validates CSRF token on POST/PUT/DELETE requests
   - `middleware.Auth` - validates session
   - `middleware.LoadUser` - loads user into context
   - `middleware.LoadSiteConfig` - loads site config into context

5. **Session Management**: Uses SCS with SQLite store. Session data accessed via context key `middleware.UserContextKey`.

6. **API Middleware**: REST API routes use different middleware:
   - `middleware.APIKeyAuth` - validates Bearer token
   - `middleware.RequirePermission` - checks API key permissions
   - `middleware.APIRateLimit` - per-key rate limiting

7. **Theme System** (`internal/theme/`): Core themes (default, developer) are embedded in the binary (`internal/themes/`). Custom themes can be placed in `custom/themes/` to override or extend core themes. Each theme has templates, static assets, and optional locale overrides. Use `TTheme` in theme templates for translations with theme→global fallback.

8. **Module System** (`internal/module/`): Extensible plugin architecture with lifecycle management, migrations, active status toggle, and embedded i18n translations.

9. **Caching** (`internal/cache/`): Supports both in-memory and Redis caching with automatic fallback. Caches site config, menus, languages, and sitemaps. Set `OCMS_REDIS_URL` for distributed caching across multiple instances.

10. **Scheduler** (`internal/scheduler/`): Cron-based task scheduler for scheduled publishing.

### Package Dependencies

```
cmd/ocms/main.go
    ├── internal/cache       (in-memory + Redis caching)
    ├── internal/config      (env var loading)
    ├── internal/handler     (HTTP handlers)
    │   ├── api/             (REST API handlers)
    │   ├── internal/render  (template rendering)
    │   ├── internal/store   (sqlc database access)
    │   └── internal/service (business logic)
    ├── internal/middleware  (auth, API auth, rate limiting)
    ├── internal/module      (module system, hooks)
    ├── internal/scheduler   (cron scheduler)
    ├── internal/seo         (sitemap, robots.txt, meta)
    ├── internal/session     (SCS session manager)
    ├── internal/theme       (theme loading/rendering)
    ├── internal/themes      (embedded core themes)
    ├── modules/             (built-in modules)
    ├── custom/              (user content: themes, modules)
    └── web                  (embedded admin templates/static)
```

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `OCMS_SESSION_SECRET` | Yes | - | Min 32 bytes for session encryption |
| `OCMS_DB_PATH` | No | `./data/ocms.db` | SQLite database path |
| `OCMS_SERVER_PORT` | No | `8080` | Server port |
| `OCMS_ENV` | No | `development` | Set to `production` for prod |
| `OCMS_CUSTOM_DIR` | No | `./custom` | Directory for custom themes/modules |
| `OCMS_ACTIVE_THEME` | No | `default` | Name of active theme |
| `OCMS_API_RATE_LIMIT` | No | `100` | API requests per minute per key |
| `OCMS_REDIS_URL` | No | - | Redis URL for distributed cache (e.g., `redis://localhost:6379/0`) |
| `OCMS_CACHE_PREFIX` | No | `ocms:` | Key prefix for Redis cache entries |
| `OCMS_CACHE_TTL` | No | `3600` | Default cache TTL in seconds |
| `OCMS_CACHE_MAX_SIZE` | No | `10000` | Max entries for in-memory cache |
| `OCMS_HCAPTCHA_SITE_KEY` | No | - | hCaptcha site key (overrides database setting) |
| `OCMS_HCAPTCHA_SECRET_KEY` | No | - | hCaptcha secret key (overrides database setting) |
| `OCMS_GEOIP_DB_PATH` | No | - | Path to GeoLite2-Country.mmdb for country detection |
| `OCMS_DO_SEED` | No | `false` | Enable database seeding (admin user, config, menus) |

## Default Credentials

When `OCMS_DO_SEED=true`, seeds admin user: `admin@example.com` / `changeme1234`

Seeding is opt-in to prevent automatic recreation of deleted data on restart.

## Key Endpoints

### Admin Routes

**Editor Routes (editor + admin access):**
- `/admin/` - Dashboard
- `/admin/events` - Event log
- `/admin/pages` - Page management (list, new, edit)
- `/admin/tags` - Tag management (list, new, edit)
- `/admin/categories` - Category management (list, new, edit)
- `/admin/media` - Media library (list, upload, edit)
- `/admin/menus` - Menu management (list, new, edit)
- `/admin/forms` - Form builder (list, new, edit, submissions)
- `/admin/widgets` - Widget management
- `/admin/themes/settings` - Theme settings (for active theme)

**Admin-Only Routes:**
- `/admin/users` - User management (list, new, edit)
- `/admin/languages` - Language management (list, new, edit)
- `/admin/config` - Site configuration
- `/admin/themes` - Theme list and activation
- `/admin/modules` - Module list and toggle
- `/admin/api-keys` - API key management (list, new, edit)
- `/admin/webhooks` - Webhook management (list, new, edit, deliveries)
- `/admin/cache` - Cache statistics and clear
- `/admin/export` - Content export
- `/admin/import` - Content import
- `/admin/docs` - Site documentation

### REST API
- `GET /api/v1/pages` - List pages (public: published only)
- `POST /api/v1/pages` - Create page (requires `pages:write`)
- `GET /api/v1/media` - List media
- `GET /api/v1/tags` - List tags
- `GET /api/v1/categories` - List categories (tree)
- `GET /api/v1/docs` - API documentation

### Health Check Routes
- `GET /health` - Overall health status (200 OK / 503 Service Unavailable)
- `GET /health/live` - Liveness probe (always returns `{"status":"alive"}`)
- `GET /health/ready` - Readiness probe (checks database connectivity)

**Public (unauthenticated):** Returns only `{"status":"healthy"}` or `{"status":"degraded"}` — no internal details exposed.

**Authenticated (admin session or API key):** Returns full details including uptime, version, database latency, disk space. Add `?verbose=true` for Go runtime info (goroutines, memory, CPU count).

```bash
# Public — minimal status only
curl http://localhost:8080/health

# Authenticated — full details via API key
curl -H "Authorization: Bearer YOUR_API_KEY" http://localhost:8080/health

# Authenticated with system info
curl -H "Authorization: Bearer YOUR_API_KEY" "http://localhost:8080/health?verbose=true"
```

### SEO Routes
- `/sitemap.xml` - Auto-generated sitemap
- `/robots.txt` - Robots configuration

## Testing Requirements

**CRITICAL**: After ANY code change (no exceptions - includes refactoring, renaming, style fixes, test changes, etc.), you MUST:

1. **Rebuild assets**: Run `make assets` to install JS deps and compile SCSS
2. **Restart the server**: Run `make dev` or restart any running server
3. **Test the application with HTTP requests**:
   - Verify homepage loads: `curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/`
   - Verify admin dashboard loads: `curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/admin/`
   - Test health endpoint: `curl -s http://localhost:8080/health`
   - Test any affected endpoints with actual HTTP requests
4. **Keep the server running**: Do not stop the server after testing

**NO EXCEPTIONS**: Even "simple" changes like type renames, import changes, or test fixes can break the UI. Always verify with HTTP requests.

Example testing workflow:
```bash
# Start server in background
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go run ./cmd/ocms &

# Wait for server to start
sleep 3

# Test health endpoint
curl -s http://localhost:8080/health

# Test API endpoints
curl -s http://localhost:8080/api/v1/pages
curl -s http://localhost:8080/api/v1/tags
curl -s http://localhost:8080/sitemap.xml

# Test with API key authentication
curl -H "Authorization: Bearer YOUR_API_KEY" http://localhost:8080/api/v1/pages

# Kill server when done
pkill -f "go run ./cmd/ocms" || true
```

Never tell the user to "restart the server and test" - always run the tests yourself first.

## Code Quality Requirements

**CRITICAL**: All code MUST be free of the following warnings and issues:

- **Duplicate code**: Avoid copy-paste code blocks. Extract common logic into reusable functions. **NEVER add `//nolint:dupl` comments** - always fix the duplication by refactoring.
- **Unhandled errors**: All errors MUST be checked and handled appropriately. Never ignore returned errors.
- **Condition is always 'false'/'true'**: Remove dead code and unreachable branches. Ensure all conditions can evaluate to both outcomes.
- **Unused variables/imports**: Remove any unused declarations.
- **Shadowed variables**: Avoid shadowing variables from outer scopes.
- **Resource leaks**: Always defer `Close()` for `sql.Rows` in the caller, even if a helper also closes.

Before submitting code, verify with:
```bash
go vet ./...              # Check for common issues
staticcheck ./...         # Extended static analysis
errcheck ./...            # Detect unhandled errors
```

Install errcheck if needed: `go install github.com/kisielk/errcheck@latest`

**Detecting "Condition is always false/true":** Standard tools don't catch constant comparisons. Perform semantic analysis manually:
1. Find constant definitions: `grep -rn "^const\|^\tconst" --include="*.go" .`
2. Find comparisons to those constants in test files
3. Evaluate if comparisons like `if CONST != literal` are always false (when CONST equals that literal)
4. Remove useless tests that compare constants to their own values

**Detecting "Empty slice declaration using literal":** Use `var x []Type` instead of `x := []Type{}`.
```bash
grep -rn ":= \[\][a-zA-Z.]*{}" --include="*.go" .
```
- `x := []Type{}` creates non-nil empty slice (usually unnecessary)
- `var x []Type` creates nil slice (idiomatic Go)
- Exception: Use literal when nil vs empty matters (e.g., JSON marshaling)

**Detecting "Variable collides with imported package name":** Never use variable names that match imported package names.

Common packages to check: `bytes`, `context`, `crypto`, `encoding`, `errors`, `fmt`, `hash`, `html`, `http`, `io`, `json`, `log`, `math`, `net`, `os`, `path`, `reflect`, `regexp`, `runtime`, `sort`, `sql`, `strconv`, `strings`, `sync`, `template`, `testing`, `time`, `unicode`, `url`, `xml`

Detection method:
1. Find files importing a package: `grep -l '"net/url"' --include="*_test.go" -r .`
2. Check if those files use the package name as a variable: `grep -n 'url :=' <file>`
3. Rename colliding variables to `got`, `result`, `gotURL`, etc.

Example fix:
```go
// BAD: 'url' shadows imported "net/url" package
url := p.PageURL(2)

// GOOD: Use descriptive name that doesn't collide
got := p.PageURL(2)
```

**Detecting "Potential resource leak":** Always defer `Close()` in the caller when using `sql.Rows`.

When `QueryContext` returns `rows`, always add `defer rows.Close()` in the same function, even if a helper function also closes it.

Detection method:
```bash
grep -rn "QueryContext\|Query(" --include="*.go" . | grep -v "_test.go"
```

Check if `rows` is passed to a helper without a local defer.

Example fix:
```go
// BAD: rows passed to helper without local defer - if helper panics, leak occurs
rows, err := db.QueryContext(ctx, query)
if err != nil {
    return nil
}
return scanRows(rows)

// GOOD: always defer close in caller
rows, err := db.QueryContext(ctx, query)
if err != nil {
    return nil
}
defer func() { _ = rows.Close() }()
return scanRows(rows)
```

Note: `rows.Close()` is idempotent - calling it twice is safe.

## Code Style

### License Headers

All `.go` source files must include the SPDX short form header:
```go
// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package <name>
```

### Error Handling

**Wrapping errors:**
```go
// Use fmt.Errorf with %w for wrapped errors
if err != nil {
    return fmt.Errorf("failed to create page: %w", err)
}
```

**Web handlers:** Use flash messages and redirects for user-facing errors
```go
if errors.Is(err, sql.ErrNoRows) {
    flashError(w, r, h.renderer, redirectURL, "Page not found")
} else {
    slog.Error("failed to get page", "error", err, "page_id", id)
    flashError(w, r, h.renderer, redirectURL, "Error loading page")
}
```

**API handlers:** Use typed JSON error responses
```go
slog.Error("failed to fetch data", "error", err)
api.WriteInternalError(w, "Error loading data")
```

### Constants

Use constants for magic values:
```go
const (
    PagesPerPage    = 20
    VersionsPerPage = 10
    DefaultTimeout  = 30 * time.Second
)
```

Use typed context keys to prevent collisions:
```go
type ContextKey string

const (
    ContextKeyUser     ContextKey = "user"
    ContextKeySiteName ContextKey = "siteName"
)
```

### Cleanup

Use defer for cleanup immediately after acquiring resources:
```go
db, err := sql.Open("sqlite", path)
if err != nil {
    return err
}
defer db.Close()
```

### Logging (slog)

Use structured logging with consistent field names:
```go
// Errors: always include "error" field and relevant IDs
slog.Error("failed to create page", "error", err, "user_id", userID)

// Info: log actions with entity IDs for audit trails
slog.Info("page created", "page_id", page.ID, "slug", page.Slug, "created_by", userID)
slog.Info("page deleted", "page_id", id, "deleted_by", userID)
```

### Naming Conventions

| Type | Convention | Example |
|------|------------|---------|
| Constants | PascalCase with prefix | `SessionKeyUserID`, `ContextKeyUser` |
| Handler methods | HTTP verb pattern | `List`, `Create`, `Update`, `Delete` |
| Validation funcs | `validate` + Field | `validatePageTitle`, `ValidateSlugForUpdate` |
| Helper funcs | action + Entity | `buildPageTree`, `parseFormInput` |
| Test funcs | `Test` + FuncName | `TestNewPagesHandler`, `TestParsePageForm` |

### Validation Functions

Return empty string for success, error message for failure:
```go
func validateSlug(slug string) string {
    if slug == "" {
        return "Slug is required"
    }
    if !util.IsValidSlug(slug) {
        return "Invalid slug format"
    }
    return "" // Valid
}
```

### Function Documentation

Comments start with the function/type name:
```go
// NewPagesHandler creates a new PagesHandler with the given dependencies.
func NewPagesHandler(db *sql.DB, renderer *render.Renderer) *PagesHandler { ... }

// List handles GET /admin/pages - displays a paginated list of pages.
func (h *PagesHandler) List(w http.ResponseWriter, r *http.Request) { ... }
```

## Testing Specific Components

```bash
# Test cache layer
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./internal/cache/...

# Test theme system
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./internal/theme/...

# Test module system
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./internal/module/...

# Test API middleware
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./internal/middleware/...

# Test API handlers
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./internal/handler/api/...
```

## Documentation

Additional documentation is available in the `docs/` directory:

- `docs/media.md` - Media library, image variants, and storage structure
- `docs/multi-language.md` - Multi-language content setup and translation workflow
- `docs/webhooks.md` - Webhook configuration and event handling
- `docs/import-export.md` - Content backup, export, and import guide
- `docs/reverse-proxy.md` - Nginx, Apache, and Nginx Proxy Manager configuration
- `docs/login-security.md` - Login protection, rate limiting, and account lockout
- `docs/developer-module.md` - Developer module for test data generation and i18n
- `docs/dbmanager-module.md` - DB Manager module for direct SQL query execution
- `docs/hcaptcha.md` - hCaptcha integration for bot protection on login
- `docs/i18n.md` - Internationalization, translation file format, theme translations, and `TTheme` usage
- `docs/csrf.md` - CSRF protection configuration, TrustedOrigins format, and troubleshooting

Security audit documents are available in the `.audit/` directory (gitignored).

## Claude Code Agents

Specialized AI agents are available in `.claude/agents/` for focused development tasks:

### test-runner
Expert Go test runner for the oCMS project. Handles running tests, debugging failures, and adding test coverage.

**Usage examples:**
- "Run all tests with coverage"
- "Test the cache package"
- "Debug failing API tests"
- "Add tests for the webhook feature"

**Invoke:** `@test-runner` or use the `/test` command

### db-manager
Database migration and query management specialist. Works with goose migrations and SQLC code generation.

**Usage examples:**
- "Create a migration for a comments table"
- "Regenerate SQLC code"
- "Add a query to fetch pages by tag"
- "Check migration status"

**Invoke:** `@db-manager` or use `/migrate` or `/sqlc-generate` commands

### api-developer
REST API development expert. Helps develop, test, and debug API endpoints with authentication and validation.

**Usage examples:**
- "Add a new API endpoint for comments"
- "Test the pages API endpoint"
- "Debug API authentication issues"
- "Add pagination to the media API"

**Invoke:** `@api-developer` or use the `/api-test` command

### module-developer
Module system specialist. Creates and manages modules with lifecycle hooks and i18n support.

**Usage examples:**
- "Create a new analytics module"
- "Add a hook to track page views"
- "Create translations for a module"
- "Debug module registration issues"

**Invoke:** `@module-developer`

### security-auditor
Security vulnerability scanner and auditor. Identifies security issues and ensures best practices.

**Usage examples:**
- "Scan for vulnerabilities"
- "Check for security issues in dependencies"
- "Review CSRF protection"
- "Audit API authentication"

**Invoke:** `@security-auditor` or use the `/security-scan` command

### code-quality-auditor
Code quality scanner for Go applications. Detects duplicate code, unhandled errors, constant comparisons, empty slice literals, and package name collisions.

**Usage examples:**
- "Run a quick code quality scan"
- "Check for unhandled errors"
- "Find duplicate code in tests"
- "Check for package name collisions"

**Invoke:** `@code-quality-auditor` or use the `/code-quality` command

## Claude Code Slash Commands

Quick commands for common development tasks:

### /test
Run all Go tests with verbose output and coverage reporting. Sets the required `OCMS_SESSION_SECRET` environment variable automatically.

### /build
Build the production binary. Compiles SCSS assets and creates the `bin/ocms` binary.

### /migrate
Manage database migrations using goose. Checks status and applies pending migrations.

### /sqlc-generate
Regenerate SQLC Go code from SQL queries in `internal/store/queries/`.

### /dev-server
Start the development server with asset compilation. Runs `make dev` and reports server status.

### /api-test
Test REST API endpoints with actual HTTP requests. Starts server, runs curl tests, and reports results.

### /security-scan
Scan the project for vulnerabilities using govulncheck. Saves audit report to `.audit/` directory.

### /code-quality
Scan the project for code quality issues including unhandled errors, duplicate code, constant comparisons, empty slice literals, and package name collisions. Runs `go vet`, `staticcheck`, and `errcheck`.

### /commit-prepare
Review changes and prepare a commit message. Runs `/code-quality` first and asks for confirmation if issues are found before proceeding with the commit message.

### /clean
Clean build artifacts, compiled binaries, and development databases.

## How to Use Claude Code Extensions

**Agents** - For complex, multi-step tasks requiring specialized knowledge:
```
@agent-name Your question or task here
```

**Commands** - For quick, predefined workflows:
```
/command-name
```

**Examples:**
```
@test-runner Run all tests and report coverage

/test

@db-manager Create a migration to add a comments table

/migrate

@api-developer Add a new endpoint for fetching user statistics

/api-test

@security-auditor Scan for vulnerabilities and create an audit report

/security-scan
```
