-- +goose Up
-- +goose StatementBegin
ALTER TABLE pages ADD COLUMN video_url TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pages DROP COLUMN video_url;
-- +goose StatementEnd
