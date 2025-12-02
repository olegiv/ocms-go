# Opossum CMS (oCMS) — Phase 1 Implementation Prompt

## Project Overview

Create a generic CMS called **Opossum CMS (oCMS)** in Go. This is a hobby project focused on clean architecture, idiomatic Go, and enjoyable development experience.

---

## Critical Development Rules

### 1. Iterative Development
- **DO NOT implement everything at once**
- Work through iterations sequentially
- Each iteration must be testable and committable independently
- Wait for confirmation before proceeding to next iteration

### 2. Dependency Management
- **Always use non-vulnerable module versions**
- Run `go mod tidy` after adding dependencies
- Check for known vulnerabilities with `govulncheck` before committing
- Pin specific versions in go.mod when necessary

### 3. Self-Testing Requirement
- **You must test everything you implement before asking me to test**
- Run the application and verify functionality works
- Check for compilation errors
- Test HTTP endpoints manually (curl or browser)
- Run any unit tests you create
- Only after confirming it works, present it for my review

### 4. Installation Verification
- After adding any dependency, verify:
  - `go build ./...` succeeds
  - `go run ./cmd/ocms` starts without errors
  - Basic functionality works as expected

---

## Technology Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.25+ (latest stable) |
| Router | chi |
| Templates | html/template (stdlib) |
| Database | SQLite (development), PostgreSQL (future production) |
| Query Layer | sqlc (type-safe SQL) |
| Migrations | goose |
| Sessions | scs (alexedwards/scs) with SQLite store |
| Validation | go-playground/validator |
| Password Hashing | argon2 (x/crypto/argon2) |
| Config | caarlos0/env |
| CSS | SCSS (compiled with dart-sass, no Node.js) |
| JS | Vanilla JS + HTMX + Alpine.js (no jQuery, no Node.js runtime) |
| WYSIWYG | TipTap (loaded via CDN) |
| Icons | Lucide (SVG, copy directly into templates) |

---

## Development Environment

- macOS (MacPorts, no Homebrew)
- Debian 12 (development)
- Ubuntu 24 (production target)
- **SQLite for all Phase 1 development** — PostgreSQL migration in Phase 2+
- No Node.js packages — use standalone binaries only (dart-sass)

---

## Project Structure

```
opossum/
├── cmd/
│   └── ocms/
│       └── main.go                 # Entry point, wire dependencies
├── internal/
│   ├── handler/                    # HTTP handlers grouped by domain
│   │   ├── admin.go                # Admin dashboard handlers
│   │   ├── auth.go                 # Login, logout handlers
│   │   ├── pages.go                # Page CRUD handlers
│   │   ├── users.go                # User management handlers
│   │   ├── config.go               # Site configuration handlers
│   │   └── events.go               # Event log viewer
│   ├── model/                      # Domain entities (plain structs)
│   │   ├── page.go
│   │   ├── user.go
│   │   ├── config.go
│   │   └── event.go
│   ├── store/                      # Database layer
│   │   ├── queries/                # SQL files for sqlc
│   │   │   ├── pages.sql
│   │   │   ├── users.sql
│   │   │   ├── config.sql
│   │   │   └── events.sql
│   │   ├── db.go                   # Database connection
│   │   └── generated/              # sqlc generated code
│   ├── service/                    # Business logic layer
│   │   ├── auth.go
│   │   ├── pages.go
│   │   └── users.go
│   ├── middleware/
│   │   ├── auth.go                 # Authentication middleware
│   │   ├── csrf.go                 # CSRF protection
│   │   ├── logging.go              # Request logging
│   │   └── recovery.go             # Panic recovery
│   ├── config/
│   │   └── config.go               # App configuration struct (caarlos0/env)
│   └── render/
│       └── render.go               # Template rendering helpers
├── migrations/
│   ├── 00001_create_users.sql
│   ├── 00002_create_pages.sql
│   ├── 00003_create_page_versions.sql
│   ├── 00004_create_config.sql
│   ├── 00005_create_events.sql
│   └── 00006_create_sessions.sql
├── web/
│   ├── templates/
│   │   ├── layouts/
│   │   │   ├── admin.html          # Admin panel layout
│   │   │   └── base.html           # Base HTML structure
│   │   ├── admin/
│   │   │   ├── dashboard.html
│   │   │   ├── pages_list.html
│   │   │   ├── pages_form.html
│   │   │   ├── pages_versions.html
│   │   │   ├── users_list.html
│   │   │   ├── users_form.html
│   │   │   ├── config.html
│   │   │   └── events.html
│   │   ├── auth/
│   │   │   └── login.html
│   │   └── partials/
│   │       ├── alert.html
│   │       ├── pagination.html
│   │       └── nav.html
│   └── static/
│       ├── scss/
│       │   ├── main.scss
│       │   ├── _variables.scss
│       │   ├── _reset.scss
│       │   ├── _layout.scss
│       │   ├── _components.scss
│       │   └── _admin.scss
│       ├── js/
│       │   └── admin.js            # Vanilla JS for admin interactions
│       └── dist/                   # Compiled CSS/JS (gitignored, built)
├── scripts/
│   └── build-assets.sh             # SCSS compilation script
├── data/                           # SQLite database files (gitignored)
│   └── .gitkeep
├── sqlc.yaml
├── go.mod
├── go.sum
├── Makefile
├── .env.example
├── .gitignore
└── README.md
```

