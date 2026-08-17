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

Add your own registration file — one per module, never a shared list:

```go
// custom/modules/imports_mymodule.go
package modules

import _ "github.com/olegiv/ocms-go/custom/modules/mymodule"
```

Build and run — the module appears at **Admin > Modules**.

Why a per-module file rather than one shared `imports.go`? Because
`custom/modules/*` is gitignored apart from what ships with oCMS. A tracked
shared file listing your module would commit a reference to a package that is
absent from a fresh clone, and the build would fail with `no required module
provides package`. Your own file is ignored, so this repository still builds
standalone — and deployments sharing one checkout never edit the same line.

`custom/modules/doc.go` declares the package and must stay: a Go package with no
source files does not exist, and `cmd/ocms` blank-imports this one.

## How It Works

The auto-registration flow:

1. Each custom module's `init()` calls `module.RegisterCustomModule(New())`
2. Each `custom/modules/imports_*.go` blank-imports one custom module package, triggering `init()`
3. `cmd/ocms/main.go` blank-imports `custom/modules`, which loads all custom modules
4. At startup, `module.CustomModules()` returns all registered modules
5. The main registry initializes them alongside built-in modules

This means adding a new custom module only requires:
- Creating files in `custom/modules/mymodule/`
- Adding `custom/modules/imports_mymodule.go`

Both live under `custom/`. No core files need to be modified — with exactly one
exception, which is worth knowing before you design your module.

### The one thing a custom module cannot do

**Do not implement `TemplateFuncs()`.**

`Registry.AllTemplateFuncs()` returns functions from *active* modules only, and
`html/template` resolves function names at *parse* time. Themes are parsed once
at boot (`cmd/ocms/main.go`), so a theme calling your module function fails to
parse on the next restart after the module is deactivated — the toggle itself
looks harmless, and the breakage lands later. And a theme that fails to parse is
not an error page: `internal/theme/manager.go` logs one warning and drops it,
then `cmd/ocms/main.go` quietly activates whichever theme sorts first — the two
core themes are always embedded, so there is always one to fall back to and the
site keeps serving, just not with your theme.

Guarding against that requires a no-op placeholder in
`internal/render/render.go`, which is a core edit, and
`TestEveryModuleTemplateFuncHasRendererPlaceholder` enforces it with a
deliberate no-allowlist policy — so omitting the placeholder turns the core test
suite red instead.

Expose your data over a route instead. `RegisterRoutes` gives you a public
endpoint, `RegisterAdminRoutes` an authenticated one, and a theme can call
either. `custom/modules/bookmarks/` shows the shape: it used to ship
`bookmarkCount`/`bookmarkFavorites` and now serves the same data from
`GET /bookmarks` — `?favorites=1` for the favourites, and the `total` field of
the *unfiltered* response for the count (`total` always reflects the returned
subset, so under `?favorites=1` it counts favourites).

## Modules in a multi-site deployment

Several sites can share one checkout of this repository, building one binary
that every instance runs. That arrangement has three consequences worth planning
for.

**Keep the source in the site repository, not here.** `custom/modules/*` is
gitignored apart from what ships with oCMS, so a module dropped straight into
this tree is version-controlled nowhere and one `git clean -fdx` destroys it.
Keep it in the site repo alongside its themes and copy it in before building.

oCMS ships the target for you. `site.mk` at the root of this repository carries
the whole site build — including `sync-modules`, which every build and test
target already depends on — so a site Makefile is normally one line:

```make
include core/site.mk
```

Run `make help` in the site repo for the target list, and override any variable
*before* the include — they all use `?=`, so a later `?=` is a silent no-op (a
plain `=` after the include still wins). `CORE_DIR` in particular must be set
before the include. `.DEFAULT_GOAL` is the exception: override it *after*:

```make
BINARY_NAME ?= mysite
include core/site.mk
```

Overriding `BINARY_NAME` also means exporting it for `deploy-binary.sh`, which
otherwise looks for `bin/ocms-linux-amd64`.

Sharing one definition is the point: `git pull` in this repository updates the
build logic for every site at once, so a site cannot end up unable to compile a
module because its hand-copied Makefile predates the feature.

`sync-modules` mirrors rather than merely copies: before syncing it removes any
module in this tree that neither ships with oCMS nor belongs to the site being
built. Without that, the previous site's modules would stay behind and compile
into the next site's binary — and since `Registry.InitAll` migrates every
registered module (see below), their tables would appear in a database that
should never have had them. It refuses outright to overwrite a module that ships
with oCMS, so a site module named `bookmarks` is an error rather than a silent
clobber. `make clean-modules` removes the site's own copies again. Both targets need the
core checkout to be a git working tree — that is how they tell oCMS's own
modules from a site's copies — and abort rather than guess if it is not.

