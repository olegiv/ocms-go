# Migrator Module

The Migrator module imports content from other CMS platforms into oCMS. It uses a pluggable source architecture — each supported source implements the `types.Source` interface (`Name`, `DisplayName`, `Description`, `ConfigFields`, `TestConnection`, `Import`) and registers itself during module init. Deletion of imported content is handled at the module level via the `migrator_imported_items` tracking table; sources do not implement a `Delete` method.

For the bulk-import/export of *oCMS-to-oCMS* content (Markdown + YAML front-matter), see [`docs/import-export.md`](import-export.md). The Migrator module is for importing from foreign systems.

## Overview

### Currently Supported Sources

| Source | DisplayName | Notes |
|--------|-------------|-------|
| `elefant` | Elefant CMS | MySQL-backed PHP CMS. Imports users, tags, pages, posts, media. |
| `drupal` | Drupal | Drupal 8/9/10/11 MySQL. Imports users, taxonomy (tags + categories), media, nodes, URL aliases/redirects, menus. |
| `phpnuke` | PHP-Nuke | PHP-Nuke MySQL. Imports story authors, topics/categories, media, news stories, static pages, encyclopedias. |

Add more sources by implementing `migrator/types/types.go:Source` and calling `RegisterSource` from the module's `Init`.

### Access control

All migrator admin routes are wrapped in `middleware.RequireAdmin()`. The module takes source database credentials and a filesystem path as input and writes directly to core content tables, so it is admin-only as defence in depth beyond the editor-level module admin group — the same treatment `modules/dbmanager` applies. **Editors cannot reach `/admin/migrator`.**

### Entity types

Everything the module can create is declared once in `types.AllEntityTypes`, in dependency-safe deletion order:

```
menu_item, menu, redirect, alias, post, page, tag, category, media, user
```

That order is dictated by the schema's foreign keys — `pages.author_id` is `ON DELETE RESTRICT`, so pages must be deleted before their authors. Page aliases and generic redirects are both explicitly deleted by their tracked IDs; the alias foreign key's `ON DELETE CASCADE` remains a fallback when its page is removed first. Adding an entity type means adding it to that list, to `deleters()`, and to the locale files; drift tests fail if any of the three is missed.

### Import options per source

Not every source can import every entity class. The import form renders only the options the selected source actually acts on:

| Option | Elefant | Drupal | PHP-Nuke |
|--------|:-------:|:------:|:--------:|
| `import_tags` | ✅ | ✅ | ✅ |
| `import_categories` | — | ✅ | ✅ |
| `import_media` | ✅ | ✅ | ✅ |
| `import_posts` | ✅ | ✅ | ✅ |
| `import_pages` | ✅ | ✅ | ✅ |
| `import_menus` | — | ✅ | — |
| `import_users` | ✅ | ✅ | ✅ |
| `skip_existing` | ✅ | ✅ | ✅ |

Elefant has no vocabulary or navigation tables to read, so categories and menus are absent. PHP-Nuke keeps site navigation in `blocks` as PHP snippets rather than data, so it has no menus to read either. They used to be offered anyway and checked by default, which made every Elefant run promise both, import neither, and still report **Completed**.

A source declares its capabilities through the optional `types.OptionSupporter` interface:

```go
func (s *Source) SupportedImportOptions() []string {
	return []string{"import_tags", "import_media", "import_posts",
		"import_pages", "import_users", "skip_existing"}
}
```

It is optional in the same way and for the same reason as `types.ProgressReporter`: a source that does not implement it is treated as supporting everything, so existing and out-of-tree sources keep working unchanged. Options a source does not declare are cleared server-side in `handleImport` as well as omitted from the form, so a hand-crafted POST cannot record an option the source never read on the job row.

`TestSourcesDeclareTheOptionsTheyRead` AST-walks each source package for `opts.<Field>` reads and fails in both directions — an option offered but never read, or read but not declared.

## Background imports

An import runs as a **detached background job**, not inline in the request.

