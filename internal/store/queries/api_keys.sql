-- name: CreateAPIKey :one
INSERT INTO api_keys (name, key_hash, key_prefix, permissions, expires_at, is_active, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAPIKeyByID :one
SELECT * FROM api_keys WHERE id = ?;

-- name: GetAPIKeyByPrefix :one
SELECT * FROM api_keys WHERE key_prefix = ? AND is_active = 1;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = ? AND is_active = 1;

-- name: ListAPIKeys :many
SELECT * FROM api_keys ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAPIKeysByUser :many
SELECT * FROM api_keys WHERE created_by = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys SET last_used_at = ? WHERE id = ?;

-- name: UpdateAPIKey :one
UPDATE api_keys SET name = ?, permissions = ?, expires_at = ?, is_active = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeactivateAPIKey :exec
UPDATE api_keys SET is_active = 0, updated_at = ? WHERE id = ?;

-- name: DeleteAPIKey :exec
DELETE FROM api_keys WHERE id = ?;

-- name: CountAPIKeys :one
SELECT COUNT(*) FROM api_keys;

-- name: CountActiveAPIKeys :one
SELECT COUNT(*) FROM api_keys WHERE is_active = 1;
