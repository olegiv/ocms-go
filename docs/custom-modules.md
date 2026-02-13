# Custom Modules

Custom modules extend oCMS without modifying core code. They live in `custom/modules/` and self-register using Go's `init()` pattern — the same approach used by `database/sql` drivers.

## Quick Start

Create a new custom module in three steps:

### 1. Create the module package

```
custom/modules/mymodule/
├── module.go      # Module definition, lifecycle, hooks, migrations
├── handlers.go    # HTTP handlers and database operations
├── register.go    # init() self-registration
├── templates/     # Embedded HTML templates
│   └── admin.html
└── locales/       # Embedded i18n translations
    ├── en/messages.json
    └── ru/messages.json
```

### 2. Add self-registration

Create `register.go`:

```go
package mymodule

import "github.com/olegiv/ocms-go/internal/module"

func init() {
    module.RegisterCustomModule(New())
}
```

### 3. Enable the module

Add a blank import to `custom/modules/imports.go`:

```go
import (
    _ "github.com/olegiv/ocms-go/custom/modules/bookmarks"
    _ "github.com/olegiv/ocms-go/custom/modules/mymodule"  // add this line
)
```

Build and run — the module appears at **Admin > Modules**.

## How It Works

The auto-registration flow:

1. Each custom module's `init()` calls `module.RegisterCustomModule(New())`
2. `custom/modules/imports.go` blank-imports each custom module package, triggering `init()`
3. `cmd/ocms/main.go` blank-imports `custom/modules`, which loads all custom modules
4. At startup, `module.CustomModules()` returns all registered modules
5. The main registry initializes them alongside built-in modules

This means adding a new custom module only requires:
- Creating files in `custom/modules/mymodule/`
- Adding one import line to `custom/modules/imports.go`

No core files need to be modified.

## Module Interface

Every module implements `module.Module`:

```go
type Module interface {
    Name() string                          // Unique identifier (e.g., "bookmarks")
    Version() string                       // Semantic version (e.g., "1.0.0")
    Description() string                   // Human-readable description
    Dependencies() []string                // Other module names this depends on

    Init(ctx *Context) error               // Initialize with app context
    Shutdown() error                       // Cleanup on shutdown

    RegisterRoutes(r chi.Router)           // Public routes (e.g., /bookmarks)
    RegisterAdminRoutes(r chi.Router)      // Admin routes (e.g., /admin/bookmarks)

    TemplateFuncs() template.FuncMap       // Functions available in all templates
    Migrations() []Migration               // Database schema migrations

    AdminURL() string                      // Admin dashboard path
    SidebarLabel() string                  // Sidebar display name
    TranslationsFS() embed.FS             // Embedded i18n translations
}
```

Embed `module.BaseModule` to get default no-op implementations, then override only the methods you need:

```go
type Module struct {
    module.BaseModule
    ctx *module.Context
}

func New() *Module {
    return &Module{
        BaseModule: module.NewBaseModule("mymodule", "1.0.0", "My custom module"),
    }
}
```

## Module Context

The `module.Context` provides access to application services:

| Field    | Type                   | Description                          |
|----------|------------------------|--------------------------------------|
| `DB`     | `*sql.DB`              | Database connection                  |
| `Store`  | `*store.Queries`       | SQLC-generated query methods         |
| `Logger` | `*slog.Logger`         | Structured logger                    |
| `Config` | `*config.Config`       | Application configuration            |
| `Render` | `*render.Renderer`     | Template renderer (for built-in modules) |
| `Events` | `*service.EventService`| Event logging service                |
| `Hooks`  | `*HookRegistry`        | Hook registration and execution      |

Custom modules that render their own embedded templates typically only need `DB`, `Logger`, and `Hooks`.

## Routes

### Public Routes

Registered via `RegisterRoutes(r chi.Router)`. These are accessible without authentication:

```go
func (m *Module) RegisterRoutes(r chi.Router) {
    r.Get("/mymodule", m.handlePublicList)
}
```

### Admin Routes

Registered via `RegisterAdminRoutes(r chi.Router)`. These require authentication and are protected by the admin middleware chain:

```go
func (m *Module) RegisterAdminRoutes(r chi.Router) {
    r.Get("/mymodule", m.handleAdminList)
    r.Post("/mymodule", m.handleCreate)
    r.Delete("/mymodule/{id}", m.handleDelete)
}
```

Admin routes are prefixed with `/admin/` automatically by the registry.

### Route Middleware

- Public routes get module active-status checking (returns 404 if module is inactive)
- Admin routes get authentication + active-status checking (redirects to modules list if inactive)

## Database Migrations

Modules define migrations with version numbers, descriptions, and up/down functions:

```go
func (m *Module) Migrations() []module.Migration {
    return []module.Migration{
        {
            Version:     1,
            Description: "Create my_items table",
            Up: func(db *sql.DB) error {
                _, err := db.Exec(`
                    CREATE TABLE IF NOT EXISTS my_items (
                        id INTEGER PRIMARY KEY AUTOINCREMENT,
                        name TEXT NOT NULL,
                        created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
                    )
                `)
                return err
            },
            Down: func(db *sql.DB) error {
                _, err := db.Exec(`DROP TABLE IF EXISTS my_items`)
                return err
            },
        },
    }
}
```

Migrations are tracked in the `module_migrations` table (goose migration `20260213100001`) and only run once. Version numbers must start at 1. Migration tracking uses SQLC-generated type-safe queries. Custom modules should always use parameterized queries (`?` placeholders) for database operations — never use `fmt.Sprintf` to build SQL statements.

## Hooks

Register hooks to respond to application events:

```go
func (m *Module) registerHooks() {
    m.ctx.Hooks.Register(module.HookPageAfterSave, module.HookHandler{
        Name:     "mymodule_page_saved",
        Module:   m.Name(),
        Priority: 20,  // lower = runs first
        Fn: func(ctx context.Context, data any) (any, error) {
            m.ctx.Logger.Info("page was saved")
            return data, nil
        },
    })
}
```

Available hooks:
- `module.HookPageAfterSave` — triggered after a page is saved
- `module.HookPageBeforeRender` — triggered before a page is rendered

Hook handlers from inactive modules are automatically skipped.

## Template Functions

Provide functions accessible in all templates across themes:

```go
func (m *Module) TemplateFuncs() template.FuncMap {
    return template.FuncMap{
        "myItemCount": func() int {
            count, _ := m.countItems()
            return count
        },
    }
}
```

Usage in theme templates:

```html
<p>Total items: {{ myItemCount }}</p>
```

## Embedded Admin Templates

Custom modules render their own admin pages using Go's `html/template` and `//go:embed`:

```go
//go:embed templates/admin.html
var adminTemplateHTML string

func (m *Module) Init(ctx *module.Context) error {
    m.ctx = ctx
    tmpl, err := template.New("admin").Parse(adminTemplateHTML)
    if err != nil {
        return fmt.Errorf("parsing admin template: %w", err)
    }
    m.adminTmpl = tmpl
    return nil
}
```

This keeps the module fully self-contained — no files outside `custom/modules/mymodule/`.

## Internationalization

Place translations in `locales/{lang}/messages.json`:

```json
{
    "$schema": "../../../../../.schema/i18n-schema.json",
    "language": "en",
    "messages": [
        {"id": "nav.mymodule", "message": "My Module", "translation": "My Module"},
        {"id": "mymodule.title", "message": "My Module", "translation": "My Module"}
    ]
}
```

Embed and expose via `TranslationsFS()`:

```go
//go:embed locales
var localesFS embed.FS

func (m *Module) TranslationsFS() embed.FS {
    return localesFS
}
```

The registry automatically loads these translations and merges them with the core translation set.

## Testing

Use `testutil` and `moduleutil` helpers:

```go
func testModule(t *testing.T, db *sql.DB) *Module {
    t.Helper()
    m := New()
    moduleutil.RunMigrations(t, db, m.Migrations())
    ctx, _ := moduleutil.TestModuleContext(t, db)
    if err := m.Init(ctx); err != nil {
        t.Fatalf("Init: %v", err)
    }
    return m
}

func TestMyModule(t *testing.T) {
    db, cleanup := testutil.TestDB(t)
    defer cleanup()

    m := testModule(t, db)
    // ... test module operations
}
```

Run module tests:

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./custom/modules/bookmarks/...
```

## Module Active Status

Modules can be toggled from **Admin > Modules**:

- **Active**: Routes accessible, appears in sidebar, hooks execute
- **Inactive**: Public routes return 404, admin routes redirect, hooks are skipped

Status persists in the database across restarts.

## Environment Restrictions

Implement `EnvironmentChecker` to restrict module activation to specific environments:

```go
func (m *Module) AllowedEnvs() []string {
    return []string{"development"}
}
```

When first registered, the module will start as inactive if the current environment is not in the allowed list.

## Reference Implementation

See the bookmarks module at `custom/modules/bookmarks/` for a complete working example with:

- Database CRUD operations
- Public JSON API
- Admin dashboard with embedded template
- Template functions (`bookmarkCount`, `bookmarkFavorites`)
- Hook handlers
- i18n translations (English and Russian)
- Comprehensive test suite

See also the built-in example module at `modules/example/` for a module that uses the core renderer.