The router installs a 30-second request timeout (`middleware.Timeout` in `cmd/ocms/main.go`) that replaces `r.Context()`. Running an import inline meant that any real-sized site was cancelled part-way through and answered with a 503 while leaving half-written content behind. `POST /admin/migrator/{source}/import` now:

1. Inserts a `migrator_import_jobs` row (rejecting a second concurrent import for the same source).
2. Starts the import on a context derived with `context.WithoutCancel`, so neither the request deadline nor a closed browser tab can kill it.
3. Redirects immediately with an "import started" flash.

Progress is written to the job row on a one-second ticker and rendered by a status card that htmx polls every two seconds. Polling stops without any JavaScript: the terminal fragment is the same element without `hx-trigger`, and `hx-swap="outerHTML"` replaces the poller with it.

The poll refreshes **two** regions. The Imported Content card sits outside the status card, so the status response also returns it carrying `hx-swap-oob="true"`; one poll updates both. Without that, a finished import still showed pre-import counts — and on a first import, no Delete button at all — until the operator refreshed by hand. The page copy of that card must carry no `hx-swap-oob` attribute at all, since htmx treats any present value as out-of-band.

While a job is running the card also offers a plain **Refresh** link. A frozen card and a slow import look identical, so this gives the operator a way to check without knowing to reload.

The status endpoint deliberately does not build a full page context: `BuildPageContext` pops the flash out of the session, and since the fragment never renders the alert, a two-second poll would swallow every flash message.

**Verifying changes to this card requires a browser.** `curl` and `httptest` neither enforce Subresource Integrity nor execute JavaScript, so a fragment with correct `hx-*` attributes passes every HTTP-level check while doing nothing at all — which is exactly how a stale htmx integrity hash disabled this card, and every other htmx feature in the admin UI, unnoticed for weeks. `internal/views/admin/sri_test.go` now guards the hashes mechanically.

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

The **Test** button connects to the MySQL database and counts rows in the `{prefix}blog_post` and `{prefix}blog_tag` tables. Built-in sources implement the optional `ContextConnectionTester` capability, so request cancellation and the router deadline stop the test without a late flash or redirect. The original `Source.TestConnection` method remains supported for third-party sources.

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
| Public files URL | no | `DRUPAL_PUBLIC_FILES_URL` — the site's public files URL prefix when it is not `/sites/<site>/files/`. Drupal's `file_public_path` lives in `settings.php`, not the database, so nothing in the source can reveal it; `/sites/<x>/files/` and `/system/files/` are detected automatically. |
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
| Nodes | `node_field_data` |
| Node fields | `node__body` plus discovered `node__field_*` image and taxonomy-reference tables |
| Taxonomy | `taxonomy_term_field_data`, `taxonomy_term__parent` |
| Files / media | `file_managed`, `media`, `media_field_data`, discovered `media__field_*` source tables |
| Users | `users_field_data` |
| URL aliases | Drupal 8 `url_alias` or Drupal 9+ `path_alias` (detected at runtime) |
| Menus | `menu_link_content`, `menu_link_content_data` |

Only `node_field_data` is required. Every other table is optional: the reader detects what exists and reports what it skipped in the import result, so an install without an image field or without menus still imports cleanly.

### What gets imported

Stage order is forced by referential integrity and by body rewriting:

