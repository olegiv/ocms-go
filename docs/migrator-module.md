# Migrator Module

The Migrator module imports content from other CMS platforms into oCMS. It uses a pluggable source architecture — each supported source implements the `types.Source` interface (`Name`, `DisplayName`, `Description`, `ConfigFields`, `TestConnection`, `Import`) and registers itself during module init. Deletion of imported content is handled at the module level via the `migrator_imported_items` tracking table; sources do not implement a `Delete` method.

For the bulk-import/export of *oCMS-to-oCMS* content (Markdown + YAML front-matter), see [`docs/import-export.md`](import-export.md). The Migrator module is for importing from foreign systems.

## Overview

### Currently Supported Sources

| Source | DisplayName | Notes |
|--------|-------------|-------|
| `elefant` | Elefant CMS | MySQL-backed PHP CMS. Imports users, tags, pages, posts, media. |
| `drupal` | Drupal | Drupal 8/9/10/11 MySQL. Imports users, taxonomy (tags + categories), media, nodes, URL aliases, menus. |

Add more sources by implementing `migrator/types/types.go:Source` and calling `RegisterSource` from the module's `Init`.

### Access control

All migrator admin routes are wrapped in `middleware.RequireAdmin()`. The module takes source database credentials and a filesystem path as input and writes directly to core content tables, so it is admin-only as defence in depth beyond the editor-level module admin group — the same treatment `modules/dbmanager` applies. **Editors cannot reach `/admin/migrator`.**

### Entity types

Everything the module can create is declared once in `types.AllEntityTypes`, in dependency-safe deletion order:

```
menu_item, menu, alias, post, page, tag, category, media, user
```

That order is dictated by the schema's foreign keys — `pages.author_id` is `ON DELETE RESTRICT`, so pages must be deleted before their authors; `page_aliases.page_id` is `ON DELETE CASCADE`, so aliases have no deleter of their own. Adding an entity type means adding it to that list, to `deleters()`, and to the locale files; drift tests fail if any of the three is missed.

## Background imports

An import runs as a **detached background job**, not inline in the request.

The router installs a 30-second request timeout (`middleware.Timeout` in `cmd/ocms/main.go`) that replaces `r.Context()`. Running an import inline meant that any real-sized site was cancelled part-way through and answered with a 503 while leaving half-written content behind. `POST /admin/migrator/{source}/import` now:

1. Inserts a `migrator_import_jobs` row (rejecting a second concurrent import for the same source).
2. Starts the import on a context derived with `context.WithoutCancel`, so neither the request deadline nor a closed browser tab can kill it.
3. Redirects immediately with an "import started" flash.

Progress is written to the job row on a one-second ticker and rendered by a status card that htmx polls every two seconds. Polling stops without any JavaScript: the terminal fragment is the same element without `hx-trigger`, and `hx-swap="outerHTML"` replaces the poller with it.

Because a goroutine cannot outlive its process, a `running` row owned by a different run ID with a stale heartbeat is marked `interrupted` at startup. The `owner_run_id` guard means a second process sharing the database does not kill its sibling's live import.

### Concurrency

A partial unique index — `UNIQUE(source) WHERE status = 'running'` — is the concurrency guard. It survives restarts and multiple processes, which an in-process mutex would not. Starting a second import for the same source flashes `migrator.error_import_running`; different sources may import concurrently.

Imports write per entity and autocommit. Wrapping an import in one transaction would hold SQLite's single write lock for its whole duration and starve every other writer; the tracking table provides the undo path instead.

## Admin Interface

Access at **Admin > Migrator** or `/admin/migrator`. The dashboard lists every registered source. Clicking a source opens its configuration form; the form fields come from the source's `ConfigFields()` method. Defaults are read from environment variables (see per-source section below). Password-typed defaults are never rendered into the HTML.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/migrator` | List registered sources |
| GET | `/admin/migrator/{source}` | Source-specific configuration form |
| GET | `/admin/migrator/{source}/status` | htmx job-status fragment (polled while running) |
| POST | `/admin/migrator/{source}/test` | Test connection with the submitted config |
| POST | `/admin/migrator/{source}/import` | Start a background import |
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

The **Test** button connects to the MySQL database and counts rows in the `{prefix}blog_post` and `{prefix}blog_tag` tables.

## Drupal Source

Connects to a Drupal 8/9/10/11 MySQL database and its public files directory. Verified against Drupal 11.4.x; the reader detects its schema at runtime, so contrib-heavy and older 8/9/10 installs work too.

### Configuration fields

| Field | Required | Default (env) |
|-------|----------|---------------|
| MySQL host | yes | `DRUPAL_HOST` (default `localhost`) |
| MySQL port | yes | `DRUPAL_PORT` (default `3306`) |
| MySQL user | yes | `DRUPAL_USER` |
| MySQL password | yes | `DRUPAL_PASSWORD` |
| MySQL database | yes | `DRUPAL_DB` |
| Table prefix | no | `DRUPAL_PREFIX` |
| Files path | no | `DRUPAL_FILES` — absolute path to `sites/default/files` (required to copy media) |
| Content type mapping | no | `DRUPAL_TYPE_MAP` (default `article:post,page:page`) |
| Tag vocabularies | no | `DRUPAL_TAG_VOCABULARIES` (default `tags`) |

