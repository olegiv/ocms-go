-- +goose Up
-- +goose StatementBegin
CREATE TABLE redirects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_path TEXT NOT NULL,
    target_url TEXT NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 301,
    is_wildcard BOOLEAN NOT NULL DEFAULT 0,
    target_type TEXT NOT NULL DEFAULT '_self',
    enabled BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Unique constraint on source_path to prevent duplicate redirects
CREATE UNIQUE INDEX idx_redirects_source_path ON redirects(source_path);

-- Index for efficient lookups of enabled redirects
CREATE INDEX idx_redirects_enabled ON redirects(enabled);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_redirects_enabled;
DROP INDEX IF EXISTS idx_redirects_source_path;
DROP TABLE IF EXISTS redirects;
-- +goose StatementEnd