---

## Phase 1 Entities

### User
```go
type User struct {
    ID           int64
    Email        string     // unique, validated
    PasswordHash string
    Role         string     // admin, editor, viewer
    Name         string
    CreatedAt    time.Time
    UpdatedAt    time.Time
    LastLoginAt  *time.Time
}
```

### Page
```go
type Page struct {
    ID          int64
    Title       string
    Slug        string      // unique, auto-generated from title, editable
    Body        string      // HTML from TipTap
    Status      string      // draft, published
    AuthorID    int64
    CreatedAt   time.Time
    UpdatedAt   time.Time
    PublishedAt *time.Time
}
```

### PageVersion
```go
type PageVersion struct {
    ID        int64
    PageID    int64
    Title     string
    Body      string
    ChangedBy int64       // user ID
    CreatedAt time.Time
}
```

### Config
```go
type Config struct {
    Key       string      // primary key
    Value     string
    Type      string      // string, int, bool, json
    UpdatedAt time.Time
    UpdatedBy *int64
}
```

### Event
```go
type Event struct {
    ID        int64
    Level     string      // info, warning, error
    Category  string      // auth, page, user, system
    Message   string
    UserID    *int64      // nullable, for anonymous events
    Metadata  string      // JSON for extra context
    CreatedAt time.Time
}
```

---

## Iteration Plan

### Iteration 1: Project Foundation
**Goal:** Skeleton project that compiles and runs

**Tasks:**
1. Initialize Go module: `go mod init github.com/user/opossum`
2. Create directory structure (all folders)
3. Create `.gitignore`:
   ```
   /data/*.db
   /web/static/dist/
   .env
   /bin/
   *.log
   ```
4. Create `.env.example`:
   ```
   OCMS_DB_PATH=./data/ocms.db
   OCMS_SESSION_SECRET=change-me-to-32-byte-secret-key
   OCMS_SERVER_HOST=localhost
   OCMS_SERVER_PORT=8080
   OCMS_ENV=development
   OCMS_LOG_LEVEL=debug
   ```
5. Create `internal/config/config.go` using caarlos0/env:
   ```go
   type Config struct {
       DBPath        string `env:"OCMS_DB_PATH" envDefault:"./data/ocms.db"`
       SessionSecret string `env:"OCMS_SESSION_SECRET,required"`
       ServerHost    string `env:"OCMS_SERVER_HOST" envDefault:"localhost"`
       ServerPort    int    `env:"OCMS_SERVER_PORT" envDefault:"8080"`
       Env           string `env:"OCMS_ENV" envDefault:"development"`
       LogLevel      string `env:"OCMS_LOG_LEVEL" envDefault:"info"`
   }
   ```
6. Create minimal `cmd/ocms/main.go` that:
   - Loads config
   - Sets up slog
   - Creates chi router
   - Serves a simple "oCMS is running" response at `/`
   - Starts HTTP server
7. Create basic `Makefile`:
   ```makefile
   .PHONY: run build test clean

   run:
   	go run ./cmd/ocms

   build:
   	go build -o bin/ocms ./cmd/ocms

   test:
   	go test -v ./...

   clean:
   	rm -rf bin/ data/*.db
   ```
8. Install dependencies and verify build works

