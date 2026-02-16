-- +goose Up
CREATE TABLE IF NOT EXISTS api_key_source_state (
    api_key_id INTEGER PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    last_ip TEXT NOT NULL,
    last_seen_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_api_key_source_state_seen_at ON api_key_source_state(last_seen_at);

-- +goose Down
DROP INDEX IF EXISTS idx_api_key_source_state_seen_at;
DROP TABLE IF EXISTS api_key_source_state;
