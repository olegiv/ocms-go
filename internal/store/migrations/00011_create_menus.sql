-- +goose Up
-- +goose StatementBegin
CREATE TABLE menus (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE menu_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    menu_id INTEGER NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    parent_id INTEGER REFERENCES menu_items(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    url TEXT DEFAULT '',
    target TEXT DEFAULT '_self',
    page_id INTEGER REFERENCES pages(id) ON DELETE SET NULL,
    position INTEGER NOT NULL DEFAULT 0,
    css_class TEXT DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_menus_slug ON menus(slug);
CREATE INDEX idx_menu_items_menu ON menu_items(menu_id);
CREATE INDEX idx_menu_items_parent ON menu_items(parent_id);
CREATE INDEX idx_menu_items_page ON menu_items(page_id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS menu_items;
DROP TABLE IF EXISTS menus;
