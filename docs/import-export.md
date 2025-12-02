# Import/Export

oCMS provides comprehensive import and export functionality to backup, migrate, or transfer content between installations.

## Overview

The import/export system supports:
- **JSON Export**: Portable JSON format for content data
- **ZIP Export**: JSON plus media files in a single archive
- **Selective Export**: Choose which content types to include
- **Conflict Resolution**: Handle duplicates during import

## Exporting Content

### Accessing Export

1. Navigate to **Admin > Config > Export**
2. Select content types to include
3. Choose export format (JSON or ZIP)
4. Click **Export**

### Export Options

| Option | Description |
|--------|-------------|
| **Pages** | Page content, SEO metadata, and translation links |
| **Page Status Filter** | All pages, published only, or drafts only |
| **Categories** | Hierarchical category structure |
| **Tags** | Tag definitions |
| **Media** | Media metadata (ZIP includes files) |
| **Menus** | Menu structures and items |
| **Forms** | Form definitions (optionally with submissions) |
| **Users** | User accounts (emails only, no passwords) |
| **Languages** | Language configuration |
| **Site Config** | Site settings |

### Export Formats

#### JSON Export

Creates a single `.json` file containing all selected content:

```bash
# Download results in:
ocms-export-2024-01-15.json
```

Best for:
- Smaller sites without many media files
- API consumption
- Version control

#### ZIP Export

Creates a `.zip` archive containing:
- `export.json` - Content data
- `media/` - All media files

```bash
# Download results in:
ocms-export-2024-01-15.zip
```

Best for:
- Complete site backups
- Migration between servers
- Sites with media files

### Export Schema

```json
{
    "version": "1.0",
    "exported_at": "2024-01-15T10:30:00Z",
    "site": {
        "name": "My Site",
        "base_url": "https://example.com"
    },
    "languages": [...],
    "users": [...],
    "categories": [...],
    "tags": [...],
    "pages": [...],
    "media": [...],
    "menus": [...],
    "forms": [...],
    "config": {...}
}
```

#### Page Export Format

```json
{
    "id": 123,
    "title": "About Us",
    "slug": "about-us",
    "body": "<p>Content here...</p>",
    "status": "published",
    "author_email": "admin@example.com",
    "categories": ["company", "info"],
    "tags": ["about", "company"],
    "seo": {
        "meta_title": "About Our Company",
        "meta_description": "Learn about us",
        "meta_keywords": "company,about",
        "og_image": "media/og-image.jpg",
        "no_index": false,
        "no_follow": false,
        "canonical_url": ""
    },
    "translations": {
        "ru": 456
    },
    "language_code": "en",
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-15T10:30:00Z",
    "published_at": "2024-01-10T12:00:00Z"
}
```

## Importing Content

### Accessing Import

1. Navigate to **Admin > Config > Import**
2. Upload your export file (JSON or ZIP)
3. Click **Validate** to preview changes
4. Configure import options
5. Click **Import**

### Import Process

1. **Upload**: Select your export file
2. **Validate**: Preview what will be imported
3. **Configure**: Set conflict resolution strategy
4. **Import**: Execute the import
5. **Review**: Check the import summary

### Import Options

| Option | Description |
|--------|-------------|
| **Conflict Strategy** | How to handle existing content |
| **Content Types** | Which types to import (checkboxes) |
| **Dry Run** | Preview without making changes |

### Conflict Resolution

When importing content that already exists (matched by slug or email):

| Strategy | Behavior |
|----------|----------|
| **Skip** | Keep existing, ignore imported |
| **Overwrite** | Replace existing with imported |
| **Rename** | Import with modified slug (adds `-1`, `-2`, etc.) |

### Import Order

Content is imported in this order to maintain relationships:

1. Languages
2. Users
3. Categories
4. Tags
5. Pages
6. Media
7. Menus
8. Forms

### Validation Errors

The validator checks for:
- Valid JSON format
- Required fields present
- Reference integrity (authors, categories exist)
- File format compatibility

Example validation output:
```
Found:
  - 15 pages
  - 5 categories
  - 10 tags
  - 50 media files

Warnings:
  - Page "about-us" already exists (will skip)
  - Category "news" already exists (will overwrite)

Errors:
  - Page 5 references non-existent author "unknown@email.com"
```

### Import Summary

After import completes:

```
Import Complete:

Created:
  - 12 pages
  - 3 categories
  - 8 tags
  - 45 media files

Updated:
  - 2 categories

Skipped:
  - 1 page (duplicate)

Errors:
  - 0
```

## Migration Guide

### Migrating to a New Server

1. **Export from source**:
   - Go to Export, select all content types
   - Choose ZIP format (includes media)
   - Download the archive

2. **Prepare target**:
   - Install oCMS on the new server
   - Run migrations
   - Create admin user

3. **Import to target**:
   - Go to Import
   - Upload the ZIP file
   - Validate and review
   - Set conflict strategy to "Overwrite"
   - Import

