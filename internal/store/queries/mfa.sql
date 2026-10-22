-- name: GetUserTOTP :one
SELECT * FROM user_totp WHERE user_id = ?;

-- name: UpsertUserTOTP :exec
INSERT INTO user_totp (user_id, secret_enc, last_step, enrolled_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET
    secret_enc = excluded.secret_enc,
    last_step = excluded.last_step,
    enrolled_at = excluded.enrolled_at,
    last_used_at = NULL;

-- name: AdvanceUserTOTPStep :execrows
-- Records an accepted time step. The guard makes a code single-use even when
-- two requests verify it at once: only the first moves last_step past it.
UPDATE user_totp SET last_step = sqlc.arg(step), last_used_at = sqlc.arg(used_at)
WHERE user_id = sqlc.arg(user_id) AND last_step < sqlc.arg(step);

-- name: DeleteUserTOTP :exec
DELETE FROM user_totp WHERE user_id = ?;

-- name: CreateRecoveryCode :exec
INSERT INTO user_recovery_codes (user_id, code_hash, created_at) VALUES (?, ?, ?);

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes WHERE user_id = ?;

-- name: UseRecoveryCode :execrows
UPDATE user_recovery_codes SET used_at = ?
WHERE user_id = ? AND code_hash = ? AND used_at IS NULL;

-- name: CountUnusedRecoveryCodes :one
SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ? AND used_at IS NULL;

-- name: BumpUserSessionVersion :one
-- Invalidates every existing session for the user. Use it when the user's
-- second factor changes, the way UpdateUserPassword does for the password.
UPDATE users SET session_version = session_version + 1, updated_at = ?
WHERE id = ?
RETURNING session_version;
