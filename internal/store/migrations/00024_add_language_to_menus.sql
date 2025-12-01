-- +goose Up
-- Add language support to menus

-- Add language_id to menus table
ALTER TABLE menus ADD COLUMN language_id INTEGER REFERENCES languages(id);

-- Set existing menus to default language
UPDATE menus SET language_id = (SELECT id FROM languages WHERE is_default = 1);

-- Create index for language filtering
CREATE INDEX idx_menus_language_id ON menus(language_id);

-- +goose Down
DROP INDEX IF EXISTS idx_menus_language_id;

-- SQLite doesn't support DROP COLUMN directly, so we recreate the table
CREATE TABLE menus_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO menus_new (id, name, slug, created_at, updated_at)
SELECT id, name, slug, created_at, updated_at FROM menus;

DROP TABLE menus;
ALTER TABLE menus_new RENAME TO menus;

CREATE INDEX idx_menus_slug ON menus(slug);