4. **Verify**:
   - Check pages render correctly
   - Verify media files load
   - Test translation links

### Partial Migration

To migrate only specific content:

1. Export with only needed content types selected
2. Import to target with "Skip" conflict strategy
3. Manually resolve any conflicts

## Backup Strategies

### Manual Backups

Schedule regular exports:
```bash
# Create backup script
#!/bin/bash
DATE=$(date +%Y-%m-%d)
curl -X POST "https://yoursite.com/admin/export" \
    -H "Cookie: session=..." \
    -o "backup-$DATE.zip"
```

### Automated Backups

For automated backups, consider:
1. SQLite database backup (direct file copy)
2. uploads directory backup
3. Periodic export via API

### Backup Checklist

- [ ] Export includes all content types
- [ ] ZIP format includes media files
- [ ] Backup stored in separate location
- [ ] Periodic restore tests
- [ ] Backup rotation policy

## Troubleshooting

### Import Fails

**"Invalid JSON format"**
- Ensure the file is valid JSON
- Check for file corruption during transfer
- Try re-exporting from source

**"Reference not found"**
- Import in correct order (users before pages)
- Ensure referenced content exists
- Check author emails match

**"Media file missing"**
- Use ZIP export/import for media
- Verify media directory permissions
- Check file paths in export

### Large Imports

For very large sites:

1. **Split the import**: Export/import content types separately
2. **Increase timeout**: Set longer PHP/server timeouts
3. **Import via CLI**: Future feature

### Export Too Large

If export file is too large:

1. Export without media (JSON only)
2. Transfer media files separately
3. Export in parts (pages, then media, etc.)

## API Access

### Export via API

```bash
curl -X POST "http://localhost:8080/admin/export" \
    -H "Cookie: session=your-session-cookie" \
    -d "include_pages=true&include_media=true&format=json" \
    -o export.json
```

### Import via API

```bash
curl -X POST "http://localhost:8080/admin/import" \
    -H "Cookie: session=your-session-cookie" \
    -F "file=@export.json" \
    -F "conflict_strategy=skip"
```

## Best Practices

1. **Regular backups**: Export weekly at minimum
2. **Test restores**: Periodically test importing to a test instance
3. **Version exports**: Include date in filenames
4. **Use ZIP for migration**: Ensures media files included
5. **Validate first**: Always validate before importing
6. **Backup before import**: Export existing content before importing new
7. **Review changes**: Check validation output carefully

## Schema Reference

### Full Export Schema

```json
{
    "version": "1.0",
    "exported_at": "2024-01-15T10:30:00Z",
    "site": {
        "name": "string",
        "base_url": "string",
        "tagline": "string"
    },
    "languages": [
        {
            "code": "string",
            "name": "string",
            "native_name": "string",
            "direction": "ltr|rtl",
            "is_default": "boolean",
            "is_active": "boolean",
            "position": "number"
        }
    ],
    "users": [
        {
            "email": "string",
            "name": "string",
            "role": "admin|editor",
            "is_active": "boolean"
        }
    ],
    "categories": [
        {
            "id": "number",
            "name": "string",
            "slug": "string",
            "description": "string",
            "parent_id": "number|null",
            "language_code": "string"
        }
    ],
    "tags": [
        {
            "name": "string",
            "slug": "string",
            "language_code": "string"
        }
    ],
    "pages": [
        {
            "id": "number",
            "title": "string",
            "slug": "string",
            "body": "string",
            "status": "draft|published",
            "author_email": "string",
            "categories": ["string"],
            "tags": ["string"],
            "seo": {
                "meta_title": "string",
                "meta_description": "string",
                "meta_keywords": "string",
                "og_image": "string",
                "no_index": "boolean",
                "no_follow": "boolean",
                "canonical_url": "string"
            },
            "translations": {
                "lang_code": "page_id"
            },
            "language_code": "string",
            "created_at": "datetime",
            "updated_at": "datetime",
            "published_at": "datetime|null"
        }
    ],
    "media": [
        {
            "uuid": "string",
            "filename": "string",
            "original_name": "string",
            "mime_type": "string",
            "size": "number",
            "path": "string",
            "alt_text": "string",
            "caption": "string"
        }
    ],
    "menus": [
        {
            "name": "string",
            "slug": "string",
            "language_code": "string",
            "items": [
                {
                    "title": "string",
                    "url": "string",
                    "page_id": "number|null",
                    "parent_id": "number|null",
                    "position": "number",
                    "is_active": "boolean"
                }
            ]
        }
    ],
    "forms": [
        {
            "name": "string",
            "slug": "string",
            "description": "string",
            "fields": [
                {
                    "name": "string",
                    "label": "string",
                    "type": "string",
                    "required": "boolean",
                    "options": "string",
                    "position": "number"
                }
            ],
            "submissions": [
                {
                    "data": {"field": "value"},
                    "submitted_at": "datetime"
                }
            ]
        }
    ],
    "config": {
        "key": "value"
    }
}
```
