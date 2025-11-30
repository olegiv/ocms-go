-- name: CreatePage :one
INSERT INTO pages (title, slug, body, status, author_id, featured_image_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
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
SET title = ?, slug = ?, body = ?, status = ?, featured_image_id = ?, updated_at = ?
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

-- Featured image queries

-- name: GetFeaturedImageForPage :one
SELECT m.* FROM media m
INNER JOIN pages p ON p.featured_image_id = m.id
WHERE p.id = ?;

-- name: UpdatePageFeaturedImage :exec
UPDATE pages SET featured_image_id = ?, updated_at = ? WHERE id = ?;

-- name: ClearPageFeaturedImage :exec
UPDATE pages SET featured_image_id = NULL, updated_at = ? WHERE id = ?;

-- Frontend queries for public pages

-- name: ListPublishedPages :many
SELECT * FROM pages WHERE status = 'published' ORDER BY published_at DESC LIMIT ? OFFSET ?;

-- name: CountPublishedPages :one
SELECT COUNT(*) FROM pages WHERE status = 'published';

-- name: GetPublishedPageBySlug :one
SELECT * FROM pages WHERE slug = ? AND status = 'published';

-- name: ListPublishedPagesByCategory :many
SELECT DISTINCT p.* FROM pages p
INNER JOIN page_categories pc ON pc.page_id = p.id
WHERE pc.category_id = ? AND p.status = 'published'
ORDER BY p.published_at DESC
LIMIT ? OFFSET ?;

-- name: CountPublishedPagesByCategory :one
SELECT COUNT(DISTINCT p.id) FROM pages p
INNER JOIN page_categories pc ON pc.page_id = p.id
WHERE pc.category_id = ? AND p.status = 'published';

-- name: ListPublishedPagesForTag :many
SELECT p.* FROM pages p
INNER JOIN page_tags pt ON pt.page_id = p.id
WHERE pt.tag_id = ? AND p.status = 'published'
ORDER BY p.published_at DESC
LIMIT ? OFFSET ?;

-- name: CountPublishedPagesForTag :one
SELECT COUNT(*) FROM pages p
INNER JOIN page_tags pt ON pt.page_id = p.id
WHERE pt.tag_id = ? AND p.status = 'published';

-- name: GetPageAuthor :one
SELECT u.id, u.name, u.email FROM users u
INNER JOIN pages p ON p.author_id = u.id
WHERE p.id = ?;
