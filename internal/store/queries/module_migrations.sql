-- name: IsModuleMigrationApplied :one
SELECT COUNT(*) FROM module_migrations
WHERE module = ? AND version = ?;

-- name: RecordModuleMigration :exec
INSERT INTO module_migrations (module, version, applied_at)
VALUES (?, ?, ?);