1. **Users** — `users_field_data` where `uid > 0`. Every account lands as `RolePublic`; Drupal's `administrator`/`content_editor` roles are deliberately **not** honoured, since importing a foreign system's admin would be a privilege-escalation footgun. Drupal's phpass hashes cannot be verified by oCMS's Argon2id verifier, so all imported accounts share one placeholder hash and must use "forgot password".
2. **Taxonomy and taxonomy redirects** — terms become tags or categories per the vocabulary setting. Each term keeps its source `langcode` when that language is active in oCMS; neutral or unknown codes fall back to the default and are reported separately from node fallback. Same-slug source terms remain distinct, while only pre-existing same-language terms may be reused. Category parents are linked in a second pass from the source-language, current-revision, active delta-zero row, so a child term appearing before its parent still gets linked. Page associations are read from every node reference field **whose storage config declares `taxonomy_term` as its target type**, read from Drupal's `config` table and matched against the `node__field_*` tables present. Reading only `node__field_tags` meant a vocabulary referenced through `field_category` — the common case — imported its terms and then had no page associations at all. The target type has to come from configuration: `node__field_image` is shape-identical to `node__field_category`, and Drupal allocates file, media, user and term IDs from independent sequences, so a node referencing file 7 would otherwise be tagged with term 7. If the `config` table cannot be read the importer falls back to `field_tags` alone and says so — importing fewer associations is recoverable, importing wrong ones is not. Taxonomy aliases become tracked generic redirects to language-aware term URLs even when page import is disabled; non-default source aliases include their language prefix because redirect matching precedes language routing. They never replace a page or alias, a fixed or registered module route, or an exact or matching wildcard redirect. The module context exposes only a narrow `RedirectCacheInvalidator`, which makes newly imported redirects effective immediately and removes them immediately after import deletion.
3. **Media** — driven by `file_managed`, not a directory walk. Alt text and media source-field references are restricted to the owning entity's source-language, current revision, active delta-zero rows. Drupal stores alt text per image-field reference while oCMS stores one value per media row, so a shared file can have several descriptions. The importer chooses deterministically: media-entity fields take precedence over classic node image fields, then the lowest owning entity ID wins, with revision, language, and text as stable tie-breakers. Identical and empty values are ignored; distinct discarded alternatives and their source owners are reported in a bounded import summary. The importer maps both file UUIDs and media-entity UUIDs, which lets `<drupal-media>` embeds resolve correctly. Media source fields are discovered from `media.type.*` configuration, so a bundle using a custom field such as `field_photo` resolves too. `public://` resolves against the files path; `private://` and `temporary://` are reported and skipped.
4. **Nodes** — become pages or posts. The body is converted according to its Drupal **text format**: `plain_text` is escaped and paragraphed rather than parsed as markup (so `2 < 3` survives and a literal `<script>` example is displayed, not deleted), core's HTML formats pass through, and a contrib format such as Markdown is imported as-is and reported in the job summary. The slug comes from the node's `path_alias` when it has one, so existing URLs carry over; otherwise it is derived from the title, then de-duplicated. De-duplication checks page slugs **and** existing aliases: the frontend resolves a slug before falling back to the alias table, so a slug that shadows another page's alias would silently hijack that URL.
5. **Node URL aliases** — node aliases preserve their concrete language namespace. The default-language owner uses `page_aliases` when the global alias table can represent it safely; non-default owners and cross-language storage collisions use tracked concrete redirects such as `/fr/about`. This lets two source languages retain the same bare Drupal alias without one shadowing the other. Single-segment aliases are reserved per destination language before page slug allocation, and destination pages, aliases, fixed/module routes, and exact or matching wildcard redirects are checked before any alias or slug is claimed. Each imported page additionally gets a concrete route for **`node/<nid>`**, because bodies, menus and inbound links routinely point at `/node/42`. Multi-segment, mixed-case, and non-ASCII paths survive; schemes, query/fragment-bearing paths, traversal, and whitespace are reported and rejected. Only aliases belonging to the entity's resolved language (including correctly attributed neutral `und`/`zxx` rows) are used.
6. **Menus** — `menu_link_content` is partitioned by **(`menu_name`, destination language)**. Menu reuse and suffix probing are scoped to that language, and every reused slug is claimed so distinct source menus cannot merge after normalization. `link__uri` is resolved: node and taxonomy entity links and their path aliases point at the imported page or language-aware term URL, `internal:/path` and `base:` become local URLs, every scheme the oCMS menu validator accepts stays external (`http`, `https`, `mailto`, `tel` — the list lives once in `model.AllowedMenuURLSchemes`), and `route:` is reported as unsupported. Hierarchy is applied in a second pass.
7. **Search index** rebuild, then cache invalidation.

Per-item failures are appended to the result and the import continues; only connection, author, and language failures abort.

### Errors versus notices

The result separates two kinds of message, because conflating them made a healthy import look broken — a stock site with no body field and some translations reported "3 errors" having done exactly what it was asked to.

