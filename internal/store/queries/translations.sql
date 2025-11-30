-- name: CreateTranslation :one
INSERT INTO translations (entity_type, entity_id, language_id, translation_id, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTranslation :one
SELECT * FROM translations
WHERE entity_type = ? AND entity_id = ? AND language_id = ?;

-- name: GetTranslationByID :one
SELECT * FROM translations WHERE id = ?;

-- name: GetTranslationsForEntity :many
SELECT t.*, l.code as language_code, l.name as language_name, l.native_name as language_native_name
FROM translations t
INNER JOIN languages l ON l.id = t.language_id
WHERE t.entity_type = ? AND t.entity_id = ?
ORDER BY l.position ASC;

-- name: GetAllTranslationsOfEntity :many
SELECT t.*, l.code as language_code, l.name as language_name, l.native_name as language_native_name
FROM translations t
INNER JOIN languages l ON l.id = t.language_id
WHERE t.entity_type = ? AND (t.entity_id = ? OR t.translation_id = ?)
ORDER BY l.position ASC;

-- name: DeleteTranslation :exec
DELETE FROM translations WHERE id = ?;

-- name: DeleteTranslationsForEntity :exec
DELETE FROM translations WHERE entity_type = ? AND entity_id = ?;

-- name: DeleteTranslationsForEntityAndLanguage :exec
DELETE FROM translations WHERE entity_type = ? AND entity_id = ? AND language_id = ?;

-- Get the translated entity ID for a given entity and target language
-- name: GetTranslatedEntityID :one
SELECT translation_id FROM translations
WHERE entity_type = ? AND entity_id = ? AND language_id = ?;

-- Get all translations related to an entity (where entity is either source or target)
-- name: GetRelatedTranslations :many
SELECT
    t.id,
    t.entity_type,
    t.entity_id,
    t.language_id,
    t.translation_id,
    t.created_at,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name
FROM translations t
INNER JOIN languages l ON l.id = t.language_id
WHERE t.entity_type = ?
  AND (t.entity_id = ? OR t.translation_id = ?)
ORDER BY l.position ASC;

-- Check if translation exists
-- name: TranslationExists :one
SELECT EXISTS(
    SELECT 1 FROM translations
    WHERE entity_type = ? AND entity_id = ? AND language_id = ?
);

-- Count translations for an entity
-- name: CountTranslationsForEntity :one
SELECT COUNT(*) FROM translations WHERE entity_type = ? AND entity_id = ?;

-- Page-specific translation queries

-- name: GetPageByLanguageFromTranslation :one
SELECT p.* FROM pages p
INNER JOIN translations t ON t.translation_id = p.id
WHERE t.entity_type = 'page' AND t.entity_id = ? AND t.language_id = ?;

-- Get page with its language information
-- name: GetPageWithLanguage :one
SELECT
    p.*,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name,
    l.direction as language_direction
FROM pages p
LEFT JOIN languages l ON l.id = p.language_id
WHERE p.id = ?;

-- List all pages for a specific language
-- name: ListPagesByLanguage :many
SELECT * FROM pages
WHERE language_id = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- Count pages for a specific language
-- name: CountPagesByLanguage :one
SELECT COUNT(*) FROM pages WHERE language_id = ?;

-- List published pages for a specific language
-- name: ListPublishedPagesByLanguage :many
SELECT * FROM pages
WHERE language_id = ? AND status = 'published'
ORDER BY published_at DESC
LIMIT ? OFFSET ?;

-- Count published pages for a specific language
-- name: CountPublishedPagesByLanguage :one
SELECT COUNT(*) FROM pages WHERE language_id = ? AND status = 'published';

-- Get the translation of a page in a specific language (by slug for frontend)
-- name: GetPageTranslationBySlug :one
SELECT p.* FROM pages p
INNER JOIN translations t ON t.translation_id = p.id
INNER JOIN pages source ON source.id = t.entity_id
WHERE t.entity_type = 'page'
  AND source.slug = ?
  AND t.language_id = ?
  AND p.status = 'published';

-- Get all translation links for a page (for language switcher)
-- name: GetPageTranslationLinks :many
SELECT
    l.id as language_id,
    l.code as language_code,
    l.name as language_name,
    l.native_name as native_name,
    COALESCE(t.translation_id, 0) as entity_id
FROM languages l
LEFT JOIN translations t ON t.language_id = l.id
    AND t.entity_type = 'page'
    AND t.entity_id = ?
WHERE l.is_active = 1
ORDER BY l.position ASC;

-- Update page language
-- name: UpdatePageLanguage :exec
UPDATE pages SET language_id = ?, updated_at = ? WHERE id = ?;