**Verification:**
- `go build ./...` succeeds
- `go run ./cmd/ocms` starts server
- `curl http://localhost:8080/` returns response

**Commit message:** `feat: project foundation and basic server`

---

### Iteration 2: Database & Migrations
**Goal:** SQLite connection with goose migrations

**Tasks:**
1. Install goose: verify it's available
2. Create `internal/store/db.go`:
   - Open SQLite connection with `modernc.org/sqlite` (pure Go, no CGO)
   - Implement `NewDB(path string) (*sql.DB, error)`
   - Add connection configuration (busy timeout, WAL mode)
3. Create migrations (use SQLite-compatible SQL):
   - `00001_create_users.sql`
   - `00002_create_sessions.sql` (for scs session store)
4. Update `main.go` to:
   - Initialize database
   - Run migrations on startup (embed migrations or call goose)
5. Add Makefile targets:
   ```makefile
   migrate-up:
   	goose -dir migrations sqlite3 ./data/ocms.db up

   migrate-down:
   	goose -dir migrations sqlite3 ./data/ocms.db down

   migrate-status:
   	goose -dir migrations sqlite3 ./data/ocms.db status
   ```

**Verification:**
- Migrations run without error
- `data/ocms.db` file is created
- Tables exist (verify with `sqlite3 data/ocms.db ".tables"`)

**Commit message:** `feat: SQLite database with goose migrations`

---

### Iteration 3: Session Management
**Goal:** Working session middleware with SQLite store

**Tasks:**
1. Install `alexedwards/scs/v2` and `alexedwards/scs/sqlite3store`
2. Create session manager in `main.go` or dedicated file
3. Configure session:
   - Lifetime: 24 hours
   - Cookie: HttpOnly, SameSite=Lax, Secure=false (dev)
   - SQLite store
4. Add session middleware to chi router
5. Create a test route that sets/gets session value

**Verification:**
- Server starts without error
- Test route correctly stores and retrieves session data
- Session record appears in database

**Commit message:** `feat: session management with SQLite store`

---

### Iteration 4: Template Rendering
**Goal:** html/template setup with layouts and partials

**Tasks:**
1. Create `internal/render/render.go`:
   - Template cache (parse once, reuse)
   - Helper function to render templates with layout
   - Template functions (formatDate, truncate, etc.)
2. Create base templates:
   - `web/templates/layouts/base.html` — HTML skeleton
   - `web/templates/layouts/admin.html` — admin layout extending base
   - `web/templates/partials/nav.html` — navigation partial
   - `web/templates/partials/alert.html` — flash message partial
3. Create `web/templates/admin/dashboard.html` — placeholder dashboard
4. Add route `/admin` that renders dashboard template
5. Setup static file serving at `/static/`

**Verification:**
- `/admin` renders HTML page with layout
- CSS/JS files served from `/static/`
- No template parsing errors

**Commit message:** `feat: html/template rendering with layouts`

---

### Iteration 5: Basic Styling
**Goal:** SCSS compilation and basic admin UI

**Tasks:**
1. Create SCSS files:
   - `_variables.scss` — colors, spacing, fonts
   - `_reset.scss` — CSS reset/normalize
   - `_layout.scss` — grid, containers
   - `_components.scss` — buttons, forms, tables, alerts
   - `_admin.scss` — admin-specific styles
   - `main.scss` — imports all partials
2. Create `scripts/build-assets.sh`:
   ```bash
   #!/bin/bash
   sass web/static/scss/main.scss web/static/dist/main.css --style=compressed
   ```
3. Create basic admin layout with:
   - Sidebar navigation
   - Main content area
   - Header with user info placeholder
   - Responsive design (mobile-friendly)
4. Add HTMX and Alpine.js via CDN in base template
5. Update Makefile:
   ```makefile
   assets:
   	./scripts/build-assets.sh

   dev: assets
   	go run ./cmd/ocms
   ```

**Verification:**
- `./scripts/build-assets.sh` compiles without errors
- CSS loads correctly in browser
- Admin layout displays properly
- HTMX and Alpine.js load (check browser console)

**Commit message:** `feat: SCSS styling and admin layout`

---

### Iteration 6: Authentication - Models & Store
**Goal:** User model and database operations

