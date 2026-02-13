-- name: GetSchedulerOverride :one
SELECT override_schedule FROM scheduler_overrides
WHERE source = ? AND name = ?;

-- name: UpsertSchedulerOverride :exec
INSERT INTO scheduler_overrides (source, name, override_schedule, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(source, name) DO UPDATE SET
    override_schedule = excluded.override_schedule,
    updated_at = CURRENT_TIMESTAMP;

-- name: DeleteSchedulerOverride :exec
DELETE FROM scheduler_overrides WHERE source = ? AND name = ?;
