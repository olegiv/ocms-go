-- +goose Up
-- +goose StatementBegin
ALTER TABLE pages ADD COLUMN page_type TEXT NOT NULL DEFAULT 'post';
ALTER TABLE pages ADD COLUMN exclude_from_lists INTEGER NOT NULL DEFAULT 0;
UPDATE pages SET page_type = 'post';
CREATE INDEX idx_pages_page_type ON pages(page_type);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_pages_page_type;
ALTER TABLE pages DROP COLUMN exclude_from_lists;
ALTER TABLE pages DROP COLUMN page_type;
-- +goose StatementEnd