- **Errors** are things that failed but should have worked: a row that could not be created, a table that exists but could not be read.
- **Notices** are expected, informational outcomes: an optional source table the site does not have, content deliberately out of scope, a link form with no oCMS equivalent, a `private://` file.

Errors are logged at error level and shown in a red block; notices are logged at info level and shown in a muted block. `TestImportMessagesAreClassifiedCorrectly` enforces the split over the source text, so a new message cannot quietly land in the wrong bucket.

### Body HTML

Drupal bodies are rewritten before storage, in this order:

1. `<drupal-media>` / `<drupal-entity>` embeds resolve to `<img>` (or a download link) via the file/media UUID map.
2. `/sites/default/files/…` and `/system/files/…` URLs are rewritten to their new `/uploads/…` URLs.
3. The result goes through `security.SanitizePageHTML` — always, not gated on `OCMS_SANITIZE_PAGE_HTML`.

The order matters: the sanitizer strips unknown custom elements, so resolving embeds after it would silently drop every embedded image.

Note that the bluemonday UGC policy strips `<iframe>` and `<script>`, so embedded video and third-party widgets in Drupal bodies will not survive the import.

### Languages

Drupal's `default_langcode = 1` marks each *entity's* source translation, not the site's one default language, so a multilingual site's nodes and taxonomy terms arrive in several languages and are all "default" in Drupal's sense. Each imported entity takes its own `langcode` when oCMS has a matching active language; `und`/`zxx` and any language oCMS does not have fall back to the default and are reported in the job summary. Node and taxonomy fallback are reported independently so a taxonomy-only run remains auditable.

### Multi-language

Only each entity's source/original row (`default_langcode = 1`) is imported. Those rows can have different `langcode` values, so a multilingual site imports source entities in every matching active oCMS language. Drupal's additional translated rows are not imported automatically; the number skipped is reported.

### Not imported

Drupal blocks, views, unsupported custom field types, historical revisions, comments, and non-source translation rows. Taxonomy-reference fields and media source fields are discovered from Drupal configuration rather than limited to hard-coded field names.

## PHP-Nuke Source

Imports a PHP-Nuke news site: `stories`, `pages`, `encyclopedia`, `topics`, and the accounts credited on a story.

### Configuration fields

| Field | Required | Env default | Notes |
|-------|:--------:|-------------|-------|
| `mysql_host` | ✅ | `PHPNUKE_HOST` | |
| `mysql_port` | ✅ | `PHPNUKE_PORT` | |
| `mysql_user` | ✅ | `PHPNUKE_USER` | |
| `mysql_password` | ✅ | `PHPNUKE_PASSWORD` | |
| `mysql_database` | ✅ | `PHPNUKE_DB` | |
| `table_prefix` | — | `PHPNUKE_PREFIX` | Defaults to `nuke_`, but the prefix is chosen at install time and is frequently site-specific. |
| `files_path` | — | `PHPNUKE_FILES` | The **document root**, not an uploads directory — story bodies reference images relative to the site root. |
| `language_code` | — | `PHPNUKE_LANGUAGE` | Target oCMS language. Unset uses the site default. |

### Legacy character sets

This is the failure mode to know about. PHP-Nuke predates UTF-8 defaults, so a non-English install almost always stores text in a single-byte charset — `cp1251` for Russian, `latin2` for Central European, and so on — declared per table.

Nothing special is needed to read it, because `shared.BuildMySQLDSN` pins the connection charset to `utf8mb4` and MySQL transcodes on the way out. What matters is that the guarantee is easy to lose: if the connection instead negotiates the server default, every non-ASCII character arrives as a literal `?` and **no error is raised anywhere** — the import reports Completed having silently destroyed the text. `TestDSNRequestsUTF8MB4` exists to catch exactly that regression.

Verifying by hand needs the same care. The `mysql` CLI's `--default-character-set` flag is ignored by some 5.x servers, which then negotiate `latin1` and print `?`; use an explicit `SET NAMES utf8mb4` instead before concluding the data is corrupt.

### Source tables read

