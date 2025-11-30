-- +goose Up
ALTER TABLE pages ADD COLUMN meta_title TEXT NOT NULL DEFAULT '';
ALTER TABLE pages ADD COLUMN meta_description TEXT NOT NULL DEFAULT '';
ALTER TABLE pages ADD COLUMN meta_keywords TEXT NOT NULL DEFAULT '';
ALTER TABLE pages ADD COLUMN og_image_id INTEGER REFERENCES media(id) ON DELETE SET NULL;
ALTER TABLE pages ADD COLUMN no_index INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pages ADD COLUMN no_follow INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pages ADD COLUMN canonical_url TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN easily in older versions
-- For SQLite 3.35.0+ we can use ALTER TABLE DROP COLUMN
-- For compatibility, we'll just leave the columns (they won't hurt anything)
