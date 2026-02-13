-- Admin page search (non-FTS5, uses LIKE for all statuses)

-- name: CountAdminSearchPages :one
SELECT COUNT(*) FROM pages
WHERE title LIKE ? OR body LIKE ?;

-- name: SearchAdminPages :many
SELECT id, title, slug, body, status, published_at, created_at, updated_at, featured_image_id
FROM pages
WHERE title LIKE ? OR body LIKE ?
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;
