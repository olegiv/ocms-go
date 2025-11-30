-- +goose Up
CREATE TABLE languages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    native_name TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    direction TEXT NOT NULL DEFAULT 'ltr',
    position INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_languages_code ON languages(code);
CREATE INDEX idx_languages_active ON languages(is_active);
CREATE INDEX idx_languages_default ON languages(is_default);

-- Seed default language
INSERT INTO languages (code, name, native_name, is_default, is_active, direction, position)
VALUES ('en', 'English', 'English', 1, 1, 'ltr', 0);

-- +goose Down
DROP INDEX idx_languages_default;
DROP INDEX idx_languages_active;
DROP INDEX idx_languages_code;
DROP TABLE languages;