**Tasks:**
1. Create `internal/model/user.go` with User struct
2. Create sqlc configuration `sqlc.yaml`:
   ```yaml
   version: "2"
   sql:
     - engine: "sqlite"
       queries: "internal/store/queries/"
       schema: "migrations/"
       gen:
         go:
           package: "store"
           out: "internal/store"
           emit_json_tags: true
           emit_empty_slices: true
   ```
3. Create `internal/store/queries/users.sql`:
   ```sql
   -- name: CreateUser :one
   INSERT INTO users (email, password_hash, role, name, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?)
   RETURNING *;

   -- name: GetUserByEmail :one
   SELECT * FROM users WHERE email = ?;

   -- name: GetUserByID :one
   SELECT * FROM users WHERE id = ?;

   -- name: ListUsers :many
   SELECT * FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?;

   -- name: UpdateUser :one
   UPDATE users SET email = ?, role = ?, name = ?, updated_at = ?
   WHERE id = ?
   RETURNING *;

   -- name: UpdateUserLastLogin :exec
   UPDATE users SET last_login_at = ? WHERE id = ?;

   -- name: DeleteUser :exec
   DELETE FROM users WHERE id = ?;

   -- name: CountUsers :one
   SELECT COUNT(*) FROM users;
   ```
4. Run `sqlc generate`
5. Create seed function to create default admin user:
   - Email: admin@example.com
   - Password: changeme (hashed with argon2)

**Verification:**
- `sqlc generate` completes without errors
- Generated code compiles
- Seed creates admin user in database

**Commit message:** `feat: user model and database operations`

---

### Iteration 7: Authentication - Login/Logout
**Goal:** Working login and logout functionality

**Tasks:**
1. Create `internal/service/auth.go`:
   - `Authenticate(email, password string) (*User, error)`
   - Password verification with argon2
2. Create `internal/handler/auth.go`:
   - `GET /admin/login` — render login form
   - `POST /admin/login` — authenticate and create session
   - `POST /admin/logout` — destroy session
3. Create `web/templates/auth/login.html`:
   - Email and password fields
   - Error message display
   - CSRF token
4. Create `internal/middleware/auth.go`:
   - Check session for user ID
   - Load user from database
   - Store user in request context
   - Redirect to login if not authenticated
5. Apply auth middleware to `/admin/*` routes (except login)
6. Create CSRF middleware using `gorilla/csrf` or custom implementation

**Verification:**
- Login page renders at `/admin/login`
- Invalid credentials show error message
- Valid credentials redirect to dashboard
- Session cookie is set
- `/admin` requires authentication
- Logout destroys session

**Commit message:** `feat: authentication with login/logout`

---

### Iteration 8: Admin Dashboard
**Goal:** Dashboard with stats and recent activity

**Tasks:**
1. Create `internal/handler/admin.go`:
   - `GET /admin` — dashboard handler
2. Update `web/templates/admin/dashboard.html`:
   - Welcome message with user name
   - Stats cards (placeholder data for now):
     - Total pages
     - Published pages
     - Draft pages
     - Total users
   - Recent activity section (placeholder)
   - Quick action buttons
3. Add template data struct for dashboard
4. Display current user info in header (from session)

**Verification:**
- Dashboard loads after login
- User name displays correctly
- Stats cards render (even with zeros)
- Navigation works

**Commit message:** `feat: admin dashboard with stats`

---

### Iteration 9: User Management - List & Create
**Goal:** View users and create new ones

**Tasks:**
1. Create `internal/handler/users.go`:
   - `GET /admin/users` — list users
   - `GET /admin/users/new` — new user form
   - `POST /admin/users` — create user
2. Create templates:
   - `web/templates/admin/users_list.html` — table with pagination
   - `web/templates/admin/users_form.html` — create/edit form
3. Implement pagination (10 users per page)
4. Add validation:
   - Required fields
   - Valid email format
   - Role must be admin/editor/viewer
5. Add flash messages for success/error

**Verification:**
- User list displays all users
- Pagination works
- Can create new user
- Validation errors display
- Flash messages appear

**Commit message:** `feat: user management - list and create`

---

### Iteration 10: User Management - Edit & Delete
**Goal:** Complete user CRUD

**Tasks:**
1. Add handlers:
   - `GET /admin/users/{id}` — edit form
   - `PUT /admin/users/{id}` — update user
   - `DELETE /admin/users/{id}` — delete user
