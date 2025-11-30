# oCMS

A lightweight content management system built with Go, featuring a modern admin interface, session-based authentication, SQLite storage, and extensible architecture with themes and modules.

## Features

### Content Management
- **Page Management**: Create, edit, publish, and version pages with a rich content editor
- **Scheduled Publishing**: Schedule pages to publish at a future date/time
- **Media Library**: Upload and manage images, documents, and videos with automatic image processing
  - Automatic thumbnail and variant generation
  - Folder organization
  - Featured image support for pages
- **Menu Builder**: Create navigation menus with drag-and-drop ordering
  - Hierarchical menu structures
  - Link to pages or external URLs
  - Multiple menu locations
- **Full-Text Search**: Built-in SQLite FTS5 search for fast content discovery

### Taxonomy
- **Categories**: Organize content with hierarchical categories
- **Tags**: Add flat taxonomy tags to pages

### Forms
- **Form Builder**: Create contact forms, surveys, and data collection forms
  - Multiple field types (text, email, textarea, select, checkbox, radio)
  - Form submissions management
  - Read/unread status tracking
  - Email notifications

### Theme System
- **Multiple Themes**: Switch between different frontend themes
- **Theme Settings**: Configurable theme options (colors, layout, etc.)
- **Template Override**: Themes can customize page templates
- **Static Assets**: Theme-specific CSS, JavaScript, and images

### Module System
- **Extensible Architecture**: Add custom functionality via modules
- **Module Lifecycle**: Init, routes, admin routes, and shutdown hooks
- **Module Migrations**: Modules can have their own database migrations
- **Template Functions**: Modules can add custom template functions

### REST API
- **Full CRUD API**: Complete REST API for pages, media, tags, and categories
- **API Key Authentication**: Secure API access with bearer token authentication
- **Permission-Based Access**: Fine-grained permissions (read/write per resource)
- **Rate Limiting**: Per-key and global rate limiting
- **API Documentation**: Built-in API documentation page

### SEO
- **Meta Tags**: Custom title, description, and keywords per page
- **Open Graph**: Full Open Graph and Twitter Card support
- **Sitemap**: Auto-generated sitemap.xml
- **Robots.txt**: Configurable robots.txt generation
- **Canonical URLs**: Set canonical URLs to avoid duplicate content
- **NoIndex/NoFollow**: Control search engine indexing per page

### Administration
- **User Management**: Role-based access control (admin/editor)
- **Authentication**: Secure session-based authentication with argon2id password hashing
- **Event Logging**: Comprehensive audit trail for all actions
- **Admin Dashboard**: Modern responsive UI with HTMX and Alpine.js
  - Statistics overview
  - Recent submissions widget
  - Quick actions
- **Cache Management**: View cache stats and clear cache
- **API Key Management**: Create and manage API keys
- **SQLite Database**: Zero-configuration embedded database with migrations

### Performance
- **In-Memory Caching**: Configurable cache layer for site config, menus, and more
- **Response Compression**: Gzip compression for HTML and JSON responses
- **Graceful Shutdown**: Clean shutdown with request draining
- **Health Check**: `/health` endpoint for monitoring

## Prerequisites

