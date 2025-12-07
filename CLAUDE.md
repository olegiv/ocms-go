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

6. **API Middleware**: REST API routes use different middleware:
   - `middleware.APIKeyAuth` - validates Bearer token
   - `middleware.RequirePermission` - checks API key permissions
   - `middleware.APIRateLimit` - per-key rate limiting

7. **Theme System** (`internal/theme/`): Loads themes from `themes/` directory with templates, static assets, and optional locale overrides (`themes/{name}/locales/`). Use `TTheme` in theme templates for translations with theme→global fallback.

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
    ├── modules/             (custom modules)
    ├── themes/              (frontend themes)
    └── web                  (embedded templates/static)
```

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `OCMS_SESSION_SECRET` | Yes | - | Min 32 bytes for session encryption |
| `OCMS_DB_PATH` | No | `./data/ocms.db` | SQLite database path |
| `OCMS_SERVER_PORT` | No | `8080` | Server port |
| `OCMS_ENV` | No | `development` | Set to `production` for prod |
| `OCMS_THEMES_DIR` | No | `./themes` | Directory containing themes |
| `OCMS_ACTIVE_THEME` | No | `default` | Name of active theme |
| `OCMS_API_RATE_LIMIT` | No | `100` | API requests per minute per key |
| `OCMS_REDIS_URL` | No | - | Redis URL for distributed cache (e.g., `redis://localhost:6379/0`) |
| `OCMS_CACHE_PREFIX` | No | `ocms:` | Key prefix for Redis cache entries |
| `OCMS_CACHE_TTL` | No | `3600` | Default cache TTL in seconds |
| `OCMS_CACHE_MAX_SIZE` | No | `10000` | Max entries for in-memory cache |
| `OCMS_HCAPTCHA_SITE_KEY` | No | - | hCaptcha site key (overrides database setting) |
| `OCMS_HCAPTCHA_SECRET_KEY` | No | - | hCaptcha secret key (overrides database setting) |

## Default Credentials

On first run, seeds admin user: `admin@example.com` / `changeme`

## Key Endpoints

### Admin Routes
- `/admin/` - Dashboard
- `/admin/pages` - Page management
- `/admin/media` - Media library
- `/admin/themes` - Theme management
- `/admin/modules` - Module list
- `/admin/api-keys` - API key management
- `/admin/cache` - Cache management
- `/admin/hcaptcha` - hCaptcha settings

### REST API
- `GET /api/v1/pages` - List pages (public: published only)
- `POST /api/v1/pages` - Create page (requires `pages:write`)
- `GET /api/v1/media` - List media
- `GET /api/v1/tags` - List tags
- `GET /api/v1/categories` - List categories (tree)
- `GET /api/v1/docs` - API documentation

### SEO Routes
- `/sitemap.xml` - Auto-generated sitemap
- `/robots.txt` - Robots configuration
- `/health` - Health check endpoint

## Testing Requirements

**IMPORTANT**: Before reporting any fix as complete, you MUST:

1. **Run the server** and verify changes with actual HTTP requests using curl
2. **Test affected endpoints** - don't just build and run unit tests
3. **Verify expected responses** - check HTTP status codes and response content

Example testing workflow:
```bash
# Start server in background
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go run ./cmd/ocms &

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

## Testing Specific Components

```bash
# Test cache layer
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test -v ./internal/cache/...

# Test theme system
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test -v ./internal/theme/...

# Test module system
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test -v ./internal/module/...

# Test API middleware
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test -v ./internal/middleware/...

# Test API handlers
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test -v ./internal/handler/api/...
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
- `docs/hcaptcha.md` - hCaptcha integration for bot protection on login
- `docs/i18n.md` - Internationalization, translation file format, theme translations, and `TTheme` usage