Set `OCMS_DB_PATH` to an **absolute** path in each site's `.env`. The migrate
targets refuse a relative one: `dev`/`run` `cd` into the core checkout, so a
relative path would have goose migrating the site's database while the server
opened one under `core/` shared with every other site.

Copy, do not symlink: Go's wildcards skip symlinked directories, so
`go test ./...` would silently omit the module and report success with failing
tests inside it.

**The module ships to every instance.** One binary serves them all, and
`Registry.InitAll` runs migrations for *all* registered modules before it reads
active status — so a module's tables are created in every instance's database
whether or not that instance uses it. To keep it switched off elsewhere,
implement `EnvironmentChecker` and read a per-instance environment variable:

```go
func (m *Module) AllowedEnvs() []string {
	if strings.EqualFold(os.Getenv("OCMS_MYMODULE_ENABLED"), "true") {
		return []string{"development", "production"}
	}
	return nil // empty ⇒ every environment disallowed ⇒ registers inactive
}
```

Each instance has its own `.env` (systemd `EnvironmentFile=`), so only the sites
that opt in register it active. Pair it with `ActivationGuard.CheckActivation`
to refuse a manual toggle from the admin UI elsewhere — `AllowedEnvs` only sets
the default at first registration; after that the `modules` table wins.

**Registration files never collide.** Because each module brings its own
`imports_<name>.go`, two sites sharing this checkout never edit the same file.

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

    TemplateFuncs() template.FuncMap       // Built-in modules only — do not override
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
| `SchedulerRegistry` | `*scheduler.Registry` | Register cron jobs (may be nil in tests) |
| `Cache`  | `*cache.Manager`       | Invalidate cached content (may be nil — nil-guard it) |
| `RedirectCacheInvalidator` | `RedirectCacheInvalidator` | Make redirect writes visible immediately (may be nil) |
| `PublicRouteChecker` | `PublicRouteChecker` | Avoid shadowing routes owned by core or another module |

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
- `module.HookSecurityHoneypotTriggered` — triggered when a form honeypot field is filled

Hook handlers from inactive modules are automatically skipped.

## Template Functions — not for custom modules

`module.Module` declares `TemplateFuncs()`, and the built-in modules under
`modules/` use it. **A custom module must not**, for the reason given in
[The one thing a custom module cannot do](#the-one-thing-a-custom-module-cannot-do):
every module func needs a matching no-op placeholder in
`internal/render/render.go`, which is a core edit, and
`TestEveryModuleTemplateFuncHasRendererPlaceholder` fails without one.

The built-ins can do it because they ship *with* core, so their placeholders are
part of the same change.

The same applies to layout injection. The frontend renders module HTML into
`<head>`, body-top and body-end from `BaseTemplateData.ModuleHeadHTML`,
`ModuleBodyTopHTML` and `ModuleBodyEndHTML` — but those are filled by looking up
a **hardcoded list of function names** in `internal/handler/frontend.go`
(`privacyHead`, `analyticsExtHead`, `embedHead`, `informerBar`,
`analyticsExtBody`, `embedBody`, `analyticsIntReadTracker`). Adding a name means
editing core, so a custom module cannot join that list either.

### What to do instead

Serve the data or markup from a route and let the theme fetch it:

```go
func (m *Module) RegisterRoutes(r chi.Router) {
    limiter := middleware.NewGlobalRateLimiter(2.0, 5)
    r.With(limiter.Middleware()).Get("/myitems", m.handleList)
}
```

This is strictly more capable than a template function — the endpoint is
reachable from a theme, a script, htmx, or anything else — and it costs no core
edit. Two working examples:

- `custom/modules/bookmarks/` serves `GET /bookmarks?favorites=1`, which is what
  replaced its former `bookmarkCount`/`bookmarkFavorites` funcs.
- A block that has to appear *inside* page content can be marked with an
  ordinary link that a small theme script swaps for the fetched markup. A link
  survives the page-HTML sanitizer, reads as prose in every derived snippet
  (meta description, list excerpts, search results, Markdown export), and still
  works when the script does not run.

If you genuinely need output injected into every page's `<head>` or `<body>`,
that is a built-in module's job — move the code to `modules/` and add the
placeholder and injection-list entry in the same change.

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
- Public JSON API — the `?favorites=1` filter replaced `bookmarkFavorites`, and
  the `total` field of the unfiltered response replaced `bookmarkCount`
- Admin dashboard with embedded template
- Hook handlers
- i18n translations (English and Russian)
- Comprehensive test suite

See also the built-in example module at `modules/example/` for a module that uses the core renderer.
