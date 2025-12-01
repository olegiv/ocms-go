-- +goose Up

-- Add language_id to categories
ALTER TABLE categories ADD COLUMN language_id INTEGER REFERENCES languages(id);

-- Set existing categories to default language
UPDATE categories SET language_id = (SELECT id FROM languages WHERE is_default = 1);

-- Create index for language filtering
CREATE INDEX idx_categories_language_id ON categories(language_id);

-- Add language_id to tags
ALTER TABLE tags ADD COLUMN language_id INTEGER REFERENCES languages(id);

-- Set existing tags to default language
UPDATE tags SET language_id = (SELECT id FROM languages WHERE is_default = 1);

-- Create index for language filtering
CREATE INDEX idx_tags_language_id ON tags(language_id);

-- +goose Down
DROP INDEX IF EXISTS idx_tags_language_id;
DROP INDEX IF EXISTS idx_categories_language_id;

-- SQLite doesn't support DROP COLUMN directly in older versions
-- The columns will remain but without indexes for simplicity
-- In production, a proper migration would recreate the tables
