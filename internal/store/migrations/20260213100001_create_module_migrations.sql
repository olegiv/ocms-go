-- +goose Up
-- Module migrations tracking table.
-- Previously created at runtime via DDL in module/registry.go.
CREATE TABLE IF NOT EXISTS module_migrations (
    module TEXT NOT NULL,
    version INTEGER NOT NULL,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (module, version)
);

-- +goose Down
DROP TABLE IF EXISTS module_migrations;
