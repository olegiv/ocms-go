-- name: GetEditorialGuide :one
SELECT * FROM editorial_style_guides WHERE language_code = ?;

-- name: UpsertEditorialGuide :exec
INSERT INTO editorial_style_guides(language_code,body,updated_by,updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(language_code) DO UPDATE SET body=excluded.body, updated_by=excluded.updated_by, updated_at=excluded.updated_at;

-- name: DeleteEditorialGuide :exec
DELETE FROM editorial_style_guides WHERE language_code = ? AND language_code <> '';

-- name: ListEditorialPresets :many
SELECT * FROM editorial_presets WHERE language_code IN ('', sqlc.arg(language_code)) ORDER BY slug, language_code;

-- name: UpsertEditorialPreset :exec
INSERT INTO editorial_presets(slug,language_code,name,instructions,updated_by,updated_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(slug,language_code) DO UPDATE SET name=excluded.name, instructions=excluded.instructions, updated_by=excluded.updated_by, updated_at=excluded.updated_at;

-- name: DeleteEditorialPresetOverride :exec
DELETE FROM editorial_presets WHERE slug = ? AND language_code = ? AND language_code <> '';
