-- +goose Up
ALTER TABLE pages ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1);
ALTER TABLE page_versions ADD COLUMN revision INTEGER;
ALTER TABLE page_versions ADD COLUMN snapshot_json TEXT;
ALTER TABLE page_versions ADD COLUMN legacy BOOLEAN NOT NULL DEFAULT 1;
CREATE UNIQUE INDEX idx_page_versions_revision ON page_versions(page_id, revision) WHERE revision IS NOT NULL;

CREATE TABLE page_ai_access (
    page_id INTEGER PRIMARY KEY REFERENCES pages(id) ON DELETE CASCADE,
    ai_editable BOOLEAN NOT NULL DEFAULT 0,
    set_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    set_at DATETIME NOT NULL
);
CREATE TABLE content_provenance (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    source TEXT NOT NULL,
    api_key_id INTEGER,
    oauth_grant_id TEXT NOT NULL DEFAULT '',
    oauth_client_id TEXT NOT NULL DEFAULT '',
    transport TEXT NOT NULL DEFAULT '',
    validated_json TEXT NOT NULL DEFAULT '{}',
    reported_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL,
    UNIQUE (page_id, revision)
);
CREATE TABLE idempotency_keys (
    user_id INTEGER NOT NULL,
    tool TEXT NOT NULL,
    request_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    result_json TEXT,
    entity_type TEXT NOT NULL,
    entity_id INTEGER NOT NULL,
    entity_revision INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (user_id, tool, request_id)
);
CREATE INDEX idx_idempotency_created_at ON idempotency_keys(created_at) WHERE result_json IS NOT NULL;
CREATE INDEX idx_idempotency_entity ON idempotency_keys(entity_type, entity_id);

-- Seed a complete revision of current state. Historical title/body-only rows
-- remain legacy; their missing fields cannot be reconstructed honestly.
INSERT INTO page_versions (page_id, title, body, changed_by, created_at, revision, snapshot_json, legacy)
SELECT p.id, p.title, p.body, p.author_id, CURRENT_TIMESTAMP, 1,
CAST(json_object(
    'id', p.id,
    'title', p.title,
    'slug', p.slug,
    'body', p.body,
    'summary', p.summary,
    'status', p.status,
    'author_id', p.author_id,
    'featured_image_id', p.featured_image_id,
    'meta_title', p.meta_title,
    'meta_description', p.meta_description,
    'meta_keywords', p.meta_keywords,
    'og_image_id', p.og_image_id,
    'no_index', p.no_index,
    'no_follow', p.no_follow,
    'canonical_url', p.canonical_url,
    'scheduled_at', p.scheduled_at,
    'language_code', p.language_code,
    'hide_featured_image', p.hide_featured_image,
    'page_type', p.page_type,
    'exclude_from_lists', p.exclude_from_lists,
    'published_at', p.published_at,
    'created_at', p.created_at,
    'updated_at', p.updated_at,
    'video_url', p.video_url,
    'video_title', p.video_title,
    'revision', p.revision,
    'category_ids', json((SELECT json_group_array(category_id) FROM (SELECT category_id FROM page_categories WHERE page_id = p.id ORDER BY category_id))),
    'tag_ids', json((SELECT json_group_array(tag_id) FROM (SELECT tag_id FROM page_tags WHERE page_id = p.id ORDER BY tag_id))),
    'aliases', json((SELECT json_group_array(alias) FROM (SELECT alias FROM page_aliases WHERE page_id = p.id ORDER BY alias))),
    'ai_editable', COALESCE((SELECT ai_editable FROM page_ai_access WHERE page_id = p.id), 0),
    'translations', json((WITH RECURSIVE members(id) AS (SELECT p.id UNION SELECT CASE WHEN t.entity_id = members.id THEN t.translation_id ELSE t.entity_id END FROM translations t JOIN members ON t.entity_id = members.id OR t.translation_id = members.id WHERE t.entity_type = 'page') SELECT json_group_array(json_object('id', member.id, 'language_code', member.language_code)) FROM (SELECT sibling.id, sibling.language_code FROM pages sibling JOIN members ON members.id = sibling.id ORDER BY sibling.id) member))
) AS TEXT), 0 FROM pages p;
INSERT INTO content_provenance (page_id, revision, source, created_at)
SELECT id, 1, 'migration', CURRENT_TIMESTAMP FROM pages;

-- +goose Down
DROP TABLE idempotency_keys;
DROP TABLE content_provenance;
DROP TABLE page_ai_access;
DELETE FROM page_versions WHERE legacy = 0;
DROP INDEX idx_page_versions_revision;
ALTER TABLE page_versions DROP COLUMN legacy;
ALTER TABLE page_versions DROP COLUMN snapshot_json;
ALTER TABLE page_versions DROP COLUMN revision;
ALTER TABLE pages DROP COLUMN revision;
