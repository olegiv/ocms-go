# Developer Module

A development tool for generating random test data in oCMS with full multi-language support.

> **Warning**: This module is intended for **development and testing purposes only**. Do not use in production.

## Features

- Generate 5-20 random tags with translations
- Generate 5-20 random categories (nested hierarchy) with translations
- Generate 5-20 placeholder images (colored rectangles with variants)
- Generate 5-20 published pages with tags, categories, and featured images
- Generate 5-10 menu items in Main Menu (nested, linked to pages)
- Track all generated items for bulk deletion
- Clean up generated content with one click

## Admin Interface

Access at **Admin > Modules > Developer Tools** or `/admin/developer`.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/developer` | Dashboard with statistics |
| POST | `/admin/developer/generate` | Generate test data |
| POST | `/admin/developer/delete` | Delete all generated data |

## Usage

1. Navigate to the Developer module dashboard
2. Click **Generate Test Data** to create random content
3. Use **Delete All Generated Data** to clean up

## Item Tracking

The module tracks all generated items in a dedicated table:

```sql
CREATE TABLE developer_generated_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,  -- 'tag', 'category', 'media', 'page', 'menu_item', 'translation'
    entity_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL
);
```

This ensures only module-generated content is deleted during cleanup.

## Module Structure

```
modules/developer/
├── module.go         # Module definition, lifecycle, migrations
├── handlers.go       # HTTP handlers (dashboard, generate, delete)
├── generator.go      # Random data generation and tracking logic
├── generator_test.go # Unit tests
└── locales/          # Embedded i18n translations
    ├── en/messages.json
    └── ru/messages.json
```

## Internationalization

Translations are embedded in the module and automatically loaded. Supported languages:

- English (en)
- Russian (ru)

To add a new language, create `locales/{lang}/messages.json`.

## Full Documentation

See [docs/developer-module.md](../../docs/developer-module.md) for complete documentation including:

- Image generation details
- Translation generation workflow
- Menu item structure
- Troubleshooting guide
- Security considerations
