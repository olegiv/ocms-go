-- name: GetModule :one
SELECT * FROM modules WHERE name = ?;

-- name: ListModules :many
SELECT * FROM modules ORDER BY name;

-- name: ListActiveModules :many
SELECT name FROM modules WHERE is_active = 1;

-- name: UpsertModule :one
INSERT INTO modules (name, is_active, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(name) DO UPDATE SET
    is_active = excluded.is_active,
    updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: SetModuleActive :one
UPDATE modules
SET is_active = ?, updated_at = CURRENT_TIMESTAMP
WHERE name = ?
RETURNING *;

-- name: IsModuleActive :one
SELECT is_active FROM modules WHERE name = ?;