- Go 1.21 or later
- [sqlc](https://sqlc.dev/) for SQL code generation
- [templ](https://templ.guide/) for type-safe HTML templates
- [goose](https://github.com/pressly/goose) for database migrations
- [Dart Sass](https://sass-lang.com/dart-sass) for SCSS compilation
- [libvips](https://www.libvips.org/) for image processing (required for media library)

### Installing libvips

**macOS:**
```bash
brew install vips
```

**Ubuntu/Debian:**
```bash
sudo apt-get install libvips-dev
```

**Fedora:**
```bash
sudo dnf install vips-devel
```

## Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/yourusername/ocms-go.git
   cd ocms-go
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. Install required tools:
   ```bash
   go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
   go install github.com/a-h/templ/cmd/templ@latest
   go install github.com/pressly/goose/v3/cmd/goose@latest
   ```

4. Generate code:
   ```bash
   sqlc generate
   templ generate
   ```

5. Build assets:
   ```bash
   ./scripts/build-assets.sh
   ```

## Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `OCMS_SESSION_SECRET` | Secret key for session encryption (min 32 bytes) | - | Yes |
| `OCMS_DB_PATH` | Path to SQLite database file | `./data/ocms.db` | No |
| `OCMS_SERVER_HOST` | Server host address | `localhost` | No |
| `OCMS_SERVER_PORT` | Server port number | `8080` | No |
| `OCMS_ENV` | Environment mode (`development`/`production`) | `development` | No |
| `OCMS_LOG_LEVEL` | Log level (`debug`/`info`/`warn`/`error`) | `info` | No |
| `OCMS_THEMES_DIR` | Directory containing themes | `./themes` | No |
| `OCMS_ACTIVE_THEME` | Name of the active theme | `default` | No |
| `OCMS_API_RATE_LIMIT` | API requests per minute per key | `100` | No |
| `OCMS_CACHE_TTL` | Default cache TTL in seconds | `3600` | No |

## Development

### Quick Start

```bash
# Set required environment variable
export OCMS_SESSION_SECRET="your-secret-key-at-least-32-bytes"

# Run with asset compilation
make dev

# Or run without rebuilding assets
make run
```

### Available Make Commands

| Command | Description |
|---------|-------------|
| `make dev` | Build assets and run development server |
| `make run` | Run development server (no asset build) |
| `make stop` | Stop development server on port 8080 |
| `make build` | Build production binary to `bin/ocms` |
| `make test` | Run all tests |
| `make clean` | Remove build artifacts and database |
| `make migrate-up` | Apply pending migrations |
| `make migrate-down` | Rollback last migration |
| `make migrate-status` | Show migration status |
| `make migrate-create` | Create new migration file |
| `make assets` | Compile SCSS to CSS |

### Default Admin Credentials

On first run, the application seeds a default admin user:
- **Email**: admin@example.com
- **Password**: changeme

Change these credentials immediately after first login.

## Project Structure

```
ocms-go/
├── cmd/ocms/             # Application entry point
├── internal/
│   ├── auth/             # Password hashing utilities
│   ├── cache/            # In-memory caching layer
│   ├── config/           # Configuration loading
│   ├── handler/          # HTTP handlers
│   │   └── api/          # REST API handlers
│   ├── imaging/          # Image processing (thumbnails, variants)
│   ├── middleware/       # HTTP middleware (auth, API, rate limiting)
│   ├── model/            # Domain models
│   ├── module/           # Module system (registry, hooks)
│   ├── render/           # Template rendering
│   ├── scheduler/        # Cron-based task scheduler
│   ├── seo/              # SEO utilities (sitemap, robots.txt, meta)
│   ├── service/          # Business logic (media, menus, forms)
│   ├── session/          # Session management
│   ├── store/            # Database layer (sqlc generated)
│   │   ├── migrations/   # Goose SQL migrations
│   │   └── queries/      # sqlc query definitions
│   ├── theme/            # Theme loading and management
│   └── util/             # Utility functions (slug generation)
├── modules/              # Custom modules directory
│   └── example/          # Example module implementation
├── themes/               # Theme directory
│   ├── default/          # Default theme
│   │   ├── theme.json    # Theme configuration
│   │   ├── templates/    # Theme templates
│   │   └── static/       # Theme static assets
│   └── developer/        # Developer theme (dark mode)
├── web/
│   ├── static/           # Static assets (CSS, JS)
│   │   └── scss/         # SCSS source files
│   └── templates/        # HTML templates
│       ├── admin/        # Admin panel templates
│       ├── api/          # API documentation templates
│       ├── auth/         # Login/logout templates
│       ├── errors/       # Error pages (404, 403, 500)
│       ├── layouts/      # Base layouts
│       └── partials/     # Reusable components
├── uploads/              # Media uploads directory
├── scripts/              # Build scripts
├── Makefile              # Development commands
└── sqlc.yaml             # sqlc configuration
```

## REST API

The CMS provides a RESTful API for programmatic access to content.

### Authentication

API requests require a Bearer token in the Authorization header:

```bash
curl -H "Authorization: Bearer your-api-key" http://localhost:8080/api/v1/pages
```

Create API keys in the admin panel under **Settings > API Keys**.

### Endpoints

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| GET | `/api/v1/pages` | List published pages | Optional |
| GET | `/api/v1/pages/{id}` | Get page by ID | Optional |
| GET | `/api/v1/pages/slug/{slug}` | Get page by slug | Optional |
| POST | `/api/v1/pages` | Create page | Required |
| PUT | `/api/v1/pages/{id}` | Update page | Required |
| DELETE | `/api/v1/pages/{id}` | Delete page | Required |
| GET | `/api/v1/media` | List media | Optional |
| POST | `/api/v1/media` | Upload media | Required |
| GET | `/api/v1/tags` | List tags | Public |
| GET | `/api/v1/categories` | List categories (tree) | Public |
| GET | `/api/v1/docs` | API documentation | Public |
| GET | `/health` | Health check | Public |

### Response Format

```json
{
  "data": { ... },
  "meta": {
    "total": 100,
    "page": 1,
    "per_page": 20
  }
}
```

### Error Format

```json
{
  "error": {
    "code": "validation_error",
    "message": "Validation failed",
    "details": { "title": "Title is required" }
  }
}
```

## Theme Development

Create a new theme by adding a directory in `themes/`:

```
themes/my-theme/
├── theme.json          # Theme configuration
├── templates/
│   ├── layouts/
│   │   └── base.html   # Base layout
│   ├── pages/
│   │   ├── home.html   # Homepage template
│   │   ├── page.html   # Single page template
│   │   └── 404.html    # Not found page
│   └── partials/
│       ├── header.html
│       └── footer.html
└── static/
    ├── css/
    └── js/
```

### theme.json

```json
{
  "name": "My Theme",
  "version": "1.0.0",
  "author": "Your Name",
  "description": "A custom theme",
  "settings": [
    {
      "key": "primary_color",
      "label": "Primary Color",
      "type": "color",
      "default": "#3b82f6"
    }
  ]
}
```

## Module Development

Create custom modules to extend functionality:

```go
package mymodule

import (
    "ocms-go/internal/module"
    "github.com/go-chi/chi/v5"
)

type MyModule struct {
    module.BaseModule
}

func New() *MyModule {
    return &MyModule{
        BaseModule: module.NewBaseModule("mymodule", "1.0.0", "My custom module"),
    }
}

func (m *MyModule) RegisterRoutes(r chi.Router) {
    r.Get("/my-endpoint", m.handleEndpoint)
}

func (m *MyModule) Migrations() []module.Migration {
    return []module.Migration{
        {
            Version:     1,
            Description: "Create my_table",
            Up: func(db *sql.DB) error {
                _, err := db.Exec("CREATE TABLE my_table (...)")
                return err
            },
        },
    }
}
```

Register the module in `main.go`:

```go
moduleRegistry.Register(mymodule.New())
```

## Testing

Run all tests:
```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test ./...
```

Run tests with verbose output:
```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test -v ./...
```

Run tests for a specific package:
```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!! go test -v ./internal/store/...
```

Check for vulnerabilities:
```bash
govulncheck ./...
```

## Technology Stack

- **Backend**: Go 1.21+
- **Database**: SQLite with [goose](https://github.com/pressly/goose) migrations
- **SQL**: Type-safe queries with [sqlc](https://sqlc.dev/)
- **Templates**: [templ](https://templ.guide/) for type-safe HTML
- **Frontend**: HTMX + Alpine.js
- **Styling**: Custom SCSS framework
- **Authentication**: Secure sessions with argon2id password hashing

## License

MIT License