2. Implement business rules:
   - Cannot delete yourself
   - Cannot demote yourself from admin if last admin
   - Password change is optional on edit
3. Add confirmation modal for delete (Alpine.js)
4. Use HTMX for delete without page reload

**Verification:**
- Can edit existing user
- Can change user role
- Can change password (optional)
- Cannot delete self
- Delete confirmation works
- Delete removes user from list

**Commit message:** `feat: user management - edit and delete`

---

### Iteration 11: Pages - Model & Store
**Goal:** Page and PageVersion models with database operations

**Tasks:**
1. Create migrations:
   - `00003_create_pages.sql`
   - `00004_create_page_versions.sql`
2. Create `internal/model/page.go`
3. Create `internal/store/queries/pages.sql`:
   - CRUD operations
   - List with filtering (status)
   - Version operations
4. Run migrations and sqlc generate
5. Create slug generation utility (slugify title)

**Verification:**
- Migrations run successfully
- sqlc generates without errors
- Slug utility works correctly

**Commit message:** `feat: page model and database operations`

---

### Iteration 12: Pages - List & Create
**Goal:** Page listing and creation with TipTap editor

**Tasks:**
1. Create `internal/handler/pages.go`:
   - `GET /admin/pages` — list pages
   - `GET /admin/pages/new` — new page form
   - `POST /admin/pages` — create page
2. Create templates:
   - `web/templates/admin/pages_list.html` — table with status filter
   - `web/templates/admin/pages_form.html` — form with TipTap
3. Integrate TipTap WYSIWYG:
   - Load from CDN
   - Basic toolbar (bold, italic, headings, links, lists)
   - Store HTML in hidden field
4. Auto-generate slug from title (JavaScript)
5. Create page version on save

**Verification:**
- Page list displays correctly
- TipTap editor loads and works
- Can create page with rich content
- Slug auto-generates
- Version is created

**Commit message:** `feat: page management - list and create with TipTap`

---

### Iteration 13: Pages - Edit, Delete, Publish
**Goal:** Complete page management

**Tasks:**
1. Add handlers:
   - `GET /admin/pages/{id}` — edit form
   - `PUT /admin/pages/{id}` — update page
   - `DELETE /admin/pages/{id}` — delete page
   - `POST /admin/pages/{id}/publish` — toggle publish status
2. On update:
   - Create new version
   - Update updated_at
3. On publish:
   - Set published_at timestamp
   - Change status to published
4. Add status filter to list (all/draft/published)
5. Add bulk actions (optional, HTMX)

**Verification:**
- Can edit page
- Changes create new version
- Can publish/unpublish
- Can delete page
- Status filter works

**Commit message:** `feat: page management - edit, delete, publish`

---

### Iteration 14: Page Versioning
**Goal:** Version history and restore

**Tasks:**
1. Add handler:
   - `GET /admin/pages/{id}/versions` — version history
   - `POST /admin/pages/{id}/versions/{versionId}/restore` — restore version
2. Create template:
   - `web/templates/admin/pages_versions.html`
   - List of versions with timestamps
   - Show who made the change
   - Restore button
3. Restore creates new version with old content
4. Side-by-side diff view (optional, can use simple approach)

**Verification:**
- Version history shows all changes
- Can see version content
- Restore works correctly
- New version is created on restore

**Commit message:** `feat: page version history and restore`

---

### Iteration 15: Configuration System
**Goal:** Site configuration management

**Tasks:**
1. Create migration `00005_create_config.sql`
2. Create `internal/model/config.go`
3. Create `internal/store/queries/config.sql`
4. Create `internal/handler/config.go`:
   - `GET /admin/config` — list config
   - `PUT /admin/config` — update config values
5. Create template `web/templates/admin/config.html`:
   - Form with all config values
   - Type-appropriate inputs (text, number, checkbox)
6. Seed default config values:
   - site_name (string, "Opossum CMS")
   - site_description (string, "")
   - admin_email (string, "admin@example.com")
   - posts_per_page (int, 10)
7. Make site_name available in all templates

**Verification:**
- Config page loads with current values
- Can update values
- Type validation works
- Site name appears in admin header

**Commit message:** `feat: site configuration management`

---

### Iteration 16: Event Logging
**Goal:** System-wide event logging and viewer

