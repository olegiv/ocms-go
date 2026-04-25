# Migrator Module

The Migrator module imports content from other CMS platforms into oCMS. It uses a pluggable source architecture — each supported source implements the `types.Source` interface (`Name`, `DisplayName`, `Description`, `ConfigFields`, `TestConnection`, `Import`) and registers itself during module init. Deletion of imported content is handled at the module level via the `migrator_imported_items` tracking table; sources do not implement a `Delete` method.

For the bulk-import/export of *oCMS-to-oCMS* content (Markdown + YAML front-matter), see [`docs/import-export.md`](import-export.md). The Migrator module is for importing from foreign systems.

## Overview

### Currently Supported Sources

| Source | DisplayName | Notes |
|--------|-------------|-------|
| `elefant` | Elefant CMS | MySQL-backed PHP CMS. Imports users, tags, pages, posts, media. |

Add more sources by implementing `migrator/types/types.go:Source` and calling `RegisterSource` from the module's `Init`.

### What gets imported

For the Elefant source the importer walks the source database and copies:

- Users (password hashes not imported — users must reset)
- Tags
- Pages (root pages and subpages)
- Posts (blog posts with categories, tags, publish date)
- Media (files on disk copied into `OCMS_UPLOADS_DIR`, DB rows into `media`)

Every created entity is recorded in `migrator_imported_items` (source, entity_type, entity_id) so the module can later delete the imported subset without touching original oCMS content.

## Admin Interface

Access at **Admin > Migrator** or `/admin/migrator`. The dashboard lists every registered source. Clicking a source opens its configuration form; the form fields come from the source's `ConfigFields()` method. Defaults are read from environment variables (see per-source section below).

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/migrator` | List registered sources |
| GET | `/admin/migrator/{source}` | Source-specific configuration form |
| POST | `/admin/migrator/{source}/test` | Test connection with the submitted config |
| POST | `/admin/migrator/{source}/import` | Run the import |
| POST | `/admin/migrator/{source}/delete` | Delete everything previously imported from that source |

## Elefant Source

Connects to an Elefant CMS MySQL database and copies content in a single pass.

### Configuration fields

| Field | Required | Default (env) |
|-------|----------|---------------|
| MySQL host | yes | `ELEFANT_HOST` (default `localhost`) |
| MySQL port | yes | `ELEFANT_PORT` (default `3306`) |
| MySQL user | yes | `ELEFANT_USER` |
| MySQL password | yes | `ELEFANT_PASSWORD` |
| MySQL database | yes | `ELEFANT_DB` |
| Table prefix | no | `ELEFANT_PREFIX` |
| Files path | no | `ELEFANT_FILES` — absolute path to the Elefant `files/` directory (required to copy media) |

All environment variables are optional — they only pre-fill the form. Submitted values win.

### Test connection

The **Test** button connects to the MySQL database and counts rows in the `{prefix}blog_post` and `{prefix}blog_tag` tables. Any error (connection failure, missing tables, permission denied) is reported back as a flash message.

### Import flow

1. Open the source form, fill in credentials.
2. Click **Test** — confirm green banner.
3. Click **Import**. Progress and per-entity counts appear on completion.
4. Pages, posts, tags, media, and users are inserted. Media files are copied from the Elefant `files/` path into `OCMS_UPLOADS_DIR`.

### Undoing an import

**Admin > Migrator > Elefant CMS > Delete imported items** deletes every entity tracked in `migrator_imported_items` for `source = 'elefant'`. Original oCMS content is not touched.

## Database

Migration 1 creates a single tracking table:

```sql
CREATE TABLE migrator_imported_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_migrator_source ON migrator_imported_items(source);
CREATE INDEX idx_migrator_entity ON migrator_imported_items(source, entity_type);
```

## Adding a New Source

1. Create `modules/migrator/sources/<name>/`.
2. Implement the `migrator.Source` interface (type alias for `types.Source`).
3. Add its `NewSource()` constructor and call `RegisterSource(<name>.NewSource())` from `modules/migrator/module.go` `Init`.
4. Add UI labels to `modules/migrator/locales/en/messages.json` (use the i18n key convention `<source>.field_xxx`).
5. Write tests that exercise `TestConnection` and `Import` against a fixture database.

## Testing

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! \
  go test -v ./modules/migrator/...
```

Covers the source registry, the Elefant reader, full-import, partial-import, and the media-copy paths.
