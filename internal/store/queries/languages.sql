-- name: CreateLanguage :one
INSERT INTO languages (code, name, native_name, is_default, is_active, direction, position, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetLanguageByID :one
SELECT * FROM languages WHERE id = ?;

-- name: GetLanguageByCode :one
SELECT * FROM languages WHERE code = ?;

-- name: GetDefaultLanguage :one
SELECT * FROM languages WHERE is_default = 1;

-- name: ListLanguages :many
SELECT * FROM languages ORDER BY position, name;

-- name: ListActiveLanguages :many
SELECT * FROM languages WHERE is_active = 1 ORDER BY position, name;

-- name: UpdateLanguage :one
UPDATE languages SET code = ?, name = ?, native_name = ?, is_default = ?, is_active = ?, direction = ?, position = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteLanguage :exec
DELETE FROM languages WHERE id = ?;

-- name: ClearDefaultLanguage :exec
UPDATE languages SET is_default = 0 WHERE is_default = 1;

-- name: SetDefaultLanguage :exec
UPDATE languages SET is_default = 1, updated_at = ? WHERE id = ?;

-- name: CountLanguages :one
SELECT COUNT(*) FROM languages;

-- name: CountActiveLanguages :one
SELECT COUNT(*) FROM languages WHERE is_active = 1;

-- name: LanguageCodeExists :one
SELECT EXISTS(SELECT 1 FROM languages WHERE code = ?);

-- name: LanguageCodeExistsExcluding :one
SELECT EXISTS(SELECT 1 FROM languages WHERE code = ? AND id != ?);

-- name: GetMaxLanguagePosition :one
SELECT COALESCE(MAX(position), 0) FROM languages;

-- name: UpdateLanguagePosition :exec
UPDATE languages SET position = ?, updated_at = ? WHERE id = ?;

