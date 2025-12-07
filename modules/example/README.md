# Example Module

A reference implementation demonstrating the oCMS module system. Use this as a template when creating custom modules.

## Features

- Public routes registration
- Admin routes with dashboard
- Template functions for use in themes
- Hook handlers for page events
- Database migrations
- Embedded i18n translations
- CRUD operations example

## Routes

### Public Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/example` | Public information page |

### Admin Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/example` | Admin dashboard |
| GET | `/admin/example/items` | List items (JSON) |
| POST | `/admin/example/items` | Create item |
| DELETE | `/admin/example/items/{id}` | Delete item |

## Template Functions

The module provides template functions for use in themes:

```go
{{ exampleFunc }}      // Returns "Hello from example module"
{{ exampleVersion }}   // Returns module version (e.g., "1.0.0")
```

## Hook Handlers

The module demonstrates how to register hook handlers:

```go
// page.after_save - Triggered after a page is saved
m.ctx.Hooks.Register(module.HookPageAfterSave, module.HookHandler{
    Name:     "example_page_saved",
    Module:   m.Name(),
    Priority: 10,
    Fn: func(ctx context.Context, data any) (any, error) {
        // Handle the event
        return data, nil
    },
})

// page.before_render - Triggered before rendering a page
m.ctx.Hooks.Register(module.HookPageBeforeRender, module.HookHandler{
    Name:     "example_before_render",
    Module:   m.Name(),
    Priority: 5,
    Fn: func(ctx context.Context, data any) (any, error) {
        // Modify render data if needed
        return data, nil
    },
})
```

## Database Schema

The module creates an example items table:

```sql
CREATE TABLE example_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

## Module Structure

```
modules/example/
├── module.go      # Module definition, lifecycle, hooks, migrations
├── handlers.go    # HTTP handlers and database operations
└── locales/       # Embedded i18n translations
    ├── en/messages.json
    └── ru/messages.json
```

## Module Interface Implementation

The example module implements the `module.Module` interface:

```go
type Module struct {
    module.BaseModule           // Provides Name(), Version(), Description()
    ctx *module.ModuleContext   // Access to DB, Logger, Hooks, Render
}

// Required methods
func (m *Module) Init(ctx *module.ModuleContext) error
func (m *Module) Shutdown() error
func (m *Module) RegisterRoutes(r chi.Router)
func (m *Module) RegisterAdminRoutes(r chi.Router)
func (m *Module) TemplateFuncs() template.FuncMap
func (m *Module) AdminURL() string
func (m *Module) TranslationsFS() embed.FS
func (m *Module) Migrations() []module.Migration
```

## Creating a New Module

1. Copy this module directory as a template
2. Rename the package and update `module.go`
3. Update routes in `RegisterRoutes` and `RegisterAdminRoutes`
4. Add template functions if needed
5. Create migrations for your database schema
6. Add i18n translations in `locales/`
7. Register in `cmd/ocms/main.go`:

```go
import "ocms-go/modules/mymodule"

// In main()
if err := moduleRegistry.Register(mymodule.New()); err != nil {
    return fmt.Errorf("registering mymodule: %w", err)
}
```

## Internationalization

Translations are embedded and automatically loaded. The module supports:

- English (en)
- Russian (ru)

Add new languages by creating `locales/{lang}/messages.json`.

## Module Active Status

Modules can be enabled/disabled from **Admin > Modules**:

- **Active**: Routes accessible, appears in sidebar
- **Inactive**: Routes return 404 (public) or redirect (admin)

Status is persisted in the database and survives restarts.
