-- +goose Up
ALTER TABLE pages ADD COLUMN summary TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE pages DROP COLUMN summary;
