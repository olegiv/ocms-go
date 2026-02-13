-- +goose Up
-- Scheduler overrides table for cron schedule customization.
-- Previously created at runtime via DDL in scheduler/registry.go.
CREATE TABLE IF NOT EXISTS scheduler_overrides (
    source TEXT NOT NULL,
    name TEXT NOT NULL,
    override_schedule TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source, name)
);

-- +goose Down
DROP TABLE IF EXISTS scheduler_overrides;
