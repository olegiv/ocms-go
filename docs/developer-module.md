# Developer Module

The Developer module is a development tool for generating random test data in OCMS. It creates tags, categories, media (placeholder images), pages, and menu items with full translations for all active languages. All generated content is tracked and can be bulk-deleted.

## Overview

This module is intended for **development and testing purposes only**. It helps developers and testers quickly populate the CMS with realistic content to test themes, APIs, and frontend rendering.

### Features

- Generate 5-20 random tags with translations
- Generate 5-20 random categories (nested hierarchy) with translations
- Generate 5-20 placeholder images (colored rectangles)
- Generate 5-20 published pages with tags, categories, and featured images
- Generate 5-20 menu items in Main Menu (nested, linked to pages)
- Automatic translations for all active languages
- Track all generated items for bulk deletion
- Clean up generated content with one click

## Installation

The Developer module is registered in `cmd/ocms/main.go`:

```go
import "ocms-go/modules/developer"

// In main()
if err := moduleRegistry.Register(developer.New()); err != nil {
    return fmt.Errorf("registering developer module: %w", err)
}
```

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
5. **Menu Items (5-20)**: Items in Main Menu (ID=1), ~40% nested, linked to generated pages

Each item includes translations for all active languages. Translation names include language code suffix (e.g., "Technology (de)" for German).

### Delete All Generated Data

Click **Delete All Generated Data** to remove all content created by this module:

1. Menu items are deleted first
2. Translation records are removed
3. Pages and their associations (tags, categories) are cleared
4. Media files are deleted from disk and database
5. Categories and tags are removed
6. Tracking table is cleared

The Main Menu itself (ID=1) is preserved; only generated items within it are deleted.

## How It Works

### Item Tracking

The module uses a dedicated tracking table to remember which items it created:

```sql
CREATE TABLE developer_generated_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,  -- 'tag', 'category', 'media', 'page', 'menu_item', 'translation'
    entity_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL
);
```

This allows the module to delete only its own content without affecting manually created items.

### Image Generation

Placeholder images are generated using Go's standard library (`image` and `image/jpeg`):

- **Size**: 800x600 pixels (original)
- **Format**: JPEG at 85% quality
- **Colors**: 10 predefined colors (blue, red, yellow, green, purple, cyan, orange, light green, indigo, pink)
- **Variants**: thumbnail (150x150), medium (800x600), large (1920x1080)

Images are saved to:
- `./uploads/originals/{UUID}/placeholder-N.jpg`
- `./uploads/thumbnail/{UUID}/placeholder-N.jpg`
- `./uploads/medium/{UUID}/placeholder-N.jpg`
- `./uploads/large/{UUID}/placeholder-N.jpg`

### Translation Generation

For each generated item:

1. The base item is created in the default language
2. For each additional active language:
   - A translated version is created with language code suffix
   - A translation record links the translated item to the original
3. All items (base and translations) are tracked for deletion

### Menu Item Structure

Menu items are created in the Main Menu (ID=1):

- ~60% are root-level items
- ~40% are nested under root items
- Each item links to a generated page via `page_id`
- Positions are assigned sequentially after existing items

## Database Schema

### Tracking Table

| Column | Type | Description |
|--------|------|-------------|
| id | INTEGER | Primary key |
| entity_type | TEXT | Type: 'tag', 'category', 'media', 'page', 'menu_item', 'translation' |
| entity_id | INTEGER | ID of the tracked entity |
| created_at | DATETIME | When the item was generated |

## Module Structure

```
modules/developer/
├── module.go       # Module definition, lifecycle, migrations
├── handlers.go     # HTTP handlers (dashboard, generate, delete)
└── generator.go    # Random data generation and tracking logic
```

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
    return rand.Intn(16) + 5 // 5-20
}
```

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

## Security

The module is protected by:

- Admin authentication middleware
- CSRF token validation on POST requests
- Module is designed for development environments only

The warning badge and confirmation dialog remind users that this module should not be used in production.
