-- name: CreateMedia :one
INSERT INTO media (uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by, language_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMediaByID :one
SELECT * FROM media WHERE id = ?;

-- name: GetMediaByUUID :one
SELECT * FROM media WHERE uuid = ?;

-- name: ListMedia :many
SELECT * FROM media ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListMediaInFolder :many
SELECT * FROM media WHERE folder_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListMediaInRootFolder :many
SELECT * FROM media WHERE folder_id IS NULL ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListMediaByType :many
SELECT * FROM media WHERE mime_type LIKE ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: SearchMedia :many
SELECT * FROM media WHERE filename LIKE ? OR alt LIKE ? ORDER BY created_at DESC LIMIT ?;

-- name: UpdateMedia :one
UPDATE media SET filename = ?, alt = ?, caption = ?, folder_id = ?, language_id = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteMedia :exec
DELETE FROM media WHERE id = ?;

-- name: CountMedia :one
SELECT COUNT(*) FROM media;

-- name: CountMediaInFolder :one
SELECT COUNT(*) FROM media WHERE folder_id = ?;

-- name: CountMediaInRootFolder :one
SELECT COUNT(*) FROM media WHERE folder_id IS NULL;

-- name: CountMediaByType :one
SELECT COUNT(*) FROM media WHERE mime_type LIKE ?;

-- name: CreateMediaVariant :one
INSERT INTO media_variants (media_id, type, width, height, size, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMediaVariants :many
SELECT * FROM media_variants WHERE media_id = ?;

-- name: GetMediaVariant :one
SELECT * FROM media_variants WHERE media_id = ? AND type = ?;

-- name: DeleteMediaVariants :exec
DELETE FROM media_variants WHERE media_id = ?;

-- name: CreateMediaFolder :one
INSERT INTO media_folders (name, parent_id, position, created_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetMediaFolderByID :one
SELECT * FROM media_folders WHERE id = ?;

-- name: ListMediaFolders :many
SELECT * FROM media_folders ORDER BY position, name;

-- name: ListRootMediaFolders :many
SELECT * FROM media_folders WHERE parent_id IS NULL ORDER BY position, name;

-- name: ListChildMediaFolders :many
SELECT * FROM media_folders WHERE parent_id = ? ORDER BY position, name;

-- name: UpdateMediaFolder :one
UPDATE media_folders SET name = ?, parent_id = ?, position = ?
WHERE id = ?
RETURNING *;

-- name: DeleteMediaFolder :exec
DELETE FROM media_folders WHERE id = ?;

-- name: CountMediaFolders :one
SELECT COUNT(*) FROM media_folders;

-- name: MoveMediaToFolder :exec
UPDATE media SET folder_id = ?, updated_at = ? WHERE id = ?;

-- name: GetRecentMedia :many
SELECT * FROM media ORDER BY created_at DESC LIMIT ?;

-- Media Translations

-- name: GetMediaTranslation :one
SELECT * FROM media_translations
WHERE media_id = ? AND language_id = ?;

-- name: GetMediaTranslations :many
SELECT mt.*, l.code as language_code, l.name as language_name
FROM media_translations mt
JOIN languages l ON l.id = mt.language_id
WHERE mt.media_id = ?
ORDER BY l.position;

-- name: UpsertMediaTranslation :one
INSERT INTO media_translations (media_id, language_id, alt, caption, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(media_id, language_id)
DO UPDATE SET alt = excluded.alt, caption = excluded.caption, updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: DeleteMediaTranslation :exec
DELETE FROM media_translations WHERE media_id = ? AND language_id = ?;

-- name: DeleteAllMediaTranslations :exec
DELETE FROM media_translations WHERE media_id = ?;
