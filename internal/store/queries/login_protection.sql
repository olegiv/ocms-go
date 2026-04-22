-- name: GetLoginProtection :one
SELECT email, attempt_count, first_failed_at, locked_until, lockout_count, updated_at
FROM login_protection
WHERE email = ?;

-- name: UpsertLoginProtection :exec
INSERT INTO login_protection (email, attempt_count, first_failed_at, locked_until, lockout_count, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(email) DO UPDATE SET
    attempt_count   = excluded.attempt_count,
    first_failed_at = excluded.first_failed_at,
    locked_until    = excluded.locked_until,
    lockout_count   = excluded.lockout_count,
    updated_at      = excluded.updated_at;

-- name: DeleteLoginProtection :exec
DELETE FROM login_protection WHERE email = ?;

-- name: CleanupStaleLoginProtection :exec
-- Remove rows whose lockout has expired (or was never set) and whose attempt
-- window has also passed. Called periodically by the cleanup goroutine.
DELETE FROM login_protection
WHERE (locked_until IS NULL OR locked_until < ?)
  AND first_failed_at < ?;
