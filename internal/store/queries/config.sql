-- name: GetConfig :one
SELECT * FROM config WHERE key = ?;

-- name: ListConfig :many
SELECT * FROM config ORDER BY key;

-- name: UpsertConfig :one
INSERT INTO config (key, value, type, description, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
    value = excluded.value,
    updated_at = excluded.updated_at,
    updated_by = excluded.updated_by
RETURNING *;

-- name: UpdateConfigValue :one
UPDATE config
SET value = ?, updated_at = ?, updated_by = ?
WHERE key = ?
RETURNING *;

-- name: DeleteConfig :exec
DELETE FROM config WHERE key = ?;

-- name: CountConfig :one
SELECT COUNT(*) FROM config;
