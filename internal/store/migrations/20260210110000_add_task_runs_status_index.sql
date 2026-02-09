-- +goose Up
CREATE INDEX IF NOT EXISTS idx_task_runs_status ON scheduled_task_runs(status);

-- +goose Down
DROP INDEX IF EXISTS idx_task_runs_status;