`stories`, `stories_cat`, `topics`, `pages`, `pages_categories`, `encyclopedia`, `encyclopedia_text`, `users`.

### What gets imported

- **Stories → posts.** PHP-Nuke splits an article across `hometext` (the front-page teaser) and `bodytext` (the rest); both are concatenated, or the article is truncated. The teaser also becomes the page summary. `time` becomes both `created_at` and `published_at`.
- **Topics → categories**, **story categories → tags.** A story carries both, so mapping them to different oCMS taxonomies keeps each one rather than letting one shadow the other. A category whose slug already exists is reused, not duplicated, so re-running does not create `hotels-2`.
- **Static pages → pages.** `page_header`, `text`, `page_footer` and `signature` are concatenated in render order. `active = 0` imports as a draft.
- **Encyclopedias → one page each**, with their terms rendered as a definition list. PHP-Nuke serves each term from its own query-string URL, which has no oCMS equivalent; collapsing them keeps the content and its ordering instead of creating hundreds of unreachable pages.
- **Story authors → users.** Only accounts credited on a story (`stories.aid` or `stories.informant`) are imported — a long-lived PHP-Nuke `users` table is mostly dormant registrations. Posts are attributed to the submitter when it resolves, then the publishing admin, then the fallback author.
- **Media.** Only files a body actually references are imported, resolved beneath `files_path`. A PHP-Nuke document root also holds theme furniture, banner creatives and smilies; walking it wholesale would fill the library with files no content mentions. Missing files are reported as a job summary and their markup is left untouched.

### URL preservation

None, deliberately. PHP-Nuke serves content from query strings such as `modules.php?name=News&file=article&sid=12`, which cannot be expressed as a page alias. Minting an alias like `article-12` would invent a URL the source site never served, so preserving the old links is left to the reverse proxy.

### Not imported

phpBB forums (`bb*`), the 4nAlbum photo gallery, comments, links, downloads, FAQs, journals, blocks, banners, polls, and the analytics/statistics tables (`msanalysis_*`, `stats_*`, `nsnst_*`).

## Undoing an import

**Admin > Migrator > *source* > Delete imported items** deletes every entity tracked in `migrator_imported_items` for that source. Original oCMS content is not touched.

The whole delete runs in **one transaction**, and that is what serializes it against a starting import. oCMS opens SQLite with `_txlock=immediate`, so the write lock is taken at `BEGIN` and the delete's transaction cannot interleave with `startJob`'s. The handler checks for a running job too, but only to produce a good error message — on its own it is a read that an import can slip past. Filesystem removal happens after commit, but every deleted media UUID is first written to `migrator_media_cleanup_queue` in that same transaction; a queue-write failure rolls the database deletion back. Failed removals remain queued across restarts, are retried during initialization and later delete attempts, and remain visible in imported-media counts with a warning. An item whose database delete is refused keeps its tracking row so a retry can still find it. Any exact imported dependency of that failed row — for example its page-backed menu target, taxonomy URL target, or body-embedded media — stays tracked too; unrelated rows do not.

Several things are deliberately **not** deleted:

- **A menu that already existed in oCMS.** The importer tracks only the menu *items* it added, never the menu itself, so deleting the import cannot destroy a menu you built by hand.
- **A tag, category or media item original content has started using.** `page_tags.tag_id` and `page_categories.category_id` are `ON DELETE CASCADE`, and `pages.featured_image_id` / `og_image_id` are `ON DELETE SET NULL`, so removing an imported term would silently strip that association from a page the import does not own. Media embedded in a page body through `/uploads/<variant>/<uuid>/...` is protected for the same reason. By the time taxonomy is deleted every imported page is already gone, so any remaining reference necessarily belongs to content this import does not own — the row is kept and merely untracked. A failure to count references keeps the row too: an extra entity the operator can delete by hand is a smaller harm than content destroyed with no undo.
- **A menu the import created that still holds an administrator's item.** `menu_items.menu_id` is `ON DELETE CASCADE`, and an item cannot be moved out of a menu — `menu_id` is `NOT NULL` — so the menu is kept and untracked instead.
- **A user who still owns content.** `pages.author_id` and `page_versions.changed_by` are `ON DELETE RESTRICT`, and `media.uploaded_by` carries no action clause, which enforces the same way — so a user owning any of them cannot be deleted at all. Two imports sharing an email is the ordinary way to reach this: the second reuses the account the first created.
- **A menu item an administrator pointed at an imported page.** `menu_items.page_id` is `ON DELETE SET NULL` and a page-backed item stores no fallback URL, so the item would be left with an empty destination. Successfully deleted tracked menu items are gone before pages are considered, so anything else still pointing at one belongs to the administrator: it is given the page's URL. If deletion of a tracked page-backed item failed, the exact target page is retained and remains tracked for the same retry instead of being detached into a dead URL.
- **A menu item an administrator hung off an imported one.** `menu_items.parent_id` is `ON DELETE CASCADE`, so deleting the imported parent would take it too. Children are lifted to the imported item's own parent and the item is then deleted — preserving the item instead would leave an imported entry in the navigation permanently, which is exactly what the operator asked to clear.

