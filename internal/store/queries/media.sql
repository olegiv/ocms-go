-- name: CreateMedia :one
INSERT INTO media (uuid, filename, mime_type, size, width, height, alt, caption, folder_id, uploaded_by, language_code, created_at, updated_at)
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
UPDATE media SET filename = ?, alt = ?, caption = ?, folder_id = ?, language_code = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: UpdateMediaForImport :one
UPDATE media
SET filename = ?, mime_type = ?, size = ?, width = ?, height = ?, alt = ?,
    caption = ?, folder_id = ?, uploaded_by = ?, language_code = ?, updated_at = ?
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

-- Media Translations (uses language_id as FK to languages table)

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

-- name: GetMediaTranslationAlt :one
SELECT mt.alt FROM media_translations mt
JOIN languages l ON l.id = mt.language_id
WHERE mt.media_id = ? AND l.code = ? AND mt.alt != '';

-- name: GetMediaTranslationCaption :one
SELECT mt.caption FROM media_translations mt
JOIN languages l ON l.id = mt.language_id
WHERE mt.media_id = ? AND l.code = ? AND mt.caption != '';

-- name: CountPagesUsingMedia :one
SELECT COUNT(*) FROM pages
WHERE featured_image_id = ? OR og_image_id = ?;

-- name: CountPagesEmbeddingMediaUUID :one
SELECT COUNT(*) FROM pages
WHERE instr(
    body,
    '/uploads/' || sqlc.arg(storage_dir) || '/' || sqlc.arg(media_uuid) || '/'
) > 0;

-- name: CountContentReferencingMediaPath :one
-- Counts every content record still pointing at a media storage path.
--
-- Media URLs are plain text with no foreign key, so nothing in the schema
-- stops a file being deleted while a menu item, category description, form,
-- widget or config value still links to it. Public form submissions are not
-- trusted references because unauthenticated visitors control their data.
-- The caller passes the full '/uploads/<dir>/<uuid>/' prefix; matching the
-- prefix rather than a whole
-- URL keeps filenames, variants and query strings out of the comparison.
SELECT
    (SELECT COUNT(*) FROM pages
      WHERE instr(body, sqlc.arg(media_path)) > 0
         OR instr(summary, sqlc.arg(media_path)) > 0
         OR instr(video_url, sqlc.arg(media_path)) > 0
         OR instr(canonical_url, sqlc.arg(media_path)) > 0
         OR instr(meta_description, sqlc.arg(media_path)) > 0)
  + (SELECT COUNT(*) FROM menu_items WHERE instr(COALESCE(url, ''), sqlc.arg(media_path)) > 0)
  + (SELECT COUNT(*) FROM categories WHERE instr(COALESCE(description, ''), sqlc.arg(media_path)) > 0)
  + (SELECT COUNT(*) FROM forms
      WHERE instr(COALESCE(description, ''), sqlc.arg(media_path)) > 0
         OR instr(COALESCE(success_message, ''), sqlc.arg(media_path)) > 0)
  + (SELECT COUNT(*) FROM form_fields
      WHERE instr(COALESCE(placeholder, ''), sqlc.arg(media_path)) > 0
         OR instr(COALESCE(help_text, ''), sqlc.arg(media_path)) > 0
         OR instr(COALESCE(options, ''), sqlc.arg(media_path)) > 0
         OR instr(COALESCE(validation, ''), sqlc.arg(media_path)) > 0)
  + (SELECT COUNT(*) FROM widgets
      WHERE instr(COALESCE(content, ''), sqlc.arg(media_path)) > 0
         OR instr(COALESCE(settings, ''), sqlc.arg(media_path)) > 0)
  + (SELECT COUNT(*) FROM config WHERE instr(value, sqlc.arg(media_path)) > 0)
  AS reference_count;
