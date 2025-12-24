-- +goose Up
-- Media translations for multi-language alt text and captions
CREATE TABLE media_translations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    language_id INTEGER NOT NULL REFERENCES languages(id) ON DELETE CASCADE,
    alt TEXT NOT NULL DEFAULT '',
    caption TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(media_id, language_id)
);

CREATE INDEX idx_media_translations_media_id ON media_translations(media_id);

-- +goose Down
DROP INDEX IF EXISTS idx_media_translations_media_id;
DROP TABLE IF EXISTS media_translations;
