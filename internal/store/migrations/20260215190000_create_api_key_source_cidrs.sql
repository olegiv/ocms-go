-- +goose Up
CREATE TABLE IF NOT EXISTS api_key_source_cidrs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    api_key_id INTEGER NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    cidr TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(api_key_id, cidr)
);

CREATE INDEX IF NOT EXISTS idx_api_key_source_cidrs_key ON api_key_source_cidrs(api_key_id);

-- +goose Down
DROP INDEX IF EXISTS idx_api_key_source_cidrs_key;
DROP TABLE IF EXISTS api_key_source_cidrs;
