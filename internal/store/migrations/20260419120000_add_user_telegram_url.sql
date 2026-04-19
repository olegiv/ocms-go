-- +goose Up
ALTER TABLE users ADD COLUMN telegram_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN telegram_url;
