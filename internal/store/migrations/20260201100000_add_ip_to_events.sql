-- +goose Up
-- +goose StatementBegin
ALTER TABLE events ADD COLUMN ip_address TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE events DROP COLUMN ip_address;
-- +goose StatementEnd
