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

-- name: DeleteTranslationsRelatedToEntity :exec
DELETE FROM translations
WHERE entity_type = ? AND (entity_id = ? OR translation_id = ?);

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

-- Posts only for a specific language (for recent posts, blog feeds - excludes static pages)
-- name: ListPublishedPostsByLanguage :many
SELECT * FROM pages
WHERE language_code = ? AND status = 'published' AND page_type = 'post' AND exclude_from_lists = 0
ORDER BY published_at DESC
LIMIT ? OFFSET ?;

-- Count published posts for a specific language
-- name: CountPublishedPostsByLanguage :one
SELECT COUNT(*) FROM pages WHERE language_code = ? AND status = 'published' AND page_type = 'post' AND exclude_from_lists = 0;

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
-- Returns every published page in the current translation component. Translation
-- rows form an undirected graph: an entity may be the source or target of an
-- edge, and siblings can be more than one hop away from the current page.
-- name: GetPageAvailableTranslations :many
WITH RECURSIVE translation_component(entity_id) AS (
    SELECT CAST(sqlc.arg('entity_id') AS INTEGER) AS entity_id FROM (SELECT 1 AS seed)
    UNION
    SELECT CASE
        WHEN t.entity_id = translation_component.entity_id THEN t.translation_id
        ELSE t.entity_id
    END AS entity_id
    FROM translations t
    INNER JOIN translation_component
        ON t.entity_id = translation_component.entity_id
        OR t.translation_id = translation_component.entity_id
    WHERE t.entity_type = 'page'
)
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
    SELECT p.id, p.slug, p.title, p.language_code
    FROM pages p
    INNER JOIN translation_component tc ON tc.entity_id = p.id
    WHERE p.status = 'published'
) p ON p.language_code = l.code
WHERE l.is_active = 1
ORDER BY l.position;

-- Return an entity in the same translation component that already owns the
-- requested language. This keeps Translate actions from creating a second
-- entity for a language when the existing entity is connected through a
-- sibling rather than a direct outgoing edge.
-- name: GetTranslationComponentEntityByLanguage :one
WITH RECURSIVE translation_component(entity_id) AS (
    SELECT CAST(sqlc.arg('entity_id') AS INTEGER) AS entity_id FROM (SELECT 1 AS seed)
    UNION
    SELECT CASE
        WHEN t.entity_id = translation_component.entity_id THEN t.translation_id
        ELSE t.entity_id
    END AS entity_id
    FROM translations t
    INNER JOIN translation_component
        ON t.entity_id = translation_component.entity_id
        OR t.translation_id = translation_component.entity_id
    WHERE t.entity_type = sqlc.arg('entity_type')
)
SELECT candidate.entity_id
FROM (
    SELECT p.id AS entity_id
    FROM pages p
    INNER JOIN translation_component tc ON tc.entity_id = p.id
    WHERE sqlc.arg('entity_type') = 'page'
      AND p.language_code = sqlc.arg('language_code')
    UNION ALL
    SELECT c.id AS entity_id
    FROM categories c
    INNER JOIN translation_component tc ON tc.entity_id = c.id
    WHERE sqlc.arg('entity_type') = 'category'
      AND c.language_code = sqlc.arg('language_code')
    UNION ALL
    SELECT tag.id AS entity_id
    FROM tags tag
    INNER JOIN translation_component tc ON tc.entity_id = tag.id
    WHERE sqlc.arg('entity_type') = 'tag'
      AND tag.language_code = sqlc.arg('language_code')
    UNION ALL
    SELECT f.id AS entity_id
    FROM forms f
    INNER JOIN translation_component tc ON tc.entity_id = f.id
    WHERE sqlc.arg('entity_type') = 'form'
      AND f.language_code = sqlc.arg('language_code')
) candidate
ORDER BY candidate.entity_id
LIMIT 1;

-- List every other entity in the same undirected translation component with
-- its actual language. Admin editors use this to show complete translation
-- state even when the current entity is a translated child of a star graph.
-- name: ListTranslationComponentMembers :many
WITH RECURSIVE translation_component(entity_id) AS (
    SELECT CAST(sqlc.arg('source_entity_id') AS INTEGER) AS entity_id FROM (SELECT 1 AS seed)
    UNION
    SELECT CASE
        WHEN t.entity_id = translation_component.entity_id THEN t.translation_id
        ELSE t.entity_id
    END AS entity_id
    FROM translations t
    INNER JOIN translation_component
        ON t.entity_id = translation_component.entity_id
        OR t.translation_id = translation_component.entity_id
    WHERE t.entity_type = sqlc.arg('entity_type')
), component_entities AS (
    SELECT p.id AS entity_id, p.language_code
    FROM pages p
    INNER JOIN translation_component tc ON tc.entity_id = p.id
    WHERE sqlc.arg('entity_type') = 'page'
      AND p.id != sqlc.arg('source_entity_id')
    UNION ALL
    SELECT c.id AS entity_id, c.language_code
    FROM categories c
    INNER JOIN translation_component tc ON tc.entity_id = c.id
    WHERE sqlc.arg('entity_type') = 'category'
      AND c.id != sqlc.arg('source_entity_id')
    UNION ALL
    SELECT tag.id AS entity_id, tag.language_code
    FROM tags tag
    INNER JOIN translation_component tc ON tc.entity_id = tag.id
    WHERE sqlc.arg('entity_type') = 'tag'
      AND tag.id != sqlc.arg('source_entity_id')
    UNION ALL
    SELECT f.id AS entity_id, f.language_code
    FROM forms f
    INNER JOIN translation_component tc ON tc.entity_id = f.id
    WHERE sqlc.arg('entity_type') = 'form'
      AND f.id != sqlc.arg('source_entity_id')
)
SELECT component_entities.entity_id, l.id AS language_id, l.code AS language_code
FROM component_entities
INNER JOIN languages l ON l.code = component_entities.language_code
ORDER BY l.position, component_entities.entity_id;

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
