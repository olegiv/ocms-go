-- name: UpsertOAuthClient :exec
INSERT INTO oauth_clients (id, name, redirect_uris, source, expires_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET name = excluded.name, redirect_uris = excluded.redirect_uris,
    expires_at = excluded.expires_at WHERE oauth_clients.source = excluded.source;

-- name: GetOAuthClient :one
SELECT * FROM oauth_clients WHERE id = ?;

-- name: CountOAuthClients :one
SELECT COUNT(*) FROM oauth_clients;

-- name: CountOAuthClientsBySource :one
SELECT COUNT(*) FROM oauth_clients WHERE source = ?;

-- name: DeleteExpiredUnusedOAuthClients :exec
DELETE FROM oauth_clients WHERE expires_at < ?
AND NOT EXISTS (SELECT 1 FROM oauth_grants WHERE client_id = oauth_clients.id);

-- Successful authorization-code exchange gives a DCR client the full
-- lifetime. Consent alone leaves the registration short-lived.
-- name: ExtendDCROAuthClient :exec
UPDATE oauth_clients SET expires_at = sqlc.arg(expires_at)
WHERE id = sqlc.arg(id) AND source = 'dcr' AND expires_at < sqlc.arg(expires_at);

-- Check both states together so concurrent redemption cannot hide a replay.
-- name: CanExchangeCodeForDCRClient :one
SELECT CAST(EXISTS (
    SELECT 1 FROM oauth_clients o
    WHERE o.id = sqlc.arg(client_id) AND o.source = 'dcr'
      AND (o.expires_at > sqlc.arg(now) OR EXISTS (
          SELECT 1 FROM oauth_codes c JOIN oauth_grants g ON g.id = c.grant_id
          WHERE c.code_hash = sqlc.arg(code_hash) AND g.client_id = o.id
            AND c.consumed_at = 0 AND c.expires_at > sqlc.arg(now)
            AND g.revoked_at = 0 AND g.expires_at > sqlc.arg(now)
      ))
) AS BOOLEAN);

-- name: CreateOAuthGrant :one
INSERT INTO oauth_grants (id, user_id, client_id, scopes, resource, session_version, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetOAuthGrant :one
SELECT * FROM oauth_grants WHERE id = ?;

-- name: ListOAuthGrantsForUser :many
SELECT g.*, c.name AS client_name FROM oauth_grants g
JOIN oauth_clients c ON c.id = g.client_id
WHERE g.user_id = ? AND g.revoked_at = 0 AND g.expires_at > ?
AND EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id)
ORDER BY g.created_at DESC LIMIT 100;

-- name: RevokeOAuthGrant :exec
UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at = 0;

-- name: RevokeOAuthGrantByAccess :execrows
UPDATE oauth_grants SET revoked_at = unixepoch()
WHERE oauth_grants.id = sqlc.arg(grant_id) AND revoked_at = 0
AND EXISTS (
    SELECT 1 FROM oauth_tokens
    WHERE oauth_tokens.id = sqlc.arg(token_id) AND oauth_tokens.grant_id = oauth_grants.id
    AND access_revoked_at = 0 AND access_expires_at > unixepoch()
);

-- name: RevokeOwnedOAuthGrant :execrows
UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at = 0;

-- name: TouchOAuthGrant :exec
UPDATE oauth_grants SET last_used_at = ? WHERE id = ?;

-- name: TouchOAuthGrantIfDue :exec
UPDATE oauth_grants SET last_used_at = sqlc.arg(last_used_at)
WHERE id = sqlc.arg(id) AND revoked_at = 0 AND last_used_at < sqlc.arg(before_time);

-- name: CreateOAuthCode :exec
INSERT INTO oauth_codes (code_hash, grant_id, redirect_uri, challenge, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetOAuthCode :one
SELECT * FROM oauth_codes WHERE code_hash = ?;

-- name: ConsumeOAuthCode :execrows
UPDATE oauth_codes SET consumed_at = ? WHERE code_hash = ? AND consumed_at = 0;

-- name: CreateOAuthToken :exec
INSERT INTO oauth_tokens (id, grant_id, access_hash, refresh_hash, scopes, created_at, access_expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetOAuthTokenByAccess :one
SELECT * FROM oauth_tokens WHERE access_hash = ?;

-- name: GetOAuthTokenByID :one
SELECT * FROM oauth_tokens WHERE id = ?;

-- name: GetOAuthTokenByRefresh :one
SELECT * FROM oauth_tokens WHERE refresh_hash = ?;

-- name: ConsumeOAuthRefresh :execrows
UPDATE oauth_tokens SET refresh_used_at = ?, access_revoked_at = ?
WHERE id = ? AND refresh_used_at = 0;
