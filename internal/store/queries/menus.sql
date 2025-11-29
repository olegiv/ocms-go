-- name: CreateMenu :one
INSERT INTO menus (name, slug, created_at, updated_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetMenuByID :one
SELECT * FROM menus WHERE id = ?;

-- name: GetMenuBySlug :one
SELECT * FROM menus WHERE slug = ?;

-- name: ListMenus :many
SELECT * FROM menus ORDER BY name ASC;

-- name: UpdateMenu :one
UPDATE menus SET name = ?, slug = ?, updated_at = ?
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

-- Menu Item queries

-- name: CreateMenuItem :one
INSERT INTO menu_items (menu_id, parent_id, title, url, target, page_id, position, css_class, is_active, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMenuItemByID :one
SELECT * FROM menu_items WHERE id = ?;

-- name: ListMenuItems :many
SELECT * FROM menu_items WHERE menu_id = ? ORDER BY position ASC;

-- name: ListTopLevelMenuItems :many
SELECT * FROM menu_items WHERE menu_id = ? AND parent_id IS NULL ORDER BY position ASC;

-- name: ListChildMenuItems :many
SELECT * FROM menu_items WHERE parent_id = ? ORDER BY position ASC;

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
ORDER BY mi.position ASC;