Deleting imported media uses the centralized `imaging.DeleteMediaFiles` helper. It validates the canonical UUID, derives every possible directory from `model.MediaStorageDirs()`, attempts all removals, and returns their joined errors. `TestNoHardcodedVariantLists` rejects hand-written variant lists, so adding a storage variant extends cleanup without another source-specific copy.

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
        CHECK (status IN ('running','completed','failed','partial','interrupted')),
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

Migration 3 adds a `notices` column to the job table, keeping informational messages apart from failures. Migration 4 adds the `skipped` counter; migration 5 transactionally rebuilds the job-status constraint to support `partial` and recovers interrupted rebuild state before retrying.

Migration 6 creates the durable media cleanup queue:

```sql
CREATE TABLE migrator_media_cleanup_queue (
    source TEXT NOT NULL,
    upload_root TEXT NOT NULL,
    media_uuid TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source, upload_root, media_uuid)
);
CREATE INDEX idx_migrator_media_cleanup_source
    ON migrator_media_cleanup_queue(source);
```

No credentials are ever written to any migrator table. History is trimmed to the newest 20 terminal jobs per source.

## Security notes

- **Source database host.** `OCMS_MIGRATOR_ALLOWED_DB_HOSTS` optionally restricts which hosts a source may connect to (comma-separated bare hostnames or IPs; no scheme, no port — an entry carrying a port is rejected at parse time rather than stored as a key nothing can match, and the same rule is applied to the submitted host, so the two cannot disagree; a bare IPv6 literal such as `::1` or `[::1]` is still accepted). It is an allowlist rather than the private-IP denylist used for webhooks, because a CMS database being migrated from almost always lives on a private address — denying RFC1918 would break the feature's main use case. An empty value means no restriction; routes are admin-only regardless. In production, `OCMS_REQUIRE_MIGRATOR_ALLOWED_DB_HOSTS` defaults to `true` and refuses startup when the module is active with an empty allowlist; set it to `false` to opt out deliberately.
- **DSNs are never built by string formatting.** Every source must go through `shared.BuildMySQLDSN`, which assembles the DSN with `mysql.Config.FormatDSN` and enforces the host allowlist. This is not cosmetic: raw interpolation let a submitted database name such as `db?allowAllFiles=true` inject driver parameters and turn on the driver's `LOCAL INFILE` handling, which lets a hostile MySQL server read arbitrary files readable by the oCMS process. `TestNoSourceBuildsDSNByStringFormatting` fails the build if a source reintroduces hand-built DSNs.
- **Host values are shape-checked** before being placed in the DSN. `FormatDSN` writes the address verbatim, so a host containing `@`, `(`, `/` or whitespace could otherwise re-parse as a different protocol (for example a `unix` socket).
- **Table prefixes** are sanitized to `[A-Za-z0-9_]{0,20}` before being interpolated into SQL. Every other value is a bound parameter.
- **Source database passwords are never persisted.** The form config round-trips through the session so a failed connection test does not clear the form, but `withoutSecrets` strips every password-typed field first — the session store is SQLite-backed and gob-encoded, not encrypted. The password input is therefore rendered without a `value` attribute and must be re-entered.
- **Every source database call is context-aware**, so an import honours both shutdown and the six-hour job deadline, and a black-holed host cannot pin a goroutine and socket for the OS TCP timeout.
- **Local file access is allowlisted.** `OCMS_MIGRATOR_ALLOWED_FILE_ROOTS` accepts comma-separated absolute roots; non-empty `DRUPAL_FILES` and `ELEFANT_FILES` values are trusted roots too. Configured roots must exist and be directories. Safe root symlinks are canonicalized, while any root resolving to the filesystem root is rejected. A submitted Files path must equal or descend from one of them both lexically and after symlink resolution. With no trusted root, local-media import fails closed while database/content-only migration remains available.
- **Source files are capability-scoped.** After validation, scanning and opening use relative paths through `os.Root`; raw form values never reach filesystem operations. Symlink escapes, sibling-prefix paths and traversal components are rejected. Writes sanitize filenames and validate destination containment, and partial output is removed after seek, copy, close, processing, media-row, or tracking failures.
- **Cleanup is retryable and bounded.** UUID directory removal validates canonical UUIDs, derives every storage directory from `model.MediaStorageDirs`, attempts them all and returns joined errors. Sources use the optional `MediaCleanupQueuer` capability when immediate compensation fails.
- **Tracking is compensating.** A created entity is not published into source maps, counters, or live progress until its tracking insert succeeds. A failed insert triggers bounded rollback of the row and files; a failed file rollback is placed on the durable cleanup queue.
- **Only an allowlist of MIME types** is importable (JPEG, PNG, GIF, WebP, PDF, MP4, WebM).
- **Credentials are never logged.** The import logs the source, the acting user, and the option flags only; the config round-trips through the session, not the URL.
- **A panic inside a source is recovered** by the job goroutine — chi's `Recoverer` middleware does not reach it, so without that the server would go down.

