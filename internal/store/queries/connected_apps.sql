-- These projections deliberately omit credential hashes, lookups and prefixes.
-- all_users is set only after checking the current actor's admin role.
-- A grant exists from consent, before any token does. One that never issued a
-- token is not a connection yet (it may never become one), so it is not listed;
-- revocation by ID still reaches it, and the retention job removes it.
-- name: ListConnectedOAuthGrants :many
SELECT g.id, g.user_id, u.name AS owner_name, u.role AS owner_role,
    u.session_version AS owner_session_version, c.name AS client_name,
    g.client_id, g.scopes, g.session_version, g.created_at, g.expires_at,
    g.revoked_at, g.last_used_at
FROM oauth_grants g
JOIN oauth_clients c ON c.id = g.client_id
JOIN users u ON u.id = g.user_id
WHERE (sqlc.arg(all_users) = 1 OR g.user_id = sqlc.arg(user_id))
AND EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id)
ORDER BY g.created_at DESC, g.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListConnectedAPIKeys :many
SELECT k.id, k.name, k.permissions, k.last_used_at, k.expires_at, k.is_active,
    k.created_by, k.created_at, u.name AS owner_name, u.role AS owner_role
FROM api_keys k JOIN users u ON u.id = k.created_by
WHERE (sqlc.arg(all_users) = 1 OR k.created_by = sqlc.arg(user_id))
AND EXISTS (
    SELECT 1 FROM json_each(CASE WHEN json_valid(k.permissions)
        AND json_type(k.permissions) = 'array' THEN k.permissions ELSE '[]' END)
    WHERE type = 'text' AND value = 'mcp:access'
)
AND NOT EXISTS (
    SELECT 1 FROM json_each(CASE WHEN json_valid(k.permissions)
        AND json_type(k.permissions) = 'array' THEN k.permissions ELSE '[]' END)
    WHERE type != 'text'
)
ORDER BY k.created_at DESC, k.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: RevokeConnectedOAuthGrant :execrows
UPDATE oauth_grants
SET revoked_at = CASE WHEN revoked_at = 0 THEN sqlc.arg(revoked_at) ELSE revoked_at END
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_users) = 1 OR user_id = sqlc.arg(user_id));

-- name: RevokeConnectedAPIKey :execrows
UPDATE api_keys SET is_active = 0, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_users) = 1 OR created_by = sqlc.arg(user_id))
AND EXISTS (
    SELECT 1 FROM json_each(CASE WHEN json_valid(permissions)
        AND json_type(permissions) = 'array' THEN permissions ELSE '[]' END)
    WHERE type = 'text' AND value = 'mcp:access'
)
AND NOT EXISTS (
    SELECT 1 FROM json_each(CASE WHEN json_valid(permissions)
        AND json_type(permissions) = 'array' THEN permissions ELSE '[]' END)
    WHERE type != 'text'
);
