-- name: CreateCategory :one
INSERT INTO categories (name, slug, description, parent_id, position, language_id, created_at, updated_at)
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
UPDATE categories SET name = ?, slug = ?, description = ?, parent_id = ?, position = ?, language_id = ?, updated_at = ?
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
GROUP BY c.id, c.name, c.slug, c.description, c.parent_id, c.position, c.language_id, c.created_at, c.updated_at
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
SELECT id, slug, updated_at FROM categories ORDER BY updated_at DESC;

-- Language-specific category queries

-- name: GetCategoryWithLanguage :one
SELECT
    c.*,
    COALESCE(l.code, '') as language_code,
    COALESCE(l.name, '') as language_name,
    COALESCE(l.native_name, '') as language_native_name,
    COALESCE(l.direction, 'ltr') as language_direction
FROM categories c
LEFT JOIN languages l ON l.id = c.language_id
WHERE c.id = ?;

-- name: ListCategoriesByLanguage :many
SELECT * FROM categories
WHERE language_id = ?
ORDER BY position, name;

-- name: ListCategoriesWithLanguage :many
SELECT
    c.*,
    COALESCE(l.code, '') as language_code,
    COALESCE(l.name, '') as language_name
FROM categories c
LEFT JOIN languages l ON l.id = c.language_id
ORDER BY c.position, c.name;

-- name: GetCategoryUsageCountsWithLanguage :many
SELECT
    c.*,
    COUNT(pc.page_id) as usage_count,
    COALESCE(l.code, '') as language_code,
    COALESCE(l.name, '') as language_name
FROM categories c
LEFT JOIN page_categories pc ON pc.category_id = c.id
LEFT JOIN languages l ON l.id = c.language_id
GROUP BY c.id, c.name, c.slug, c.description, c.parent_id, c.position, c.language_id, c.created_at, c.updated_at, l.code, l.name
ORDER BY c.position, c.name;

-- name: UpdateCategoryLanguage :exec
UPDATE categories SET language_id = ?, updated_at = ? WHERE id = ?;

-- Get all available translations for a category (for language switcher)
-- name: GetCategoryAvailableTranslations :many
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
    -- Get categories that are translations of the source category
    SELECT c.id, c.slug, c.name, t.language_id
    FROM categories c
    INNER JOIN translations t ON t.translation_id = c.id
    WHERE t.entity_type = 'category' AND t.entity_id = ?
    UNION
    -- Get the source category itself
    SELECT c.id, c.slug, c.name, c.language_id
    FROM categories c
    WHERE c.id = ?
    UNION
    -- Get categories where current category is a translation (sibling translations)
    SELECT c2.id, c2.slug, c2.name, c2.language_id
    FROM translations t
    INNER JOIN categories c2 ON (c2.id = t.entity_id OR c2.id = t.translation_id)
    WHERE t.entity_type = 'category'
    AND (t.entity_id = ? OR t.translation_id = ?)
) c ON c.language_id = l.id
WHERE l.is_active = 1
ORDER BY l.position;

-- name: CategorySlugExistsForLanguage :one
SELECT COUNT(*) FROM categories WHERE slug = ? AND language_id = ?;

-- name: CategorySlugExistsExcludingForLanguage :one
SELECT COUNT(*) FROM categories WHERE slug = ? AND id != ? AND language_id = ?;

-- Category usage counts filtered by page language (for frontend sidebar)
-- name: GetCategoryUsageCountsByLanguage :many
SELECT c.*, COUNT(p.id) as usage_count
FROM categories c
INNER JOIN page_categories pc ON pc.category_id = c.id
INNER JOIN pages p ON p.id = pc.page_id AND p.status = 'published' AND p.language_id = ?
GROUP BY c.id, c.name, c.slug, c.description, c.parent_id, c.position, c.language_id, c.created_at, c.updated_at
ORDER BY c.position, c.name;