**Content type mapping** maps Drupal node bundles onto the oCMS `page_type`. Bundles you do not list become pages. Malformed entries are ignored rather than failing the import.

**Tag vocabularies** lists the Drupal vocabularies that become flat oCMS *tags*. Every other vocabulary becomes oCMS *categories*, which are hierarchical and preserve Drupal's term parents and weights.

The DSN is assembled with `mysql.Config.FormatDSN()` rather than string formatting, so a password containing `@`, `/`, `:` or `?` is escaped correctly, and connect/read timeouts are always set.

### Test connection

The **Test** button connects, introspects `INFORMATION_SCHEMA`, and logs the node bundles it found plus any optional tables that are missing. Run it before configuring the content type mapping.

### Source tables read

| Purpose | Table(s) |
|---|---|
| Nodes | `node_field_data`, `node__body` |
| Node image / tags | `node__field_image`, `node__field_tags` |
| Taxonomy | `taxonomy_term_field_data`, `taxonomy_term__parent` |
| Files | `file_managed`, `media__field_media_image` |
| Users | `users_field_data` |
| URL aliases | `path_alias` |
| Menus | `menu_link_content`, `menu_link_content_data` |

Only `node_field_data` is required. Every other table is optional: the reader detects what exists and reports what it skipped in the import result, so an install without an image field or without menus still imports cleanly.

### What gets imported

Stage order is forced by referential integrity and by body rewriting:

1. **Users** — `users_field_data` where `uid > 0`. Every account lands as `RolePublic`; Drupal's `administrator`/`content_editor` roles are deliberately **not** honoured, since importing a foreign system's admin would be a privilege-escalation footgun. Drupal's phpass hashes cannot be verified by oCMS's Argon2id verifier, so all imported accounts share one placeholder hash and must use "forgot password".
2. **Taxonomy** — terms become tags or categories per the vocabulary setting. Category parents are linked in a second pass, so a child term appearing before its parent still gets linked.
3. **Media** — driven by `file_managed`, not a directory walk, so only files Drupal knows about are copied and each keeps its UUID (which is what lets `<drupal-media>` embeds resolve later). `public://` resolves against the files path; `private://` and `temporary://` are reported and skipped.
4. **Nodes** — become pages or posts. The slug comes from the node's `path_alias` when it has one, so existing URLs carry over; otherwise it is derived from the title, then de-duplicated.
5. **URL aliases** — every `path_alias` row pointing at an imported node becomes a `page_aliases` entry, so old Drupal URLs keep resolving. An alias identical to the page's own slug is skipped.
6. **Menus** — `menu_link_content` grouped by `menu_name`. `link__uri` is resolved: `entity:node/N` and `internal:/node/N` point at the imported page, `internal:/path` and `base:` become local URLs, `http(s)://` stays external, and `route:` is reported as unsupported. Hierarchy is applied in a second pass.
7. **Search index** rebuild, then cache invalidation.

Per-item failures are appended to the result and the import continues; only connection, author, and language failures abort.

### Body HTML

Drupal bodies are rewritten before storage, in this order:

1. `<drupal-media>` / `<drupal-entity>` embeds resolve to `<img>` (or a download link) via the file UUID map.
2. `/sites/default/files/…` and `/system/files/…` URLs are rewritten to their new `/uploads/…` URLs.
3. The result goes through `security.SanitizePageHTML` — always, not gated on `OCMS_SANITIZE_PAGE_HTML`.

The order matters: the sanitizer strips unknown custom elements, so resolving embeds after it would silently drop every embedded image.

Note that the bluemonday UGC policy strips `<iframe>` and `<script>`, so embedded video and third-party widgets in Drupal bodies will not survive the import.

### Multi-language

Only default-language content is imported (`default_langcode = 1`). Drupal keeps translations as extra rows keyed by `langcode`, while oCMS models a translation as a separate page with its own globally-unique slug, so mapping them automatically would produce slug collisions rather than useful content. The number of skipped translations is reported in the import result.

### Not imported

Drupal blocks, views, custom field types beyond body/image/tags, revisions, comments, and non-default translations.

## Undoing an import

**Admin > Migrator > *source* > Delete imported items** deletes every entity tracked in `migrator_imported_items` for that source. Original oCMS content is not touched.

One subtlety worth knowing: when the importer adds links to a menu that already existed in oCMS, it tracks only the menu *items*, never the menu itself — so deleting the import cannot destroy a menu you built by hand.

## Database

Migration 1 creates the tracking table:

