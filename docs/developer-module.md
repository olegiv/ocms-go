# Developer Module

The Developer module is a development tool for generating random test data in OCMS. It creates tags, categories, media (placeholder images), pages, and menu items with full translations for all active languages. All generated content is tracked and can be bulk-deleted.

## Overview

This module is intended for **development and testing purposes only**. It helps developers and testers quickly populate the CMS with realistic content to test themes, APIs, and frontend rendering.

### Features

- Generate 5-20 random tags with translations
- Generate 5-20 random categories (nested hierarchy) with translations
- Generate 5-20 placeholder images (colored rectangles)
- Generate 5-20 published pages with tags, categories, and featured images
- Generate 3-7 menu items in every menu (nested, linked to pages)
- Automatic translations for all active languages
- Track all generated items for bulk deletion
- Clean up generated content with one click

## Installation

The Developer module is registered in `registerModules` in `cmd/ocms/main.go`:

```go
import "github.com/olegiv/ocms-go/modules/developer"

// In registerModules(), as an entry of the modules slice
modules := []module.Module{
    developer.New(),
    // ...
}
for _, mod := range modules {
    if err := registry.Register(mod); err != nil {
        return nil, fmt.Errorf("registering %s module: %w", mod.Name(), err)
    }
}
```

The module is limited to the `development` environment: in any other
environment it is inserted as inactive on first registration, and it refuses
to initialize when `OCMS_ENV` is `production`.

## Admin Interface

Access the Developer module at **Admin > Modules > Developer Tools** or directly at `/admin/developer`.

### Dashboard

The dashboard displays:

| Metric | Description |
|--------|-------------|
| Tags | Number of generated tags |
| Categories | Number of generated categories |
| Images | Number of generated placeholder images |
| Pages | Number of generated pages |
| Menu Items | Number of generated menu items |

### Generate Test Data

Click **Generate Test Data** to create random content:

1. **Tags (5-20)**: Random words from predefined lists (e.g., "Technology", "Amazing Science")
2. **Categories (5-20)**: Nested structure with ~40% root, ~40% children, ~20% grandchildren
3. **Images (5-20)**: Colored placeholder JPEGs (800x600) with variants
4. **Pages (5-20)**: Published pages with Lorem Ipsum content, 1-3 tags, 1-2 categories, featured image
5. **Menu Items (3-7 per menu)**: Items in every existing menu, ~40% nested, linked to pages

Each item includes translations for all active languages. Translation names include language code suffix (e.g., "Technology (de)" for German).

### Delete All Generated Data

Click **Delete All Generated Data** to remove all content created by this module:

1. Menu items are deleted first
2. Translation records are removed
3. Pages and their associations (tags, categories) are cleared
4. Media rows and variants are deleted from the database, then their files are removed from disk
5. Categories and tags are removed
6. Tracking table is cleared

Menus themselves are preserved; only generated items within them are deleted.

File removal goes through a persisted retry queue (`developer_media_cleanup_queue`).
Files that could not be removed stay queued and are retried on the next delete
and when the module initializes; while earlier cleanups are still pending,
a delete is refused with an error.

## How It Works

### Item Tracking

The module uses a dedicated tracking table to remember which items it created:

```sql
CREATE TABLE developer_generated_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,  -- 'tag', 'category', 'media', 'page', 'menu_item', 'translation'
    entity_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

This allows the module to delete only its own content without affecting manually created items.

### Image Generation

Placeholder images are generated using Go's standard library (`image` and `image/jpeg`):

- **Size**: 800x600 pixels (original)
- **Format**: JPEG at 85% quality
- **Colors**: 10 predefined colors (blue, red, yellow, green, purple, cyan, orange, light green, indigo, pink)
- **Variants**: thumbnail (150x150), grid (256x256), small (400x300), medium (800x600), large (1920x1080), og (1200x630); the same six as `model.ImageVariants`

Images are saved under `OCMS_UPLOADS_DIR` (default `./uploads`):
- `./uploads/originals/{UUID}/placeholder-N.jpg`
- `./uploads/{variant}/{UUID}/placeholder-N.jpg` for each of the six variants above

### Translation Generation

For each generated item:

1. The base item is created in the default language
2. For each additional active language:
   - A translated version is created with language code suffix
   - A translation record links the translated item to the original
3. All items (base and translations) are tracked for deletion

### Menu Item Structure

Menu items are created in every existing menu (3-7 items per menu):

- ~60% are root-level items
- ~40% are nested under root items
- Each item links to a page via `page_id`: a page in the menu's language, or in the default language for a menu without a language
- Menus with no pages in the needed language are skipped
- Positions are assigned sequentially after existing root items

## Database Schema

### Tracking Table

| Column | Type | Description |
|--------|------|-------------|
| id | INTEGER | Primary key |
| entity_type | TEXT | Type: 'tag', 'category', 'media', 'page', 'menu_item', 'translation' |
| entity_id | INTEGER | ID of the tracked entity |
| created_at | DATETIME | When the item was generated |

### Media Cleanup Queue

Migration 2 adds `developer_media_cleanup_queue`, which holds media file removals that still have to run (`upload_root`, `media_uuid`, `generation`, `attempts`, `last_error`, `created_at`, `updated_at`; unique per `upload_root` and `media_uuid`).

## Module Structure

```
modules/developer/
├── module.go        # Module definition, lifecycle, migrations
├── handlers.go      # HTTP handlers (dashboard, generate, delete)
├── generator.go     # Random data generation and tracking logic
├── media_cleanup.go # Persisted retry queue for generated media files
├── views.templ      # templ admin page (views_templ.go is generated)
└── locales/         # Embedded i18n translations
    ├── en/
    │   └── messages.json
    └── ru/
        └── messages.json
