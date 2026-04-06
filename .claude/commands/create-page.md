---
allowed-tools: ""
description: "Create one or more CMS pages via API or direct database insertion"
---

Create one or more pages in the oCMS system. Supports two methods: API (with Bearer token) and direct SQLite database access.

**Parameters:** $ARGUMENTS — Two optional parameters:

1. **`--site-url=<URL>`** — Remote site URL (e.g., `--site-url=https://example.com`). If set, pages are created via the REST API using an API key. If not set (default), pages are created via direct SQLite database access.

2. **Page specification** (everything else in $ARGUMENTS) — Can be:
   - A natural language description of the page(s) to create (title, content, tone, language)
   - A JSON object or array with page fields
   - Empty — you will be prompted for details

**Examples:**
```
/create-page About Us page in Russian, formal tone
/create-page --site-url=https://example.com 3 blog posts about opossums
/create-page {"title": "Test", "slug": "test", "status": "published"}
```

## Method Selection

**CRITICAL: The two methods are mutually exclusive. Never fall back from one to the other. If the chosen method fails, report the error and stop.**

### No `--site-url` (default) → Direct DB only

1. Read `OCMS_DB_PATH` from `.env` to get the SQLite database path
2. Use `sqlite3` to insert pages directly
3. No server needs to be running
4. Do NOT attempt API calls. Do NOT check if the server is running.

### `--site-url` is set → API only

1. The site URL is the base URL for API requests (e.g., `https://example.com/api/v1/pages`)
2. **API key is required.** Ask the user for the API key if not provided. The key must have `pages:write` permission.
3. Test connectivity: `curl -sf "${site_url}/health"` — if unreachable, report error and stop.
4. Create pages via POST requests:
   ```bash
   curl -s -w "\n%{http_code}" -X POST "${site_url}/api/v1/pages" \
     -H "Authorization: Bearer ${api_key}" \
     -H "Content-Type: application/json" \
     -d '{ ... }'
   ```
5. Check response: HTTP 201 = success, otherwise report the error and stop.
6. Do NOT touch the local database. Do NOT fall back to direct DB.

## Page Fields Reference

**Required:**
- `title` (TEXT) — Page title
- `slug` (TEXT, UNIQUE) — URL slug (auto-generate from title if not provided, use transliteration for non-Latin titles)

**Common fields:**
- `body` (TEXT) — HTML content (default: '')
- `status` — 'draft' or 'published' (default: 'draft')
- `page_type` — 'post' or 'page' (default: 'post')
- `language_code` — Must exist in `languages` table. For DB method, query: `SELECT code FROM languages WHERE is_active = 1;`
- `author_id` (INTEGER) — Foreign key to users (DB method only). Query: `SELECT id FROM users LIMIT 1;`
- `exclude_from_lists` — 0 or 1 (default: 0)

**SEO fields:**
- `meta_title` (TEXT, default: '')
- `meta_description` (TEXT, default: '')
- `meta_keywords` (TEXT, default: '')
- `canonical_url` (TEXT, default: '')
- `no_index` (INTEGER, default: 0)
- `no_follow` (INTEGER, default: 0)

**Media fields:**
- `featured_image_id` (INTEGER, nullable) — FK to media table
- `og_image_id` (INTEGER, nullable) — FK to media table
- `hide_featured_image` (INTEGER, default: 0)

**Scheduling:**
- `scheduled_at` (DATETIME, nullable) — For scheduled publishing
- `published_at` (DATETIME) — Auto-set when status='published'

**Other:**
- `summary` (TEXT, default: '')
- `video_url` (TEXT, default: '')
- `video_title` (TEXT, default: '')

## Direct DB Insert Template

```sql
INSERT INTO pages (
  title, slug, body, status, author_id, page_type, language_code,
  meta_title, meta_description, exclude_from_lists,
  published_at, created_at, updated_at
) VALUES (
  'Title', 'slug', '<p>Content</p>', 'published', :author_id, 'page', :lang_code,
  'Meta Title', 'Meta description', 0,
  CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);
```

## API Request Template

```json
{
  "title": "Page Title",
  "slug": "page-slug",
  "body": "<p>Content</p>",
  "status": "published",
  "page_type": "page",
  "language_code": "ru",
  "meta_title": "Meta Title",
  "meta_description": "Meta description",
  "exclude_from_lists": false,
  "tags": ["tag1", "tag2"],
  "category_ids": [1, 2]
}
```

## Taxonomy (Categories & Tags)

**DB method** — After inserting the page, associate categories and tags if requested:

```sql
-- Get the new page ID
SELECT last_insert_rowid();

-- Associate categories
INSERT INTO page_categories (page_id, category_id) VALUES (:page_id, :cat_id);

-- Associate tags (create if needed)
INSERT OR IGNORE INTO tags (name, slug, created_at, updated_at)
VALUES ('tag-name', 'tag-slug', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO page_tags (page_id, tag_id)
VALUES (:page_id, (SELECT id FROM tags WHERE slug = 'tag-slug'));
```

**API method** — Pass `tags` (array of tag name strings) and `category_ids` (array of integers) in the request body. The API auto-creates missing tags.

## Steps

### Shared Steps (both methods)

1. Parse `$ARGUMENTS` — extract `--site-url` if present, treat the rest as page specification
2. Generate page content based on the user's requirements (language, tone, structure)

### Direct DB Steps (no `--site-url`)

3. Read `.env` to get `OCMS_DB_PATH`
4. Query active languages: `sqlite3 "$db_path" "SELECT code, name FROM languages WHERE is_active = 1;"`
5. Query first author: `sqlite3 "$db_path" "SELECT id, display_name FROM users LIMIT 1;"`
6. Check for slug conflicts: `sqlite3 "$db_path" "SELECT slug FROM pages WHERE slug IN ('slug1','slug2');"`
7. Also check aliases: `sqlite3 "$db_path" "SELECT alias FROM page_aliases WHERE alias IN ('slug1','slug2');"`
8. Insert each page using `sqlite3` with a heredoc:
   ```bash
   sqlite3 "$db_path" <<'SQL'
   INSERT INTO pages (...) VALUES (...);
   SQL
   ```
9. Verify insertion: `sqlite3 "$db_path" "SELECT id, title, slug, status FROM pages WHERE id > :last_known_id;"`
10. Report created pages with their IDs, slugs, and URLs

### API Steps (with `--site-url`)

3. Ask the user for the API key (Bearer token) if not already provided
4. Test connectivity: `curl -sf "${site_url}/health"`
5. For each page, send a POST request to `${site_url}/api/v1/pages`
6. Parse the JSON response — on HTTP 201, extract the created page data
7. On error (4xx/5xx), report the status code and error message, stop
8. Report created pages with their IDs, slugs, and URLs

## Important Rules

- **Always check slug uniqueness** before inserting (DB method: query `pages` and `page_aliases` tables; API method: the API validates this and returns 409/422 on conflict)
- **Always query available languages** (DB method) — do not hardcode language codes
- **Always query a valid author_id** (DB method) — do not assume user IDs
- **Set `published_at`** to CURRENT_TIMESTAMP when status is 'published' (DB method)
- **Transliterate slugs** for non-Latin titles (e.g., Russian → transliterated slug)
- **Use heredoc** for SQL with HTML content to avoid shell escaping issues
- **Do NOT use Chrome/browser tools** — this command works entirely via CLI
- **Report results** with a table showing ID, title, slug, status for each created page