**Tasks:**
1. Create migration `00006_create_events.sql`
2. Create `internal/model/event.go`
3. Create `internal/store/queries/events.sql`
4. Create `internal/service/events.go`:
   - `LogEvent(level, category, message, userID, metadata)`
   - Convenience methods: `LogInfo`, `LogWarning`, `LogError`
5. Create `internal/handler/events.go`:
   - `GET /admin/events` — list events with filters
6. Create template `web/templates/admin/events.html`:
   - Table with pagination
   - Filters: level, category, date range
7. Add event logging throughout app:
   - Auth: login success/failure
   - Users: created/updated/deleted
   - Pages: created/updated/deleted/published
   - Config: changed
8. Add panic recovery that logs errors

**Verification:**
- Events are logged for all actions
- Event viewer shows events
- Filters work correctly
- Pagination works

**Commit message:** `feat: event logging system`

---

### Iteration 17: Polish & Error Handling
**Goal:** Professional error handling and UX improvements

**Tasks:**
1. Create error pages:
   - 404 Not Found
   - 403 Forbidden
   - 500 Internal Server Error
2. Improve flash messages:
   - Success (green)
   - Error (red)
   - Warning (yellow)
   - Info (blue)
   - Auto-dismiss with Alpine.js
3. Add loading states:
   - HTMX loading indicators
   - Button disabled states during submission
4. Add breadcrumbs to all admin pages
5. Improve form validation UX:
   - Inline error messages
   - Field highlighting
6. Add keyboard shortcuts (optional):
   - `Ctrl+S` to save forms
7. Test all edge cases:
   - Empty states
   - Long content
   - Special characters in slugs
   - Concurrent edits

**Verification:**
- Error pages display correctly
- Flash messages work and dismiss
- Loading states visible
- Forms validate properly
- No console errors

**Commit message:** `feat: error handling and UX polish`

---

### Iteration 18: Testing & Documentation
**Goal:** Tests and documentation

**Tasks:**
1. Write unit tests:
   - Auth service (password hashing, verification)
   - Slug generation
   - Config service
2. Write integration tests:
   - User CRUD
   - Page CRUD
   - Session management
3. Create `README.md`:
   - Project description
   - Setup instructions
   - Environment variables
   - Development commands
   - Project structure
4. Add inline documentation:
   - Package comments
   - Exported function comments
5. Run `govulncheck` and fix any issues

**Verification:**
- All tests pass
- `go test ./...` shows good coverage
- README is clear and complete
- No vulnerabilities

**Commit message:** `feat: tests and documentation`

---

## sqlc Configuration (SQLite)

Create `sqlc.yaml`:
```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "internal/store/queries/"
    schema: "migrations/"
    gen:
      go:
        package: "store"
        out: "internal/store"
        emit_json_tags: true
        emit_empty_slices: true
```

---

## Makefile (Complete)

```makefile
.PHONY: run build test clean assets migrate-up migrate-down migrate-status sqlc dev

# Development server
run:
	go run ./cmd/ocms

# Build production binary
build: assets sqlc
	go build -o bin/ocms ./cmd/ocms

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -rf bin/ data/*.db web/static/dist/

# Compile SCSS
assets:
	mkdir -p web/static/dist
	sass web/static/scss/main.scss web/static/dist/main.css --style=compressed

# Run migrations up
migrate-up:
	goose -dir migrations sqlite3 ./data/ocms.db up

# Run migrations down
migrate-down:
	goose -dir migrations sqlite3 ./data/ocms.db down

# Migration status
migrate-status:
	goose -dir migrations sqlite3 ./data/ocms.db status

# Generate sqlc
sqlc:
	sqlc generate

# Development with assets
dev: assets
	go run ./cmd/ocms

# Check for vulnerabilities
vulncheck:
	govulncheck ./...

# Create new migration (usage: make migration name=create_foo)
migration:
	goose -dir migrations create $(name) sql
```

---

## Configuration (caarlos0/env)

`internal/config/config.go`:
```go
package config

import (
	"fmt"
	"github.com/caarlos0/env/v11"
)

type Config struct {
	DBPath        string `env:"OCMS_DB_PATH" envDefault:"./data/ocms.db"`
	SessionSecret string `env:"OCMS_SESSION_SECRET,required"`
	ServerHost    string `env:"OCMS_SERVER_HOST" envDefault:"localhost"`
	ServerPort    int    `env:"OCMS_SERVER_PORT" envDefault:"8080"`
	Env           string `env:"OCMS_ENV" envDefault:"development"`
	LogLevel      string `env:"OCMS_LOG_LEVEL" envDefault:"info"`
}

func (c Config) IsDevelopment() bool {
	return c.Env == "development"
}

func (c Config) ServerAddr() string {
	return fmt.Sprintf("%s:%d", c.ServerHost, c.ServerPort)
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}
```

