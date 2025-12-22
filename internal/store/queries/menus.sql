-- name: CreateMenu :one
INSERT INTO menus (name, slug, language_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMenuByID :one
SELECT * FROM menus WHERE id = ?;

-- name: GetMenuBySlug :one
SELECT * FROM menus WHERE slug = ?;

-- name: ListMenus :many
SELECT * FROM menus ORDER BY name;

-- name: UpdateMenu :one
UPDATE menus SET name = ?, slug = ?, language_id = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteMenu :exec
DELETE FROM menus WHERE id = ?;

-- name: CountMenus :one
SELECT COUNT(*) FROM menus;

-- name: MenuSlugExists :one
SELECT EXISTS(SELECT 1 FROM menus WHERE slug = ?);

-- name: MenuSlugExistsExcluding :one
SELECT EXISTS(SELECT 1 FROM menus WHERE slug = ? AND id != ?);

-- name: MenuSlugExistsForLanguage :one
SELECT EXISTS(SELECT 1 FROM menus WHERE slug = ? AND language_id = ?);

-- name: MenuSlugExistsForLanguageExcluding :one
SELECT EXISTS(SELECT 1 FROM menus WHERE slug = ? AND language_id = ? AND id != ?);

-- Menu Item queries

-- name: CreateMenuItem :one
INSERT INTO menu_items (menu_id, parent_id, title, url, target, page_id, position, css_class, is_active, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMenuItemByID :one
SELECT * FROM menu_items WHERE id = ?;

-- name: ListMenuItems :many
SELECT * FROM menu_items WHERE menu_id = ? ORDER BY position;

-- name: ListTopLevelMenuItems :many
SELECT * FROM menu_items WHERE menu_id = ? AND parent_id IS NULL ORDER BY position;

-- name: ListChildMenuItems :many
SELECT * FROM menu_items WHERE parent_id = ? ORDER BY position;

-- name: UpdateMenuItem :one
UPDATE menu_items SET parent_id = ?, title = ?, url = ?, target = ?, page_id = ?, position = ?, css_class = ?, is_active = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteMenuItem :exec
DELETE FROM menu_items WHERE id = ?;

-- name: DeleteMenuItems :exec
DELETE FROM menu_items WHERE menu_id = ?;

-- name: UpdateMenuItemPosition :exec
UPDATE menu_items SET parent_id = ?, position = ?, updated_at = ?
WHERE id = ?;

-- name: CountMenuItems :one
SELECT COUNT(*) FROM menu_items WHERE menu_id = ?;

-- name: GetMaxMenuItemPosition :one
SELECT COALESCE(MAX(position), -1) FROM menu_items WHERE menu_id = ? AND (parent_id IS NULL OR parent_id = ?);

-- Menu item with page info

-- name: ListMenuItemsWithPage :many
SELECT
    mi.*,
    p.title as page_title,
    p.slug as page_slug
FROM menu_items mi
LEFT JOIN pages p ON mi.page_id = p.id
WHERE mi.menu_id = ?
ORDER BY mi.position;

-- Language-specific menu queries

-- name: ListMenusByLanguage :many
SELECT m.*, l.code as language_code, l.name as language_name, l.native_name as language_native_name
FROM menus m
LEFT JOIN languages l ON m.language_id = l.id
WHERE m.language_id = ?
ORDER BY m.name;

-- name: ListMenusWithLanguage :many
SELECT m.*, l.code as language_code, l.name as language_name, l.native_name as language_native_name
FROM menus m
LEFT JOIN languages l ON m.language_id = l.id
ORDER BY m.name;

-- name: GetMenuBySlugAndLanguage :one
SELECT m.*, l.code as language_code
FROM menus m
LEFT JOIN languages l ON m.language_id = l.id
WHERE m.slug = ? AND m.language_id = ?;

-- name: GetMenuBySlugWithLanguage :one
SELECT m.*, l.code as language_code, l.name as language_name
FROM menus m
LEFT JOIN languages l ON m.language_id = l.id
WHERE m.slug = ?;

-- name: GetMenuForLanguageOrDefault :one
SELECT m.*, l.code as language_code
FROM menus m
LEFT JOIN languages l ON m.language_id = l.id
WHERE m.slug = ? AND (m.language_id = ? OR l.is_default = 1)
ORDER BY CASE WHEN m.language_id = ? THEN 0 ELSE 1 END
LIMIT 1;
