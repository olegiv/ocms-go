-- name: CreateTag :one
INSERT INTO tags (name, slug, created_at, updated_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetTagByID :one
SELECT * FROM tags WHERE id = ?;

-- name: GetTagBySlug :one
SELECT * FROM tags WHERE slug = ?;

-- name: ListTags :many
SELECT * FROM tags ORDER BY name ASC LIMIT ? OFFSET ?;

-- name: ListAllTags :many
SELECT * FROM tags ORDER BY name ASC;

-- name: SearchTags :many
SELECT * FROM tags WHERE name LIKE ? ORDER BY name ASC LIMIT ?;

-- name: UpdateTag :one
UPDATE tags SET name = ?, slug = ?, updated_at = ?
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
ORDER BY t.name ASC;

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
GROUP BY t.id
ORDER BY t.name ASC
LIMIT ? OFFSET ?;
