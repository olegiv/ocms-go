-- +goose Up
-- +goose StatementBegin
ALTER TABLE pages ADD COLUMN featured_image_id INTEGER REFERENCES media(id) ON DELETE SET NULL;
-- +goose StatementEnd

CREATE INDEX idx_pages_featured_image ON pages(featured_image_id);

-- +goose Down
DROP INDEX IF EXISTS idx_pages_featured_image;

-- SQLite doesn't support DROP COLUMN directly, so we need to recreate the table
-- For simplicity in dev, we'll just note this limitation
-- In production, you'd need to copy data to a new table
