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

1. Navigate to **Admin > Export** (administrators only)
2. Select content types to include
3. Tick **Include Media Files** (it needs **Media** ticked) to get a ZIP archive; otherwise you get a JSON file
4. Click **Download Export** (**Download ZIP Archive** when media files are included)

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
| **Users** | User accounts (passwords are never exported) |
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
- `media/originals/<uuid>/<filename>` - Required original for each selected media item
- `media/<variant>/<uuid>/<filename>` - Every variant declared in `export.json`

```bash
# Download results in:
ocms-export-2024-01-15.zip
```

Best for:
- Complete site backups
- Migration between servers
- Sites with media files

ZIP output is staged and completely validated before download or publication.
An unreadable original or declared variant fails the export without publishing a
partial archive or replacing an existing target file.

### Media Archive Contract

Media UUIDs use the canonical hyphenated UUID form. Archive-declared media
identities accept uppercase hexadecimal input and normalize new database,
reference, and filesystem identities to lowercase. A page-only import may reuse
an existing media row only when its structured reference and recognized upload
URLs use that row's exact stored UUID spelling; case-mismatched or ambiguous
destination identities are rejected before any write. A media path has exactly
this shape:

```text
media/<storage>/<uuid>/<filename>
```

`<storage>` is `originals` or a standard image variant directory. Each media
record declares its original `file_path`; each exported variant declares its own
`file_path`. The manifest and ZIP are checked in both directions: undeclared ZIP
entries, missing declared entries, duplicate paths, UUID/filename mismatches,
unknown storage directories, and declared-size mismatches are rejected.

The compressed ZIP limit is 100 MiB. A ZIP may contain at most 5,000 media file
entries, each entry may expand to at most 32 MiB, and all media entries together
may expand to at most 512 MiB. The uncompressed `export.json` entry is limited
to 32 MiB. Only `export.json` and declared media files are allowed; undeclared
files and explicit directory entries are rejected. Multi-disk and ZIP64
archives are not supported, and the central-directory entry count is bounded
before ZIP parsing. Exports enforce the same limits as validation and import,
so oCMS does not emit an archive it would refuse to restore.

### Export Schema

```json
{
    "version": "1.0",
    "exported_at": "2024-01-15T10:30:00Z",
    "site": {
        "name": "My Site",
        "description": "Optional site description",
        "url": "https://example.com"
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
        "og_image": {"uuid": "550e8400-e29b-41d4-a716-446655440000", "filename": "og-image.jpg"},
        "no_index": false,
        "no_follow": false,
        "canonical_url": ""
    },
    "featured_image": {"uuid": "550e8400-e29b-41d4-a716-446655440001", "filename": "hero.jpg"},
    "translations": {
        "ru": 456
    },
    "language_code": "en",
    "video_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    "video_title": "Optional video title",
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-15T10:30:00Z",
    "published_at": "2024-01-10T12:00:00Z"
}
```

## Importing Content

### Accessing Import

1. Navigate to **Admin > Import** (administrators only)
2. Upload your export file (JSON or ZIP)
3. Click **Validate File** to preview changes
4. Configure import options
5. Click **Start Import**

### Import Process

1. **Upload**: Select your export file
2. **Validate**: Preview what will be imported. The uploaded file is kept on the server for two hours (the session idle timeout) while you review; after that, upload it again. The file is staged on the instance that received the upload, so a load-balanced deployment must route the validate and import steps of one session to the same instance (sticky sessions)
3. **Configure**: Set conflict resolution strategy
4. **Import**: Execute the import
5. **Review**: Check the import summary

### Import Options

