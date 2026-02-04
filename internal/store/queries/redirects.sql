-- name: CreateRedirect :one
INSERT INTO redirects (source_path, target_url, status_code, is_wildcard, target_type, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetRedirectByID :one
SELECT * FROM redirects WHERE id = ?;

-- name: GetRedirectBySourcePath :one
SELECT * FROM redirects WHERE source_path = ?;

-- name: ListRedirects :many
SELECT * FROM redirects ORDER BY source_path;

-- name: ListRedirectsPaginated :many
SELECT * FROM redirects
ORDER BY source_path
LIMIT ? OFFSET ?;

-- name: ListEnabledRedirects :many
SELECT * FROM redirects WHERE enabled = 1 ORDER BY source_path;

-- name: UpdateRedirect :one
UPDATE redirects SET
    source_path = ?,
    target_url = ?,
    status_code = ?,
    is_wildcard = ?,
    target_type = ?,
    enabled = ?,
    updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteRedirect :exec
DELETE FROM redirects WHERE id = ?;

-- name: CountRedirects :one
SELECT COUNT(*) FROM redirects;

-- name: RedirectSourcePathExists :one
SELECT EXISTS(SELECT 1 FROM redirects WHERE source_path = ?);

-- name: RedirectSourcePathExistsExcluding :one
SELECT EXISTS(SELECT 1 FROM redirects WHERE source_path = ? AND id != ?);

-- name: ToggleRedirectEnabled :exec
UPDATE redirects SET enabled = NOT enabled, updated_at = ? WHERE id = ?;
