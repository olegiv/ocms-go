-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS widgets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    theme TEXT NOT NULL,
    area TEXT NOT NULL,
    widget_type TEXT NOT NULL,
    title TEXT,
    content TEXT,
    settings TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_widgets_theme_area ON widgets(theme, area);
CREATE INDEX idx_widgets_position ON widgets(position);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_widgets_position;
DROP INDEX IF EXISTS idx_widgets_theme_area;
DROP TABLE IF EXISTS widgets;
-- +goose StatementEnd
