-- name: CreatePage :one
INSERT INTO pages (title, slug, body, status, author_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPageByID :one
SELECT * FROM pages WHERE id = ?;

-- name: GetPageBySlug :one
SELECT * FROM pages WHERE slug = ?;

-- name: ListPages :many
SELECT * FROM pages ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListPagesByStatus :many
SELECT * FROM pages WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: UpdatePage :one
UPDATE pages
SET title = ?, slug = ?, body = ?, status = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: PublishPage :one
UPDATE pages
SET status = 'published', published_at = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: UnpublishPage :one
UPDATE pages
SET status = 'draft', published_at = NULL, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeletePage :exec
DELETE FROM pages WHERE id = ?;

-- name: CountPages :one
SELECT COUNT(*) FROM pages;

-- name: CountPagesByStatus :one
SELECT COUNT(*) FROM pages WHERE status = ?;

-- name: SlugExists :one
SELECT EXISTS(SELECT 1 FROM pages WHERE slug = ?);

-- name: SlugExistsExcluding :one
SELECT EXISTS(SELECT 1 FROM pages WHERE slug = ? AND id != ?);

-- Page Version queries

-- name: CreatePageVersion :one
INSERT INTO page_versions (page_id, title, body, changed_by, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPageVersion :one
SELECT * FROM page_versions WHERE id = ?;

-- name: ListPageVersions :many
SELECT * FROM page_versions WHERE page_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CountPageVersions :one
SELECT COUNT(*) FROM page_versions WHERE page_id = ?;

-- name: GetLatestPageVersion :one
SELECT * FROM page_versions WHERE page_id = ? ORDER BY created_at DESC LIMIT 1;

-- name: DeletePageVersions :exec
DELETE FROM page_versions WHERE page_id = ?;

-- name: ListPageVersionsWithUser :many
SELECT
    pv.id,
    pv.page_id,
    pv.title,
    pv.body,
    pv.changed_by,
    pv.created_at,
    u.name as changed_by_name,
    u.email as changed_by_email
FROM page_versions pv
JOIN users u ON pv.changed_by = u.id
WHERE pv.page_id = ?
ORDER BY pv.created_at DESC
LIMIT ? OFFSET ?;

-- name: GetPageVersionWithUser :one
SELECT
    pv.id,
    pv.page_id,
    pv.title,
    pv.body,
    pv.changed_by,
    pv.created_at,
    u.name as changed_by_name,
    u.email as changed_by_email
FROM page_versions pv
JOIN users u ON pv.changed_by = u.id
WHERE pv.id = ?;
