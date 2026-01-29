-- name: CreateWidget :one
INSERT INTO widgets (theme, area, widget_type, title, content, settings, position, is_active, language_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetWidget :one
SELECT * FROM widgets WHERE id = ?;

-- name: GetWidgetsByThemeAndArea :many
SELECT * FROM widgets
WHERE theme = ? AND area = ? AND language_id = ? AND is_active = 1
ORDER BY position;

-- name: GetAllWidgetsByTheme :many
SELECT * FROM widgets
WHERE theme = ?
ORDER BY area, position;

-- name: GetAllWidgetsByThemeAndLanguage :many
SELECT * FROM widgets
WHERE theme = ? AND language_id = ?
ORDER BY area, position;

-- name: GetAllWidgets :many
SELECT * FROM widgets ORDER BY theme, area, position;

-- name: UpdateWidget :one
UPDATE widgets
SET widget_type = ?,
    title = ?,
    content = ?,
    settings = ?,
    position = ?,
    is_active = ?,
    language_id = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: UpdateWidgetPosition :exec
UPDATE widgets
SET position = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteWidget :exec
DELETE FROM widgets WHERE id = ?;

-- name: DeleteWidgetsByTheme :exec
DELETE FROM widgets WHERE theme = ?;

-- name: GetMaxWidgetPosition :one
SELECT COALESCE(MAX(position), 0) as max_position
FROM widgets
WHERE theme = ? AND area = ?;
