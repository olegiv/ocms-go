-- +goose Up
-- Persist failed-login and lockout state across restarts so a deploy, crash,
-- or OOM kill cannot be used to reset a brute-force window. Schema mirrors
-- the former in-memory loginAttempt struct in internal/middleware/login_protection.go.
CREATE TABLE IF NOT EXISTS login_protection (
    email            TEXT PRIMARY KEY,
    attempt_count    INTEGER  NOT NULL DEFAULT 0,
    first_failed_at  DATETIME NOT NULL,
    locked_until     DATETIME,
    lockout_count    INTEGER  NOT NULL DEFAULT 0,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Cleanup sweeps by locked_until/first_failed_at.
CREATE INDEX IF NOT EXISTS idx_login_protection_locked_until ON login_protection(locked_until);
CREATE INDEX IF NOT EXISTS idx_login_protection_first_failed_at ON login_protection(first_failed_at);

-- +goose Down
DROP INDEX IF EXISTS idx_login_protection_first_failed_at;
DROP INDEX IF EXISTS idx_login_protection_locked_until;
DROP TABLE IF EXISTS login_protection;
