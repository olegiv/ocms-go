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
ORDER BY l.position;

-- name: GetAllTranslationsOfEntity :many
SELECT t.*, l.code as language_code, l.name as language_name, l.native_name as language_native_name
FROM translations t
INNER JOIN languages l ON l.id = t.language_id
WHERE t.entity_type = ? AND (t.entity_id = ? OR t.translation_id = ?)
ORDER BY l.position;

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
ORDER BY l.position;

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

-- Get page with its language information (no JOIN needed - language_code is on pages)
-- name: GetPageWithLanguage :one
SELECT
    p.*,
    l.name as language_name,
    l.native_name as language_native_name,
    l.direction as language_direction
FROM pages p
INNER JOIN languages l ON l.code = p.language_code
WHERE p.id = ?;

-- List all pages for a specific language
-- name: ListPagesByLanguage :many
SELECT * FROM pages
WHERE language_code = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- Count pages for a specific language
-- name: CountPagesByLanguage :one
SELECT COUNT(*) FROM pages WHERE language_code = ?;

-- List published pages for a specific language
-- name: ListPublishedPagesByLanguage :many
SELECT * FROM pages
WHERE language_code = ? AND status = 'published'
ORDER BY published_at DESC
LIMIT ? OFFSET ?;

-- Count published pages for a specific language
-- name: CountPublishedPagesByLanguage :one
SELECT COUNT(*) FROM pages WHERE language_code = ? AND status = 'published';

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
ORDER BY l.position;

-- Update page language
-- name: UpdatePageLanguage :exec
UPDATE pages SET language_code = ?, updated_at = ? WHERE id = ?;

-- Get all available translations for a page (for language switcher)
-- Returns the page itself plus all its translations with language info and page slugs
-- name: GetPageAvailableTranslations :many
SELECT
    l.id as language_id,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name,
    l.direction as language_direction,
    l.is_default as is_default,
    COALESCE(p.id, 0) as page_id,
    COALESCE(p.slug, '') as page_slug,
    COALESCE(p.title, '') as page_title
FROM languages l
LEFT JOIN (
    -- Get pages that are translations of the source page
    SELECT p.id, p.slug, p.title, p.language_code
    FROM pages p
    INNER JOIN translations t ON t.translation_id = p.id
    WHERE t.entity_type = 'page' AND t.entity_id = ? AND p.status = 'published'
    UNION
    -- Get the source page itself
    SELECT p.id, p.slug, p.title, p.language_code
    FROM pages p
    WHERE p.id = ? AND p.status = 'published'
    UNION
    -- Get pages where current page is a translation (sibling translations)
    SELECT p2.id, p2.slug, p2.title, p2.language_code
    FROM translations t
    INNER JOIN pages p2 ON (p2.id = t.entity_id OR p2.id = t.translation_id)
    WHERE t.entity_type = 'page'
    AND (t.entity_id = ? OR t.translation_id = ?)
    AND p2.status = 'published'
) p ON p.language_code = l.code
WHERE l.is_active = 1
ORDER BY l.position;

-- Get page with language info by slug (no JOIN needed - language_code is on pages)
-- name: GetPublishedPageWithLanguageBySlug :one
SELECT
    p.*,
    l.name as language_name,
    l.native_name as language_native_name,
    l.direction as language_direction,
    l.is_default as language_is_default
FROM pages p
INNER JOIN languages l ON l.code = p.language_code
WHERE p.slug = ? AND p.status = 'published';

-- Get page count per active language for translation coverage dashboard widget
-- name: GetTranslationCoverage :many
SELECT
    l.id as language_id,
    l.code as language_code,
    l.name as language_name,
    l.is_default as is_default,
    COUNT(p.id) as page_count
FROM languages l
LEFT JOIN pages p ON p.language_code = l.code
WHERE l.is_active = 1
GROUP BY l.id, l.code, l.name, l.is_default, l.position
ORDER BY l.is_default DESC, l.position;

-- Batch get translation counts for multiple entities (for page lists)
-- Returns translation count per entity
-- name: GetTranslationCountsBatch :many
SELECT
    entity_id,
    COUNT(*) as translation_count
FROM translations
WHERE entity_type = ?
GROUP BY entity_id;

-- Batch get translations for multiple page IDs (for page list with translations indicator)
-- name: GetTranslationsForPagesBatch :many
SELECT
    t.entity_id,
    t.language_id,
    t.translation_id,
    l.code as language_code,
    l.name as language_name
FROM translations t
INNER JOIN languages l ON l.id = t.language_id
WHERE t.entity_type = 'page'
ORDER BY t.entity_id, l.position;

-- Get total translation statistics
-- name: GetTranslationStats :one
SELECT
    COUNT(DISTINCT entity_id) as total_entities,
    COUNT(*) as total_translations,
    (SELECT COUNT(*) FROM translations WHERE entity_type = 'page') as page_translations,
    (SELECT COUNT(*) FROM translations WHERE entity_type = 'category') as category_translations,
    (SELECT COUNT(*) FROM translations WHERE entity_type = 'tag') as tag_translations
FROM translations;
