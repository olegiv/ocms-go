-- +goose Up
-- +goose StatementBegin
CREATE TABLE config_translations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    config_key TEXT NOT NULL,
    language_id INTEGER NOT NULL REFERENCES languages(id) ON DELETE CASCADE,
    value TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE(config_key, language_id)
);

CREATE INDEX idx_config_translations_key ON config_translations(config_key);
CREATE INDEX idx_config_translations_language ON config_translations(language_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_config_translations_language;
DROP INDEX IF EXISTS idx_config_translations_key;
DROP TABLE IF EXISTS config_translations;
-- +goose StatementEnd
