-- +goose Up
-- +goose StatementBegin
ALTER TABLE pages ADD COLUMN video_title TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pages DROP COLUMN video_title;
-- +goose StatementEnd