| Option | Description |
|--------|-------------|
| **Conflict Strategy** | How to handle existing content |
| **Content Types** | Which types to import (checkboxes) |
| **Import Media Files** | Copy the media files from a ZIP archive into the uploads directory (ZIP only; see [Media Restore Modes](#media-restore-modes)) |
| **Dry Run** | Preview without making changes |

### Conflict Resolution

When importing content that already exists (matched by slug or email):

| Strategy | Behavior |
|----------|----------|
| **Skip** | Keep existing, ignore imported |
| **Overwrite** | Replace existing with imported |
| **Rename** | Import with modified slug (adds `-1`, `-2`, etc.); users are skipped instead, since the email is their identity |

Users that do not exist yet are created with a random, unknown password; an administrator must set a new one under **Admin > Users** before they can sign in.

### Import Order

Content is imported in this order to maintain relationships:

1. Languages
2. Users
3. Categories
4. Tags
5. Media
6. Pages
7. Menus
8. Forms
9. Site config

### Validation Errors

The validator checks for:
- Valid JSON format
- Required fields present
- Reference integrity (authors, categories exist)
- File format compatibility
- Canonical media UUID and manifest/path consistency

### Media Restore Modes

- **Metadata only**: importing media from JSON changes database metadata only;
  it never creates, replaces, or deletes upload files.
- **Archive kind is enforced**: the media-file restore option is rejected for
  JSON imports and is available only to ZIP entry points with a validated
  manifest.
- **Metadata and files**: every selected media item must have an original in the
  ZIP. Every declared variant must also be present. New and overwritten image
  rows generate only absent standard variants; declared variant bytes are never
  overwritten. Every restored image original and declared image variant is
  fully decoded and checked against its declared dimensions. The actual
  generated dimensions and sizes are stored in the same database transaction
  as the media row.
- **Files only**: the destination must already contain the same UUID, filename,
  MIME type, size, dimensions, and variant metadata. No media row or variant row
  is created or changed.

When either file restore mode is selected, its option-aware preflight checks the
destination media identity and whole-UUID storage freshness before writing. A
dry run additionally stages extraction, image decoding, and missing-variant
generation in an isolated temporary root, leaving the configured uploads tree
unchanged.

File restoration owns the complete storage namespace for a UUID. Before any
write, oCMS requires every standard storage directory for that UUID to be
absent. Existing files are never truncated or merged, including with
`Overwrite`. Extraction and generated variants use one verified upload-root
capability and exclusive file creation. Immediately before the database commit,
oCMS verifies an exact identity ledger for every created file and directory and
rejects extra entries, case aliases, or replacements. Failure compensation
removes only files and empty directories whose filesystem identity still
matches that ledger; concurrent replacements and unowned sentinels are
preserved. Operational ownership uncertainty likewise preserves files and
returns an explicit error rather than deleting potentially committed media.

Dry-run performs the same manifest, destination-relation, freshness, extraction,
image-decoding, and variant-generation checks as a real file import, but runs
filesystem work in an isolated temporary upload root and leaves both the
configured uploads directory and database unchanged.

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
    -H "Cookie: __Host-session=..." \
    -d "include_pages=on&include_media=on&include_media_files=on" \
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
2. **Keep media batches within archive limits**: 100 MiB compressed, 5,000
   files, 32 MiB per file, and 512 MiB expanded media per ZIP
3. **Validate each batch** before importing it

### Export Too Large

If export file is too large:

1. Export without media (JSON only)
2. Transfer media files separately
3. Export in parts (pages, then media, etc.)

## API Access

### Export via API

Export is a form post that needs an administrator session cookie (named `session` in development and `__Host-session` in production). Each content type is switched on with `include_<type>=on`; there is no `format` field, and ZIP output is chosen with `include_media=on&include_media_files=on`.

```bash
curl -X POST "http://localhost:8080/admin/export" \
    -H "Cookie: session=your-session-cookie" \
    -d "include_pages=on&include_media=on" \
    -o export.json
```

### Import via API

Import is two steps in one session: upload the file to `/admin/import/validate` (multipart field `import_file`), then post the options to `/admin/import`. The upload is staged server-side for the second step, so the same session cookie must be used for both.

```bash
curl -c cookies.txt -b "session=your-session-cookie" -X POST "http://localhost:8080/admin/import/validate" \
    -F "import_file=@export.json"
curl -b cookies.txt -X POST "http://localhost:8080/admin/import" \
    -d "conflict_strategy=skip&import_pages=on&import_categories=on&import_tags=on"
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
        "description": "string",
        "url": "string"
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
            "role": "string",
            "created_at": "datetime"
        }
    ],
    "categories": [
        {
            "id": "number",
            "name": "string",
            "slug": "string",
            "description": "string",
            "parent_slug": "string",
            "position": "number",
            "language_code": "string",
            "translations": {"lang_code": "category_id"},
            "created_at": "datetime",
            "updated_at": "datetime"
        }
    ],
    "tags": [
        {
            "id": "number",
            "name": "string",
            "slug": "string",
            "language_code": "string",
            "translations": {"lang_code": "tag_id"},
            "created_at": "datetime",
            "updated_at": "datetime"
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
                "og_image": {"uuid": "string", "filename": "string"},
                "no_index": "boolean",
                "no_follow": "boolean",
                "canonical_url": "string"
            },
            "featured_image": {"uuid": "string", "filename": "string"},
            "translations": {
                "lang_code": "page_id"
            },
            "language_code": "string",
            "created_at": "datetime",
            "updated_at": "datetime",
            "published_at": "datetime|null",
            "scheduled_at": "datetime|null",
            "video_url": "string",
            "video_title": "string"
        }
    ],
    "media": [
        {
            "uuid": "550e8400-e29b-41d4-a716-446655440000",
            "filename": "string",
            "mime_type": "string",
            "size": "number",
            "width": "number|null",
            "height": "number|null",
            "alt": "string",
            "caption": "string",
            "folder_path": "string",
            "uploaded_by": "email",
            "language_code": "string",
            "file_path": "media/originals/<uuid>/<filename>",
            "created_at": "datetime",
            "variants": [
                {
                    "type": "thumbnail",
                    "width": "number",
                    "height": "number",
                    "size": "number",
                    "file_path": "media/thumbnail/<uuid>/<filename>"
                }
            ]
        }
    ],
    "menus": [
        {
            "id": "number",
            "name": "string",
            "slug": "string",
            "language_code": "string",
            "items": [
                {
                    "id": "number",
                    "title": "string",
                    "url": "string",
                    "target": "string",
                    "page_slug": "string",
                    "css_class": "string",
                    "is_active": "boolean",
                    "position": "number",
                    "children": ["menu item, nested the same way"]
                }
            ],
            "created_at": "datetime",
            "updated_at": "datetime"
        }
    ],
    "forms": [
        {
            "id": "number",
            "name": "string",
            "slug": "string",
            "title": "string",
            "description": "string",
            "success_message": "string",
            "email_to": "string",
            "is_active": "boolean",
            "language_code": "string",
            "translations": {"lang_code": "form_id"},
            "fields": [
                {
                    "type": "string",
                    "name": "string",
                    "label": "string",
                    "placeholder": "string",
                    "help_text": "string",
                    "options": "string",
                    "validation": "string",
                    "is_required": "boolean",
                    "position": "number"
                }
            ],
            "submissions": [
                {
                    "data": "string (JSON-encoded field values)",
                    "ip_address": "string",
                    "user_agent": "string",
                    "is_read": "boolean",
                    "created_at": "datetime"
                }
            ],
            "created_at": "datetime",
            "updated_at": "datetime"
        }
    ],
    "config": {
        "key": "value"
    }
}
```
