-- name: CreateUser :one
INSERT INTO users (email, password_hash, role, name, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: UpdateUser :one
UPDATE users SET email = ?, role = ?, name = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;