## Adding a New Source

1. Create `modules/migrator/sources/<name>/`.
2. Implement the `migrator.Source` interface (type alias for `types.Source`).
3. Reuse `modules/migrator/sources/shared` for prefix sanitizing, upload-dir resolution, slug de-duplication, MIME detection and hardened file access. Keep a `shared.MediaRoot` open and call `Open(file.Path)`; do not open the compatibility `FullPath` directly.
4. Add its `NewSource()` constructor and call `RegisterSource(<name>.NewSource())` from `modules/migrator/module.go` `Init`.
5. Add UI labels to **both** `locales/en/messages.json` and `locales/ru/messages.json` (key convention `<source>.field_xxx`, `<source>.placeholder_xxx`, `<source>.description`).
6. Take a narrow reader interface in the import stages rather than a concrete type, so the stages can be tested against an in-memory fake instead of a live database.
7. Track every created entity with `tracker.TrackImportedItem` using a `types.Entity*` constant. Publish maps and counters only after tracking succeeds, and compensate the new row and files if it fails.

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

# PHP-Nuke
OCMS_MIGRATOR_ALLOWED_DB_HOSTS=127.0.0.1 \
PHPNUKE_TEST_HOST=127.0.0.1 PHPNUKE_TEST_PORT=3306 \
PHPNUKE_TEST_USER=nuke PHPNUKE_TEST_PASSWORD=nuke PHPNUKE_TEST_DB=nuke \
PHPNUKE_TEST_PREFIX=nuke_ PHPNUKE_FILES=/var/www/html \
  go test -tags phpnuke_integration -v ./modules/migrator/sources/phpnuke/
```

The PHP-Nuke live test asserts that imported titles contain real Cyrillic and no `??`, which is the only way to prove the legacy-charset conversion end to end.

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
| `TestImportMessagesAreClassifiedCorrectly` | an expected outcome reported as an error, or a real failure hidden as a notice |
| `TestOptionalTableGettersShortCircuit` | an optional source table queried without its schema guard (nil `*sql.DB` makes the missing guard panic) |
| `TestBuildNodeQueryOmitsBodyJoinWhenAbsent` | the node query joining a body table the site does not have |
