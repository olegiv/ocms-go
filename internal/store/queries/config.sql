-- name: GetConfig :one
SELECT * FROM config WHERE key = ?;

-- name: GetConfigByKey :one
SELECT * FROM config WHERE key = ?;

-- name: ListConfig :many
SELECT * FROM config ORDER BY key;

-- name: UpsertConfig :one
INSERT INTO config (key, value, type, description, language_id, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
    value = excluded.value,
    language_id = excluded.language_id,
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

-- Config Translations

-- name: GetConfigTranslation :one
SELECT ct.*, l.code as language_code, l.name as language_name
FROM config_translations ct
JOIN languages l ON l.id = ct.language_id
WHERE ct.config_key = ? AND ct.language_id = ?;

-- name: GetConfigTranslationByKeyAndLangCode :one
SELECT ct.*, l.code as language_code, l.name as language_name
FROM config_translations ct
JOIN languages l ON l.id = ct.language_id
WHERE ct.config_key = ? AND l.code = ?;

-- name: ListConfigTranslations :many
SELECT ct.*, l.code as language_code, l.name as language_name
FROM config_translations ct
JOIN languages l ON l.id = ct.language_id
WHERE ct.config_key = ?
ORDER BY l.position, l.code;

-- name: ListAllConfigTranslations :many
SELECT ct.*, l.code as language_code, l.name as language_name
FROM config_translations ct
JOIN languages l ON l.id = ct.language_id
ORDER BY ct.config_key, l.position, l.code;

-- name: UpsertConfigTranslation :one
INSERT INTO config_translations (config_key, language_id, value, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(config_key, language_id) DO UPDATE SET
    value = excluded.value,
    updated_at = excluded.updated_at,
    updated_by = excluded.updated_by
RETURNING *;

-- name: DeleteConfigTranslation :exec
DELETE FROM config_translations WHERE config_key = ? AND language_id = ?;

-- name: DeleteConfigTranslationsForKey :exec
DELETE FROM config_translations WHERE config_key = ?;
