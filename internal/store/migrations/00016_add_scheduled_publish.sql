-- +goose Up
-- +goose StatementBegin
ALTER TABLE pages ADD COLUMN scheduled_at DATETIME;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_pages_scheduled ON pages(scheduled_at) WHERE scheduled_at IS NOT NULL AND status = 'draft';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_pages_scheduled;
-- +goose StatementEnd

-- Note: SQLite doesn't support DROP COLUMN easily, would need table rebuild
