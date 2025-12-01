-- +goose Up
-- Make menu slug unique per language instead of globally unique
-- This allows the same menu slug (e.g., "main") for different languages

-- SQLite doesn't support ALTER COLUMN, so we need to recreate the table
-- First, drop the existing unique index on slug (if it exists as an index)
DROP INDEX IF EXISTS idx_menus_slug;

-- Recreate the menus table without the UNIQUE constraint on slug
CREATE TABLE menus_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER REFERENCES languages(id)
);

-- Copy existing data
INSERT INTO menus_new (id, name, slug, created_at, updated_at, language_id)
SELECT id, name, slug, created_at, updated_at, language_id FROM menus;

-- Drop old table
DROP TABLE menus;

-- Rename new table
ALTER TABLE menus_new RENAME TO menus;

-- Create indexes
CREATE INDEX idx_menus_language_id ON menus(language_id);
CREATE UNIQUE INDEX idx_menus_slug_language ON menus(slug, language_id);

-- +goose Down
-- Recreate original table structure with UNIQUE constraint on slug
CREATE TABLE menus_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    language_id INTEGER REFERENCES languages(id)
);

INSERT INTO menus_old (id, name, slug, created_at, updated_at, language_id)
SELECT id, name, slug, created_at, updated_at, language_id FROM menus;

DROP TABLE menus;
ALTER TABLE menus_old RENAME TO menus;

CREATE INDEX idx_menus_language_id ON menus(language_id);
CREATE INDEX idx_menus_slug ON menus(slug);
