-- name: CreateUser :one
INSERT INTO users (email, password_hash, role, name, avatar, bio, website_url, linkedin_url, github_url, telegram_url, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: UpdateUser :one
UPDATE users SET email = ?, role = ?, name = ?, avatar = ?, bio = ?, website_url = ?, linkedin_url = ?, github_url = ?, telegram_url = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: UpdateUserPassword :exec
-- Updates the user's password hash and bumps session_version, invalidating
-- every existing session for this user. Use this for any actual credential
-- change. For rehash-on-login (parameter migration of an unchanged
-- password) use UpdateUserPasswordHash instead so the user is not logged
-- out by their own login.
UPDATE users SET password_hash = ?, updated_at = ?, session_version = session_version + 1
WHERE id = ?;

-- name: UpdateUserPasswordHash :exec
-- Updates only the password hash representation (e.g., re-hashing with
-- updated Argon2 parameters when the user logs in with their existing
-- password). Does not bump session_version because the credential itself
-- has not changed.
UPDATE users SET password_hash = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CountUsersByRole :one
SELECT COUNT(*) FROM users WHERE role = ?;

-- name: CountUserOwnedContent :one
-- Everything that blocks or would be silently re-attributed by deleting a user.
-- pages.author_id and page_versions.changed_by are ON DELETE RESTRICT, and
-- media.uploaded_by has no action clause, which enforces the same way.
SELECT
    (SELECT COUNT(*) FROM pages WHERE author_id = ?) +
    (SELECT COUNT(*) FROM page_versions WHERE changed_by = ?) +
    (SELECT COUNT(*) FROM media WHERE uploaded_by = ?) +
    (SELECT COUNT(*) FROM api_keys k WHERE k.created_by = ?) +
    (SELECT COUNT(*) FROM webhooks w WHERE w.created_by = ?);
