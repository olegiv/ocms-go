-- Scheduled Tasks

-- name: CreateScheduledTask :one
INSERT INTO scheduled_tasks (name, url, schedule, is_active, timeout_seconds, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetScheduledTask :one
SELECT * FROM scheduled_tasks WHERE id = ?;

-- name: ListScheduledTasks :many
SELECT * FROM scheduled_tasks ORDER BY name;

-- name: ListActiveScheduledTasks :many
SELECT * FROM scheduled_tasks WHERE is_active = 1 ORDER BY name;

-- name: UpdateScheduledTask :one
UPDATE scheduled_tasks SET name = ?, url = ?, schedule = ?, timeout_seconds = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: ToggleScheduledTask :one
UPDATE scheduled_tasks SET is_active = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteScheduledTask :exec
DELETE FROM scheduled_tasks WHERE id = ?;

-- Scheduled Task Runs

-- name: CreateScheduledTaskRun :one
INSERT INTO scheduled_task_runs (task_id, status, started_at)
VALUES (?, 'pending', ?)
RETURNING *;

-- name: UpdateScheduledTaskRunSuccess :exec
UPDATE scheduled_task_runs
SET status = 'success', status_code = ?, response_body = ?, duration_ms = ?, completed_at = ?
WHERE id = ?;

-- name: UpdateScheduledTaskRunFailed :exec
UPDATE scheduled_task_runs
SET status = 'failed', error_message = ?, duration_ms = ?, completed_at = ?
WHERE id = ?;

-- name: ListScheduledTaskRuns :many
SELECT * FROM scheduled_task_runs
WHERE task_id = ?
ORDER BY started_at DESC
LIMIT ? OFFSET ?;

-- name: CountScheduledTaskRuns :one
SELECT COUNT(*) FROM scheduled_task_runs WHERE task_id = ?;

-- name: DeleteOldTaskRuns :exec
DELETE FROM scheduled_task_runs WHERE task_id = ? AND started_at < ?;

-- name: DeleteAllOldTaskRuns :exec
DELETE FROM scheduled_task_runs WHERE started_at < ?;
