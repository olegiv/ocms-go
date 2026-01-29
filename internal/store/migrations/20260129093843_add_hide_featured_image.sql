-- +goose Up
-- +goose StatementBegin
ALTER TABLE pages ADD COLUMN hide_featured_image INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pages DROP COLUMN hide_featured_image;
-- +goose StatementEnd
