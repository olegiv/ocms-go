-- name: CreateCategory :one
INSERT INTO categories (name, slug, description, parent_id, position, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetCategoryByID :one
SELECT * FROM categories WHERE id = ?;

-- name: GetCategoryBySlug :one
SELECT * FROM categories WHERE slug = ?;

-- name: ListCategories :many
SELECT * FROM categories ORDER BY position ASC, name ASC;

-- name: ListRootCategories :many
SELECT * FROM categories WHERE parent_id IS NULL ORDER BY position ASC, name ASC;

-- name: ListChildCategories :many
SELECT * FROM categories WHERE parent_id = ? ORDER BY position ASC, name ASC;

-- name: UpdateCategory :one
UPDATE categories SET name = ?, slug = ?, description = ?, parent_id = ?, position = ?, updated_at = ?
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
ORDER BY c.name ASC;

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
ORDER BY name ASC
LIMIT 20;

-- name: GetCategoryUsageCounts :many
SELECT c.*, COUNT(pc.page_id) as usage_count
FROM categories c
LEFT JOIN page_categories pc ON pc.category_id = c.id
GROUP BY c.id
ORDER BY c.position ASC, c.name ASC;

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
