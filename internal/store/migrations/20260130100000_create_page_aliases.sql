-- +goose Up
-- +goose StatementBegin
CREATE TABLE page_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    alias TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Unique constraint on alias to prevent duplicate aliases across all pages
CREATE UNIQUE INDEX idx_page_aliases_alias ON page_aliases(alias);

-- Index for efficient lookups by page
CREATE INDEX idx_page_aliases_page_id ON page_aliases(page_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_page_aliases_page_id;
DROP INDEX IF EXISTS idx_page_aliases_alias;
DROP TABLE IF EXISTS page_aliases;
-- +goose StatementEnd
