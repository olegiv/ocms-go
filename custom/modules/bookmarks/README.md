# Bookmarks Module

A custom module example demonstrating the oCMS module system. Provides bookmark management with favorites, CRUD operations, and a standalone admin interface.

## Features

- Public JSON API for listing bookmarks
- Admin dashboard with embedded HTML template
- Bookmark CRUD (create, list, toggle favorite, delete)
- Template functions for use in themes
- Hook handler for page save events
- Database migrations with rollback support
- Embedded i18n translations (English, Russian)
- Self-registration via `init()` pattern

## Routes

### Public Routes

| Method | Path         | Description                |
|--------|--------------|----------------------------|
| GET    | `/bookmarks` | List all bookmarks (JSON)  |

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

## Template Functions

Available in all theme templates when the module is active:

```html
<!-- Total bookmark count -->
<p>You have {{ bookmarkCount }} bookmarks saved.</p>

<!-- List favorite bookmarks -->
{{ range bookmarkFavorites }}
    <a href="{{ .URL }}">{{ .Title }}</a>
{{ end }}
```

| Function             | Returns       | Description                   |
|----------------------|---------------|-------------------------------|
| `bookmarkCount`      | `int`         | Total number of bookmarks     |
| `bookmarkFavorites`  | `[]Bookmark`  | All bookmarks marked favorite |

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

Enabled by a blank import in `custom/modules/imports.go`:

```go
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
- Template functions (with and without data)
- Hook registration and execution
- HTTP handlers (public API, admin dashboard, create, toggle, delete)
- Translations filesystem embedding
- Error handling (not found conditions)