---

## Middleware Stack Order

Configure chi middleware in this order:
1. RequestID (chi middleware)
2. RealIP (chi middleware)
3. Logger (custom, structured with slog)
4. Recoverer (custom, logs to events table)
5. Session loading (scs)
6. CSRF (gorilla/csrf, for non-GET requests)
7. Auth check (on protected routes)

---

## Routes Structure

```
GET  /                            # Redirect to /admin
GET  /admin                       # Dashboard (requires auth)
GET  /admin/login                 # Login form
POST /admin/login                 # Login action
POST /admin/logout                # Logout action

GET  /admin/pages                 # List pages
GET  /admin/pages/new             # New page form
POST /admin/pages                 # Create page
GET  /admin/pages/{id}            # Edit page form
PUT  /admin/pages/{id}            # Update page
DELETE /admin/pages/{id}          # Delete page
POST /admin/pages/{id}/publish    # Toggle publish
GET  /admin/pages/{id}/versions   # Version history
POST /admin/pages/{id}/versions/{vid}/restore  # Restore version

GET  /admin/users                 # List users
GET  /admin/users/new             # New user form
POST /admin/users                 # Create user
GET  /admin/users/{id}            # Edit user form
PUT  /admin/users/{id}            # Update user
DELETE /admin/users/{id}          # Delete user

GET  /admin/config                # Config editor
PUT  /admin/config                # Update config

GET  /admin/events                # Event log

GET  /static/*                    # Static files
```

---

## Code Style Requirements

1. **Error handling**: Always handle errors, wrap with context using `fmt.Errorf("doing X: %w", err)`
2. **Logging**: Use structured logging (slog from stdlib)
3. **Comments**: Document all exported functions
4. **Naming**: Follow Go conventions (mixedCaps, not snake_case)
5. **Testing**: Write table-driven tests for business logic
6. **Validation**: Validate all user input at handler level
7. **No panics**: Use error returns, recover only at middleware level

---

## Security Requirements

1. Password hashing with argon2id (reasonable params for 2024)
2. CSRF tokens on all state-changing operations
3. Secure session cookies (HttpOnly, SameSite=Lax)
4. Input sanitization for HTML content (bluemonday)
5. SQL injection prevention (sqlc parameterized queries)
6. Rate limiting on login endpoint (simple in-memory counter)

---

## Seed Data

Create on first run or via command:
- Admin user: admin@example.com / changeme (prompt to change on first login)
- Sample page: "Welcome to Opossum CMS"
- Default config values

---

## Success Criteria

Phase 1 is complete when all iterations are done and:
- [ ] Can login as admin
- [ ] Can create/edit/delete users with role management
- [ ] Can create/edit/delete pages with TipTap WYSIWYG
- [ ] Page versions are tracked automatically
- [ ] Can view version history and restore previous versions
- [ ] Can publish/unpublish pages
- [ ] Can edit site configuration
- [ ] Events are logged and viewable with filters
- [ ] UI is responsive and usable
- [ ] No JavaScript framework dependencies (only HTMX + Alpine.js from CDN)
- [ ] No Node.js in build process (only dart-sass binary)
- [ ] All forms have CSRF protection
- [ ] Passwords are securely hashed with argon2
- [ ] Code compiles without errors
- [ ] No known vulnerabilities (`govulncheck` passes)
- [ ] Basic tests pass
- [ ] README documents setup and usage

---

## Per-Iteration Checklist

Before marking any iteration complete, verify:
- [ ] Code compiles: `go build ./...`
- [ ] Server starts: `go run ./cmd/ocms`
- [ ] Feature works as expected (manual testing)
- [ ] No console errors in browser
- [ ] No panics or unhandled errors
- [ ] Tests pass (if applicable): `go test ./...`
- [ ] Ready for commit with descriptive message

---

Begin with **Iteration 1**. Complete each iteration fully before moving to the next. Ask clarifying questions if any requirements are ambiguous.
