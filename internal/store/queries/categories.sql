-- name: CreateCategory :one
INSERT INTO categories (name, slug, description, parent_id, position, language_code, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetCategoryByID :one
SELECT * FROM categories WHERE id = ?;

-- name: GetCategoryBySlug :one
SELECT * FROM categories WHERE slug = ?;

-- name: ListCategories :many
SELECT * FROM categories ORDER BY position, name;

-- name: ListRootCategories :many
SELECT * FROM categories WHERE parent_id IS NULL ORDER BY position, name;

-- name: ListChildCategories :many
SELECT * FROM categories WHERE parent_id = ? ORDER BY position, name;

-- name: UpdateCategory :one
UPDATE categories SET name = ?, slug = ?, description = ?, parent_id = ?, position = ?, language_code = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteCategory :exec
DELETE FROM categories WHERE id = ?;

-- name: CountCategories :one
SELECT COUNT(*) FROM categories;

-- name: AddCategoryToPage :exec
INSERT OR IGNORE INTO page_categories (page_id, category_id) VALUES (?, ?);

-- name: RemoveCategoryFromPage :exec
DELETE FROM page_categories WHERE page_id = ? AND category_id = ?;

-- name: GetCategoriesForPage :many
SELECT c.* FROM categories c
INNER JOIN page_categories pc ON pc.category_id = c.id
WHERE pc.page_id = ?
ORDER BY c.name;

-- name: ClearPageCategories :exec
DELETE FROM page_categories WHERE page_id = ?;

-- name: GetCategoryPath :many
WITH RECURSIVE category_path AS (
    SELECT cat.id, cat.name, cat.slug, cat.parent_id, 0 as depth
    FROM categories cat WHERE cat.id = ?
    UNION ALL
    SELECT c.id, c.name, c.slug, c.parent_id, cp.depth + 1
    FROM categories c
    INNER JOIN category_path cp ON c.id = cp.parent_id
)
SELECT id, name, slug, parent_id, depth FROM category_path ORDER BY depth DESC;

-- name: CategorySlugExists :one
SELECT COUNT(*) FROM categories WHERE slug = ?;

-- name: CategorySlugExistsExcluding :one
SELECT COUNT(*) FROM categories WHERE slug = ? AND id != ?;

-- name: SearchCategories :many
SELECT * FROM categories
WHERE name LIKE '%' || ? || '%'
ORDER BY name LIMIT 20;

-- name: GetCategoryUsageCounts :many
SELECT c.*, COUNT(pc.page_id) as usage_count
FROM categories c
LEFT JOIN page_categories pc ON pc.category_id = c.id
GROUP BY c.id, c.name, c.slug, c.description, c.parent_id, c.position, c.language_code, c.created_at, c.updated_at
ORDER BY c.position, c.name;

-- name: UpdateCategoryPosition :exec
UPDATE categories SET position = ?, updated_at = ? WHERE id = ?;

-- name: GetDescendantIDs :many
WITH RECURSIVE descendants AS (
    SELECT cat.id FROM categories cat WHERE cat.parent_id = ?
    UNION ALL
    SELECT c.id FROM categories c
    INNER JOIN descendants d ON c.parent_id = d.id
)
SELECT id FROM descendants;

-- name: ListPagesByCategory :many
SELECT DISTINCT p.* FROM pages p
INNER JOIN page_categories pc ON pc.page_id = p.id
WHERE pc.category_id = ?
ORDER BY p.updated_at DESC
LIMIT ? OFFSET ?;

-- name: CountPagesByCategory :one
SELECT COUNT(DISTINCT p.id) FROM pages p
INNER JOIN page_categories pc ON pc.page_id = p.id
WHERE pc.category_id = ?;

-- name: ListCategoriesForSitemap :many
SELECT c.id, c.slug, c.updated_at, c.language_code, l.is_default
FROM categories c
INNER JOIN languages l ON l.code = c.language_code AND l.is_active = 1
ORDER BY c.updated_at DESC;

-- Language-specific category queries (no JOINs needed - language_code is directly on the table)

-- name: ListCategoriesByLanguage :many
SELECT * FROM categories
WHERE language_code = ?
ORDER BY position, name;

-- name: UpdateCategoryLanguage :exec
UPDATE categories SET language_code = ?, updated_at = ? WHERE id = ?;

-- Get all available translations for a category (for language switcher)
-- Translation edges are traversed as an undirected connected component so a
-- sibling remains visible even when it is more than one hop from this category.
-- name: GetCategoryAvailableTranslations :many
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
    WHERE t.entity_type = 'category'
)
SELECT
    l.id as language_id,
    l.code as language_code,
    l.name as language_name,
    l.native_name as language_native_name,
    l.direction as language_direction,
    l.is_default as is_default,
    COALESCE(c.id, 0) as category_id,
    COALESCE(c.slug, '') as category_slug,
    COALESCE(c.name, '') as category_name
FROM languages l
LEFT JOIN (
    SELECT c.id, c.slug, c.name, c.language_code
    FROM categories c
    INNER JOIN translation_component tc ON tc.entity_id = c.id
) c ON c.language_code = l.code
WHERE l.is_active = 1
ORDER BY l.position;

-- name: CategorySlugExistsForLanguage :one
SELECT COUNT(*) FROM categories WHERE slug = ? AND language_code = ?;

-- name: CategorySlugExistsExcludingForLanguage :one
SELECT COUNT(*) FROM categories WHERE slug = ? AND id != ? AND language_code = ?;

-- Category usage counts filtered by both page and category language (for frontend sidebar)
-- name: GetCategoryUsageCountsByLanguage :many
SELECT c.*, COUNT(p.id) as usage_count
FROM categories c
INNER JOIN page_categories pc ON pc.category_id = c.id
INNER JOIN pages p ON p.id = pc.page_id AND p.status = 'published' AND p.language_code = sqlc.arg(language_code)
WHERE c.language_code = sqlc.arg(language_code)
GROUP BY c.id, c.name, c.slug, c.description, c.parent_id, c.position, c.language_code, c.created_at, c.updated_at
ORDER BY c.position, c.name;

-- name: GetCategoryNamesForAllPages :many
SELECT pc.page_id, c.name
FROM page_categories pc
INNER JOIN categories c ON c.id = pc.category_id
ORDER BY pc.page_id, c.name;

-- name: GetCategoryNamesForPublishedPages :many
SELECT pc.page_id, c.name
FROM page_categories pc
INNER JOIN categories c ON c.id = pc.category_id
INNER JOIN pages p ON p.id = pc.page_id AND p.status = 'published'
ORDER BY pc.page_id, c.name;

-- name: GetPublishedCategoryUsageCounts :many
SELECT c.*, COUNT(p.id) as usage_count
FROM categories c
INNER JOIN page_categories pc ON pc.category_id = c.id
INNER JOIN pages p ON p.id = pc.page_id AND p.status = 'published'
GROUP BY c.id, c.name, c.slug, c.description, c.parent_id, c.position, c.language_code, c.created_at, c.updated_at
ORDER BY c.position, c.name;
