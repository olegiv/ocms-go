-- name: CreateTag :one
INSERT INTO tags (name, slug, language_id, created_at, updated_at)
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
UPDATE tags SET name = ?, slug = ?, language_id = ?, updated_at = ?
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
SELECT t.id, t.name, t.slug, t.created_at, t.updated_at, COUNT(pt.page_id) as usage_count
FROM tags t
LEFT JOIN page_tags pt ON pt.tag_id = t.id
GROUP BY t.id, t.name, t.slug, t.created_at, t.updated_at
ORDER BY t.name
LIMIT ? OFFSET ?;

-- name: ListTagsForSitemap :many
SELECT id, slug, updated_at FROM tags ORDER BY updated_at DESC;

-- Language-specific tag queries

-- name: GetTagWithLanguage :one
SELECT
    t.*,
    COALESCE(l.code, '') as language_code,
    COALESCE(l.name, '') as language_name,
    COALESCE(l.native_name, '') as language_native_name,
    COALESCE(l.direction, 'ltr') as language_direction
FROM tags t
LEFT JOIN languages l ON l.id = t.language_id
WHERE t.id = ?;

-- name: ListTagsByLanguage :many
SELECT * FROM tags
WHERE language_id = ?
ORDER BY name;

-- name: ListTagsWithLanguage :many
SELECT
    t.*,
    COALESCE(l.code, '') as language_code,
    COALESCE(l.name, '') as language_name
FROM tags t
LEFT JOIN languages l ON l.id = t.language_id
ORDER BY t.name;

-- name: GetTagUsageCountsWithLanguage :many
SELECT
    t.id, t.name, t.slug, t.language_id, t.created_at, t.updated_at,
    COUNT(pt.page_id) as usage_count,
    COALESCE(l.code, '') as language_code,
    COALESCE(l.name, '') as language_name
FROM tags t
LEFT JOIN page_tags pt ON pt.tag_id = t.id
LEFT JOIN languages l ON l.id = t.language_id
GROUP BY t.id, t.name, t.slug, t.language_id, t.created_at, t.updated_at, l.code, l.name
ORDER BY t.name
LIMIT ? OFFSET ?;

-- name: UpdateTagLanguage :exec
UPDATE tags SET language_id = ?, updated_at = ? WHERE id = ?;

-- Get all available translations for a tag (for language switcher)
-- name: GetTagAvailableTranslations :many
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
    -- Get tags that are translations of the source tag
    SELECT t.id, t.slug, t.name, tr.language_id
    FROM tags t
    INNER JOIN translations tr ON tr.translation_id = t.id
    WHERE tr.entity_type = 'tag' AND tr.entity_id = ?
    UNION
    -- Get the source tag itself
    SELECT t.id, t.slug, t.name, t.language_id
    FROM tags t
    WHERE t.id = ?
    UNION
    -- Get tags where current tag is a translation (sibling translations)
    SELECT t2.id, t2.slug, t2.name, t2.language_id
    FROM translations tr
    INNER JOIN tags t2 ON (t2.id = tr.entity_id OR t2.id = tr.translation_id)
    WHERE tr.entity_type = 'tag'
    AND (tr.entity_id = ? OR tr.translation_id = ?)
) t ON t.language_id = l.id
WHERE l.is_active = 1
ORDER BY l.position;

-- name: TagSlugExistsForLanguage :one
SELECT EXISTS(SELECT 1 FROM tags WHERE slug = ? AND language_id = ?);

-- name: TagSlugExistsExcludingForLanguage :one
SELECT EXISTS(SELECT 1 FROM tags WHERE slug = ? AND id != ? AND language_id = ?);

-- Tag usage counts filtered by page language (for frontend sidebar)
-- name: GetTagUsageCountsByLanguage :many
SELECT t.id, t.name, t.slug, t.created_at, t.updated_at, COUNT(p.id) as usage_count
FROM tags t
INNER JOIN page_tags pt ON pt.tag_id = t.id
INNER JOIN pages p ON p.id = pt.page_id AND p.status = 'published' AND p.language_id = ?
GROUP BY t.id, t.name, t.slug, t.created_at, t.updated_at
ORDER BY t.name
LIMIT ? OFFSET ?;
