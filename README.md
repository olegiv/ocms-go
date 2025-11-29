# oCMS

A lightweight content management system built with Go, featuring a modern admin interface, session-based authentication, and SQLite storage.

## Features

- **Page Management**: Create, edit, publish, and version pages with a rich content editor
- **User Management**: Role-based access control (admin/editor)
- **Authentication**: Secure session-based authentication with argon2id password hashing
- **Event Logging**: Comprehensive audit trail for all actions
- **Admin Dashboard**: Modern responsive UI with HTMX and Alpine.js
- **SQLite Database**: Zero-configuration embedded database with migrations

## Prerequisites

- Go 1.21 or later
- [sqlc](https://sqlc.dev/) for SQL code generation
- [templ](https://templ.guide/) for type-safe HTML templates
- [goose](https://github.com/pressly/goose) for database migrations
- [Dart Sass](https://sass-lang.com/dart-sass) for SCSS compilation

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
├── cmd/ocms/           # Application entry point
├── internal/
│   ├── auth/           # Password hashing utilities
│   ├── config/         # Configuration loading
│   ├── handler/        # HTTP handlers
│   ├── middleware/     # HTTP middleware
│   ├── model/          # Domain models
│   ├── render/         # Template rendering
│   ├── service/        # Business logic
│   ├── session/        # Session management
│   ├── store/          # Database layer (sqlc generated)
│   │   ├── migrations/ # Goose SQL migrations
│   │   └── queries/    # sqlc query definitions
│   └── util/           # Utility functions
├── web/
│   ├── static/         # Static assets (CSS, JS)
│   │   └── scss/       # SCSS source files
│   └── templates/      # Templ templates
│       ├── admin/      # Admin panel templates
│       ├── auth/       # Login/logout templates
│       ├── errors/     # Error pages (404, 403, 500)
│       ├── layouts/    # Base layouts
│       └── partials/   # Reusable components
├── scripts/            # Build scripts
├── Makefile            # Development commands
└── sqlc.yaml           # sqlc configuration
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
