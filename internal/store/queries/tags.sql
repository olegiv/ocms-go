-- name: CreateTag :one
INSERT INTO tags (name, slug, language_code, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTagByID :one
SELECT * FROM tags WHERE id = ?;

-- name: GetTagBySlug :one
SELECT * FROM tags WHERE slug = ?;

-- name: ListTags :many
SELECT * FROM tags ORDER BY name LIMIT ? OFFSET ?;

-- name: ListAllTags :many
SELECT * FROM tags ORDER BY name;

-- name: SearchTags :many
SELECT * FROM tags WHERE name LIKE ? ORDER BY name LIMIT ?;

-- name: UpdateTag :one
UPDATE tags SET name = ?, slug = ?, language_code = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteTag :exec
DELETE FROM tags WHERE id = ?;

-- name: CountTags :one
SELECT COUNT(*) FROM tags;

-- name: TagSlugExists :one
SELECT EXISTS(SELECT 1 FROM tags WHERE slug = ?);

-- name: TagSlugExistsExcluding :one
SELECT EXISTS(SELECT 1 FROM tags WHERE slug = ? AND id != ?);

-- Page-Tag association queries

-- name: AddTagToPage :exec
INSERT OR IGNORE INTO page_tags (page_id, tag_id) VALUES (?, ?);

-- name: RemoveTagFromPage :exec
DELETE FROM page_tags WHERE page_id = ? AND tag_id = ?;

-- name: GetTagsForPage :many
SELECT t.* FROM tags t
INNER JOIN page_tags pt ON pt.tag_id = t.id
WHERE pt.page_id = ?
ORDER BY t.name;

-- name: GetPagesForTag :many
SELECT p.* FROM pages p
INNER JOIN page_tags pt ON pt.page_id = p.id
WHERE pt.tag_id = ?
ORDER BY p.created_at DESC
LIMIT ? OFFSET ?;

-- name: CountPagesForTag :one
SELECT COUNT(*) FROM page_tags WHERE tag_id = ?;

-- name: ClearPageTags :exec
DELETE FROM page_tags WHERE page_id = ?;

-- name: GetTagUsageCounts :many
SELECT t.id, t.name, t.slug, t.language_code, t.created_at, t.updated_at, COUNT(pt.page_id) as usage_count
FROM tags t
LEFT JOIN page_tags pt ON pt.tag_id = t.id
GROUP BY t.id, t.name, t.slug, t.language_code, t.created_at, t.updated_at
ORDER BY usage_count DESC, t.name
LIMIT ? OFFSET ?;

-- name: ListTagsForSitemap :many
SELECT t.id, t.slug, t.updated_at, t.language_code, l.is_default
FROM tags t
INNER JOIN languages l ON l.code = t.language_code AND l.is_active = 1
ORDER BY t.updated_at DESC;

-- Language-specific tag queries (no JOINs needed - language_code is directly on the table)

-- name: ListTagsByLanguage :many
SELECT * FROM tags
WHERE language_code = ?
ORDER BY name;

-- name: UpdateTagLanguage :exec
UPDATE tags SET language_code = ?, updated_at = ? WHERE id = ?;

-- Get all available translations for a tag (for language switcher)
-- Translation edges are traversed as an undirected connected component so a
-- sibling remains visible even when it is more than one hop from this tag.
-- name: GetTagAvailableTranslations :many
WITH RECURSIVE translation_component(entity_id) AS (
    SELECT CAST(sqlc.arg('entity_id') AS INTEGER) AS entity_id FROM (SELECT 1 AS seed)
    UNION
    SELECT CASE
        WHEN tr.entity_id = translation_component.entity_id THEN tr.translation_id
        ELSE tr.entity_id
    END AS entity_id
    FROM translations tr
    INNER JOIN translation_component
        ON tr.entity_id = translation_component.entity_id
        OR tr.translation_id = translation_component.entity_id
    WHERE tr.entity_type = 'tag'
)
SELECT
    l.id as language_id,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name,
    l.direction as language_direction,
    l.is_default as is_default,
    COALESCE(t.id, 0) as tag_id,
    COALESCE(t.slug, '') as tag_slug,
    COALESCE(t.name, '') as tag_name
FROM languages l
LEFT JOIN (
    SELECT t.id, t.slug, t.name, t.language_code
    FROM tags t
    INNER JOIN translation_component tc ON tc.entity_id = t.id
) t ON t.language_code = l.code
WHERE l.is_active = 1
ORDER BY l.position;

-- name: TagSlugExistsForLanguage :one
SELECT EXISTS(SELECT 1 FROM tags WHERE slug = ? AND language_code = ?);

-- name: TagSlugExistsExcludingForLanguage :one
SELECT EXISTS(SELECT 1 FROM tags WHERE slug = ? AND id != ? AND language_code = ?);

-- Tag usage counts filtered by both page and tag language (for frontend sidebar)
-- name: GetTagUsageCountsByLanguage :many
SELECT t.id, t.name, t.slug, t.language_code, t.created_at, t.updated_at, COUNT(p.id) as usage_count
FROM tags t
INNER JOIN page_tags pt ON pt.tag_id = t.id
INNER JOIN pages p ON p.id = pt.page_id AND p.status = 'published' AND p.language_code = ?
WHERE t.language_code = ?
GROUP BY t.id, t.name, t.slug, t.language_code, t.created_at, t.updated_at
ORDER BY usage_count DESC, t.name
LIMIT ? OFFSET ?;

-- name: GetTagNamesForAllPages :many
SELECT pt.page_id, t.name
FROM page_tags pt
INNER JOIN tags t ON t.id = pt.tag_id
ORDER BY pt.page_id, t.name;

-- name: GetTagNamesForPublishedPages :many
SELECT pt.page_id, t.name
FROM page_tags pt
INNER JOIN tags t ON t.id = pt.tag_id
INNER JOIN pages p ON p.id = pt.page_id AND p.status = 'published'
ORDER BY pt.page_id, t.name;

-- name: GetPublishedTagUsageCounts :many
SELECT t.id, t.name, t.slug, t.language_code, t.created_at, t.updated_at, COUNT(p.id) as usage_count
FROM tags t
INNER JOIN page_tags pt ON pt.tag_id = t.id
INNER JOIN pages p ON p.id = pt.page_id AND p.status = 'published'
GROUP BY t.id, t.name, t.slug, t.language_code, t.created_at, t.updated_at
ORDER BY usage_count DESC, t.name
LIMIT ? OFFSET ?;
