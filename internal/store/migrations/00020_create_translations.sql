-- +goose Up
CREATE TABLE translations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,
    entity_id INTEGER NOT NULL,
    language_id INTEGER NOT NULL REFERENCES languages(id) ON DELETE CASCADE,
    translation_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(entity_type, entity_id, language_id)
);

CREATE INDEX idx_translations_entity ON translations(entity_type, entity_id);
CREATE INDEX idx_translations_language ON translations(language_id);
CREATE INDEX idx_translations_target ON translations(entity_type, translation_id);

-- Add language_id to pages
ALTER TABLE pages ADD COLUMN language_id INTEGER REFERENCES languages(id);

-- Set existing pages to default language
UPDATE pages SET language_id = (SELECT id FROM languages WHERE is_default = 1);

-- Create index for language filtering
CREATE INDEX idx_pages_language_id ON pages(language_id);

-- +goose Down
DROP INDEX IF EXISTS idx_pages_language_id;

-- SQLite doesn't support DROP COLUMN directly in older versions
-- We need to recreate the table without language_id
-- For simplicity in development, we'll just drop the translations table
-- In production, a proper migration would recreate the pages table

DROP INDEX IF EXISTS idx_translations_target;
DROP INDEX IF EXISTS idx_translations_language;
DROP INDEX IF EXISTS idx_translations_entity;
DROP TABLE IF EXISTS translations;
