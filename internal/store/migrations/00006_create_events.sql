-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT NOT NULL DEFAULT 'info',
    category TEXT NOT NULL DEFAULT 'system',
    message TEXT NOT NULL,
    user_id INTEGER,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_events_level ON events(level);
CREATE INDEX idx_events_category ON events(category);
CREATE INDEX idx_events_user_id ON events(user_id);
CREATE INDEX idx_events_created_at ON events(created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_events_created_at;
DROP INDEX IF EXISTS idx_events_user_id;
DROP INDEX IF EXISTS idx_events_category;
DROP INDEX IF EXISTS idx_events_level;
DROP TABLE IF EXISTS events;
-- +goose StatementEnd
