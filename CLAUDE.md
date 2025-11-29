# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Development Commands

```bash
# Run development server (requires OCMS_SESSION_SECRET env var)
OCMS_SESSION_SECRET=your-secret-key-32-bytes make dev

# Run without rebuilding assets
make run

# Build production binary
make build

# Run tests (requires session secret)
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test ./...

# Run single package tests
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test -v ./internal/store/...

# Check for vulnerabilities
govulncheck ./...

# Compile SCSS to CSS
make assets

# Database migrations
make migrate-up          # Apply migrations
make migrate-down        # Rollback
make migrate-create      # Create new migration
```

## Code Generation

After modifying SQL queries or migrations, regenerate code:

```bash
sqlc generate            # Regenerate store/*.sql.go from queries/*.sql
templ generate           # Regenerate template Go code (if using templ)
```

## Architecture Overview

**Request Flow**: HTTP Request → chi router → middleware chain → handler → store (sqlc) → SQLite

### Key Architectural Patterns

1. **Database Layer (sqlc)**: All database access uses sqlc-generated code. To add/modify queries:
   - Write SQL in `internal/store/queries/*.sql` with sqlc annotations
   - Run `sqlc generate` to create type-safe Go code
   - Migrations live in `internal/store/migrations/` (goose format)

2. **Embedded Assets**: Templates and static files are embedded using `//go:embed` in `web/embed.go`. After modifying CSS/SCSS, run `make assets` to compile.

3. **Handler Pattern**: Each handler struct (in `internal/handler/`) receives `*sql.DB`, `*render.Renderer`, and `*scs.SessionManager`. Handlers call `store.New(db)` to get sqlc queries.

4. **Middleware Chain**: Protected routes use three middleware in order:
   - `middleware.Auth` - validates session
   - `middleware.LoadUser` - loads user into context
   - `middleware.LoadSiteConfig` - loads site config into context

5. **Session Management**: Uses SCS with SQLite store. Session data accessed via context key `middleware.UserContextKey`.

### Package Dependencies

```
cmd/ocms/main.go
    ├── internal/config      (env var loading)
    ├── internal/handler     (HTTP handlers)
    │   ├── internal/render  (template rendering)
    │   ├── internal/store   (sqlc database access)
    │   └── internal/service (business logic)
    ├── internal/middleware  (auth, user loading)
    ├── internal/session     (SCS session manager)
    └── web                  (embedded templates/static)
```

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `OCMS_SESSION_SECRET` | Yes | - | Min 32 bytes for session encryption |
| `OCMS_DB_PATH` | No | `./data/ocms.db` | SQLite database path |
| `OCMS_SERVER_PORT` | No | `8080` | Server port |
| `OCMS_ENV` | No | `development` | Set to `production` for prod |

## Default Credentials

On first run, seeds admin user: `admin@example.com` / `changeme`