```sql
CREATE TABLE migrator_imported_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Migration 2 creates the job table. Counters live in a single JSON column deliberately: `ImportResult` gains fields as sources learn new entity classes, and a blob avoids a schema migration per counter.

```sql
CREATE TABLE migrator_import_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running','completed','failed','interrupted')),
    phase TEXT NOT NULL DEFAULT '',
    processed INTEGER NOT NULL DEFAULT 0,
    total INTEGER NOT NULL DEFAULT 0,
    counters TEXT NOT NULL DEFAULT '{}',
    options TEXT NOT NULL DEFAULT '{}',
    errors TEXT NOT NULL DEFAULT '[]',
    fatal_error TEXT NOT NULL DEFAULT '',
    started_by INTEGER,
    started_by_email TEXT NOT NULL DEFAULT '',
    owner_run_id TEXT NOT NULL DEFAULT '',
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);
CREATE UNIQUE INDEX idx_migrator_jobs_one_running
    ON migrator_import_jobs(source) WHERE status = 'running';
```

No credentials are ever written to either table. History is trimmed to the newest 20 terminal jobs per source.

## Security notes

- **Source database host.** `OCMS_MIGRATOR_ALLOWED_DB_HOSTS` optionally restricts which hosts a source may connect to (comma-separated bare hostnames or IPs; no scheme, no port). It is an allowlist rather than the private-IP denylist used for webhooks, because a CMS database being migrated from almost always lives on a private address — denying RFC1918 would break the feature's main use case. An empty value means no restriction; routes are admin-only regardless.
- **Table prefixes** are sanitized to `[A-Za-z0-9_]{0,20}` before being interpolated into SQL. Every other value is a bound parameter.
- **The files path** is cleaned, checked for traversal, confirmed to be a directory, and resolved through symlinks; each file is then re-resolved and confirmed to sit inside that root, so a symlink in the source install cannot pull in host files. Writes go through `util.SanitizeFilename` and two `util.ValidatePathWithinBase` checks.
- **Only an allowlist of MIME types** is importable (JPEG, PNG, GIF, WebP, PDF, MP4, WebM).
- **Credentials are never logged.** The import logs the source, the acting user, and the option flags only; the config round-trips through the session, not the URL.
- **A panic inside a source is recovered** by the job goroutine — chi's `Recoverer` middleware does not reach it, so without that the server would go down.

## Adding a New Source

1. Create `modules/migrator/sources/<name>/`.
2. Implement the `migrator.Source` interface (type alias for `types.Source`).
3. Reuse `modules/migrator/sources/shared` for prefix sanitizing, upload-dir resolution, slug de-duplication, MIME detection and the hardened file-copy path — do not copy those into the new package.
4. Add its `NewSource()` constructor and call `RegisterSource(<name>.NewSource())` from `modules/migrator/module.go` `Init`.
5. Add UI labels to **both** `locales/en/messages.json` and `locales/ru/messages.json` (key convention `<source>.field_xxx`, `<source>.placeholder_xxx`, `<source>.description`).
6. Take a narrow reader interface in the import stages rather than a concrete type, so the stages can be tested against an in-memory fake instead of a live database.
7. Track every created entity with `tracker.TrackImportedItem` using a `types.Entity*` constant, so the undo path and the live progress display both work for free.

## Testing

```bash
OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! \
  go test -v ./modules/migrator/...
```

Covers the source registry, both source readers and importers, the job lifecycle, the delete path against a real foreign-key-enforcing database, and the status fragment.

Live-database tests are behind build tags and skip unless their host variable is set:

```bash
# Elefant
go test -tags integration ./modules/migrator/sources/elefant/
go test -tags fullimport  ./modules/migrator/sources/elefant/

# Drupal
DRUPAL_HOST=127.0.0.1 DRUPAL_USER=drupal DRUPAL_PASSWORD=drupal DRUPAL_DB=drupal \
DRUPAL_FILES=/var/www/html/sites/default/files \
  go test -tags drupal_integration -v ./modules/migrator/sources/drupal/
```

### Drift tests

`modules/migrator/drift_test.go` enforces invariants mechanically rather than by review. Each fails on a real bug state:

| Test | Catches |
|---|---|
| `TestTrackedEntityTypesAreDeletable` | a source tracking an entity type the undo path ignores, leaving orphaned content behind a button that reports success |
| `TestDeleteImportedItemsCoversAllEntityTypes` | an entity type with no deleter, or a deletion order that violates the foreign keys |
| `TestImportOptionsHaveFormCheckboxes` | an import option with no checkbox, or a form key wired to the wrong field |
| `TestImportResultCountersCoverAllImportedFields` | a counter that never reaches the totals, the job row, or the stats panel |
| `TestLocaleKeyParity` / `TestLocaleFormatVerbsMatch` | a key or printf verb present in one language but not the other |
| `TestLocaleCoversEntityTypesAndJobStatuses` | an entity type or job status with no label, rendering as a raw i18n key |
| `TestSourceLabelsAreTranslated` | a source shipping an untranslated field label |
| `TestAdminRoutesAreGatedToAdmins` | a route registered outside the `RequireAdmin` group |
