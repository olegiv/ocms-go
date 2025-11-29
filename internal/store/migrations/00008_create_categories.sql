-- +goose Up
CREATE TABLE categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    parent_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    position INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE page_categories (
    page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    PRIMARY KEY (page_id, category_id)
);

CREATE INDEX idx_categories_slug ON categories(slug);
CREATE INDEX idx_categories_parent ON categories(parent_id);
CREATE INDEX idx_page_categories_page ON page_categories(page_id);
CREATE INDEX idx_page_categories_category ON page_categories(category_id);

-- +goose Down
DROP INDEX IF EXISTS idx_page_categories_category;
DROP INDEX IF EXISTS idx_page_categories_page;
DROP INDEX IF EXISTS idx_categories_parent;
DROP INDEX IF EXISTS idx_categories_slug;
DROP TABLE IF EXISTS page_categories;
DROP TABLE IF EXISTS categories;