```

## Internationalization (i18n)

The Developer module includes full i18n support with embedded translations:

### Supported Languages
- **English (en)**: Default language
- **Russian (ru)**: Full translation

### Translation Keys

All translation keys are prefixed with `developer.`:

| Key | Description |
|-----|-------------|
| `developer.title` | Page title |
| `developer.description` | Module description |
| `developer.warning_*` | Development-only warning |
| `developer.stat_*` | Statistics labels |
| `developer.generate_*` | Generate section text, button, and confirmation |
| `developer.delete_*` | Delete section text, button, and confirmation |
| `developer.error_*` | Error flash messages |
| `developer.success_*` | Success flash messages |

### Adding Translations

To add a new language, create a new directory under `locales/`:

```
locales/de/messages.json
```

The translation file follows this structure:

```json
{
  "$schema": "../../../../.schema/i18n-schema.json",
  "language": "de",
  "messages": [
    {"id": "developer.title", "message": "Developer Tools", "translation": "Entwickler-Tools"},
    {"id": "developer.description", "message": "Generate random test data...", "translation": "Testdaten generieren..."}
  ]
}
```

Translations are automatically loaded when the module initializes.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | /admin/developer | Dashboard with statistics |
| POST | /admin/developer/generate | Generate all test data |
| POST | /admin/developer/delete | Delete all generated data |

## Configuration

The module uses fixed random counts (5-20 items per type). This is not currently configurable but can be modified in `generator.go`:

```go
// generateRandomCount returns a random number between 5 and 20
func generateRandomCount() int {
    return cryptoRandIntn(16) + 5 // 5-20
}
```

Menu items are not covered by this count: each menu gets `cryptoRandIntn(5) + 3` (3-7) items.

## Word Lists

The module uses predefined word lists for generating realistic names:

### Adjectives (25 words)
Amazing, Beautiful, Creative, Dynamic, Elegant, Fantastic, Global, Helpful, Innovative, Joyful, Kind, Lovely, Modern, Natural, Outstanding, Perfect, Quality, Reliable, Smart, Trendy, Unique, Vibrant, Wonderful, Excellent, Zesty

### Nouns (25 words)
Technology, Science, Art, Design, Business, Health, Education, Travel, Food, Music, Sports, Nature, Culture, Fashion, Finance, Entertainment, Lifestyle, Photography, Architecture, Innovation, Marketing, Development, Research, Solutions, Services

## Troubleshooting

### "No active languages found"

Ensure at least one language is active in **Admin > Languages** before generating test data.

### Slug conflicts

If you see "UNIQUE constraint failed" errors, previously generated items may not have been fully deleted. Clear the tracking table and manually delete conflicting items:

```sql
DELETE FROM developer_generated_items;
-- Then manually delete items with conflicting slugs
```

### Files not deleted

If image files remain after deletion, check file permissions on the `./uploads` directory. The process needs write access to delete files.

## Best Practices

1. **Use in development only**: Never enable this module in production
2. **Clear before regenerating**: Delete existing generated data before creating new test data
3. **Check language setup**: Ensure languages are configured before generating content
4. **Review generated content**: Use the admin interface to verify items were created correctly

## Module Active Status

The Developer module can be enabled or disabled from **Admin > Modules**:

- **Active (default in development)**: All routes are accessible, module appears in sidebar (if enabled)
- **Inactive (default outside development)**: Routes return 404 (public) or redirect to modules list (admin)

The active status is persisted in the database and survives server restarts. In `production` the module cannot be enabled: its `Init` fails with "developer module cannot be enabled in production environment".

### Disabling the Module

1. Navigate to **Admin > Modules**
2. Find "Developer Tools" in the list
3. Toggle the switch to disable
4. Module routes become inaccessible immediately

### Re-enabling

Toggle the switch back to enable. No server restart required.

## Security

The module is protected by:

- Admin authentication middleware
- CSRF token validation on POST requests
- Module active status toggle for quick disable
- Module is designed for development environments only

The warning badge and confirmation dialog remind users that this module should not be used in production.
