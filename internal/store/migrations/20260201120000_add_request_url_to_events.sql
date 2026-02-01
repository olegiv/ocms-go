-- +goose Up
-- +goose StatementBegin
ALTER TABLE events ADD COLUMN request_url TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE events DROP COLUMN request_url;
-- +goose StatementEnd
