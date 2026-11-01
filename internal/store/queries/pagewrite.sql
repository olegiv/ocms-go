-- name: AdvancePageRevision :one
UPDATE pages SET revision = revision + 1
WHERE id = ? AND revision = ? RETURNING *;

-- name: GetPageAIAccess :one
SELECT * FROM page_ai_access WHERE page_id = ?;

-- name: SetPageAIAccess :exec
INSERT INTO page_ai_access (page_id, ai_editable, set_by, set_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(page_id) DO UPDATE SET
ai_editable = excluded.ai_editable, set_by = excluded.set_by, set_at = excluded.set_at;

-- name: CreateCompletePageVersion :one
INSERT INTO page_versions
(page_id, title, body, changed_by, created_at, revision, snapshot_json, legacy)
VALUES (?, ?, ?, ?, ?, ?, ?, 0) RETURNING *;

-- name: GetPageVersionByRevision :one
SELECT * FROM page_versions WHERE page_id = ? AND revision = ? AND legacy = 0;

-- name: CreateContentProvenance :exec
INSERT INTO content_provenance
(page_id, revision, user_id, source, api_key_id, oauth_grant_id, oauth_client_id,
 transport, validated_json, reported_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetContentProvenance :one
SELECT * FROM content_provenance WHERE page_id = ? AND revision = ?;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE user_id = ? AND tool = ? AND request_id = ?;

-- name: CreateIdempotencyKey :exec
INSERT INTO idempotency_keys
(user_id, tool, request_id, request_hash, result_json, entity_type, entity_id,
 entity_revision, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CompactIdempotencyKeys :exec
UPDATE idempotency_keys SET result_json = NULL WHERE created_at <= ? AND result_json IS NOT NULL;

-- name: CompactPageIdempotencyKeys :exec
UPDATE idempotency_keys SET result_json = NULL WHERE entity_type = 'page' AND entity_id = ?;

-- name: ListPageIDsForMedia :many
SELECT id FROM pages WHERE featured_image_id = ? OR og_image_id = ? ORDER BY id;

-- name: ListPageIDsForCategory :many
SELECT page_id FROM page_categories WHERE category_id = ? ORDER BY page_id;

-- name: ListPageIDsForTag :many
SELECT page_id FROM page_tags WHERE tag_id = ? ORDER BY page_id;

-- name: ListPageIDsByLanguage :many
SELECT id FROM pages WHERE language_code = ? ORDER BY id;

-- name: GetPageAliasByID :one
SELECT * FROM page_aliases WHERE id = ?;

-- name: GetPageSnapshot :one
WITH RECURSIVE members(id) AS (SELECT id FROM pages WHERE id = sqlc.arg(page_id) UNION SELECT CASE WHEN t.entity_id = members.id THEN t.translation_id ELSE t.entity_id END FROM translations t JOIN members ON t.entity_id = members.id OR t.translation_id = members.id WHERE t.entity_type = 'page')
SELECT CAST(json_object(
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
    'translations', json((SELECT json_group_array(json_object('id', member.id, 'language_code', member.language_code)) FROM (SELECT sibling.id, sibling.language_code FROM pages sibling JOIN members ON members.id = sibling.id ORDER BY sibling.id) member))
) AS TEXT) AS snapshot FROM pages p WHERE p.id = sqlc.arg(page_id);
