-- +goose Up
-- +goose StatementBegin
CREATE TABLE scheduled_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    schedule TEXT NOT NULL,
    is_active INTEGER NOT NULL DEFAULT 1,
    timeout_seconds INTEGER NOT NULL DEFAULT 30,
    created_by INTEGER REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE scheduled_task_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    status_code INTEGER,
    response_body TEXT,
    error_message TEXT,
    duration_ms INTEGER,
    started_at DATETIME NOT NULL,
    completed_at DATETIME
);

CREATE INDEX idx_task_runs_task_id ON scheduled_task_runs(task_id);
CREATE INDEX idx_task_runs_started_at ON scheduled_task_runs(started_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS scheduled_task_runs;
DROP TABLE IF EXISTS scheduled_tasks;
-- +goose StatementEnd
