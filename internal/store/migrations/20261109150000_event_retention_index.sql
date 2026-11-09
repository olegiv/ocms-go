-- +goose Up
CREATE INDEX idx_events_category_created_at ON events(category, created_at);

-- +goose Down
DROP INDEX idx_events_category_created_at;
