-- +goose Up
CREATE TABLE modules (
    name TEXT PRIMARY KEY,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_modules_active ON modules(is_active);

-- +goose Down
DROP INDEX idx_modules_active;
DROP TABLE modules;
