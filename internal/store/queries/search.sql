-- Admin page search (non-FTS5, uses LIKE for all statuses)

-- name: CountAdminSearchPages :one
SELECT COUNT(*) FROM pages
WHERE (title LIKE ? ESCAPE '!') OR (body LIKE ? ESCAPE '!');

-- name: SearchAdminPages :many
SELECT id, title, slug, body, status, published_at, created_at, updated_at, featured_image_id
FROM pages
WHERE (title LIKE ? ESCAPE '!') OR (body LIKE ? ESCAPE '!')
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;
