# Bookmarks Module

A custom module example demonstrating the oCMS module system. Provides bookmark management with favorites, CRUD operations, and a standalone admin interface.

## Features

- Public JSON API for listing bookmarks
- Admin dashboard with embedded HTML template
- Bookmark CRUD (create, list, toggle favorite, delete)
- Hook handler for page save events
- Database migrations with rollback support
- Embedded i18n translations (English, Russian)
- Self-registration via `init()` pattern

## Routes

### Public Routes

| Method | Path         | Description               |
|--------|--------------|---------------------------|
| GET    | `/bookmarks` | List all bookmarks (JSON) |

Query parameters:

| Parameter     | Effect                                          |
|---------------|-------------------------------------------------|
| `favorites=1` | Return only bookmarks marked favorite           |

Any other value (or no parameter) returns the full list. This filter is what
replaced the module's former `bookmarkFavorites` template function — see
[Template Functions](#template-functions--deliberately-none).

Response format:

```json
{
    "bookmarks": [
        {
            "id": 1,
            "title": "Go Documentation",
            "url": "https://go.dev",
            "description": "Official Go docs",
            "is_favorite": true,
            "created_at": "2026-01-15T10:30:00Z"
        }
    ],
    "total": 1
}
```

### Admin Routes

| Method | Path                           | Description         |
|--------|--------------------------------|---------------------|
| GET    | `/admin/bookmarks`             | Admin dashboard     |
| POST   | `/admin/bookmarks`             | Create bookmark     |
| POST   | `/admin/bookmarks/{id}/toggle` | Toggle favorite     |
| DELETE | `/admin/bookmarks/{id}`        | Delete bookmark     |

## Template Functions — deliberately none

This module used to export `bookmarkCount` and `bookmarkFavorites`. It no longer
does, and that is the point it now exists to teach: a custom module must not
implement `TemplateFuncs()`.

`Registry.AllTemplateFuncs()` returns functions from *active* modules only, while
`html/template` resolves function names at *parse* time. Deactivate the module
and any theme calling one of its functions fails to parse on the next restart —
and an unparseable theme is not an error page, it is silently dropped in favour
of another theme. Guarding against that needs a no-op placeholder in
`internal/render/render.go`, i.e. a core edit, which is precisely what this
package is meant to prove unnecessary.

Get the same data from the public route instead:

```bash
curl 'http://localhost:8080/bookmarks'              # {"bookmarks":[…],"total":N}
curl 'http://localhost:8080/bookmarks?favorites=1'  # favourites only
```

A theme can fetch either with htmx, Alpine, or plain `fetch()`. See
[docs/custom-modules.md](../../../docs/custom-modules.md#the-one-thing-a-custom-module-cannot-do).
`TestModuleExposesNoTemplateFuncs` pins this.

## Hook Handlers

| Hook                    | Handler Name          | Priority | Description                  |
|-------------------------|-----------------------|----------|------------------------------|
| `page.after_save`       | `bookmarks_page_saved`| 20       | Logs page save events        |

The hook handler demonstrates how custom modules can react to page lifecycle events. In practice, this could be used to auto-bookmark pages, send notifications, or trigger external integrations.

## Database Schema

```sql
CREATE TABLE bookmarks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    description TEXT DEFAULT '',
    is_favorite BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

## Module Structure

```
custom/modules/bookmarks/
├── module.go          # Module definition, lifecycle, hooks, migrations
├── handlers.go        # HTTP handlers and database operations
├── register.go        # init() self-registration
├── bookmarks_test.go  # Comprehensive test suite
├── README.md          # This file
├── templates/
│   └── admin.html     # Embedded admin dashboard template
└── locales/
    ├── en/messages.json
    └── ru/messages.json
```

## Self-Registration

The module uses Go's `init()` pattern for auto-registration:

```go
// register.go
package bookmarks

import "github.com/olegiv/ocms-go/internal/module"

func init() {
    module.RegisterCustomModule(New())
}
```

Enabled by its own registration file, `custom/modules/imports_bookmarks.go`:

```go
package modules

import _ "github.com/olegiv/ocms-go/custom/modules/bookmarks"
```

## Internationalization

Translations are embedded in `locales/{lang}/messages.json` and loaded automatically by the module registry.

Supported languages:
- English (`en`)
- Russian (`ru`)

Translation keys follow the `bookmarks.*` namespace convention. Add new languages by creating `locales/{lang}/messages.json`.

## Testing

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test -v ./custom/modules/bookmarks/...
```

The test suite covers:
- Module metadata (name, version, description, admin URL, sidebar label)
- Database migrations (up and down)
- CRUD operations (create, list, toggle favorite, delete)
- Absence of template functions (`TestModuleExposesNoTemplateFuncs`)
- Hook registration and execution
- HTTP handlers (public API, admin dashboard, create, toggle, delete)
- Translations filesystem embedding
- Error handling (not found conditions)
