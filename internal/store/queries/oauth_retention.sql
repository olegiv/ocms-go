-- name: DeleteRetainedOAuthCodes :execrows
DELETE FROM oauth_codes WHERE code_hash IN (
    SELECT c.code_hash FROM oauth_codes c WHERE c.expires_at <= sqlc.arg(cutoff)
    ORDER BY c.expires_at, c.code_hash LIMIT sqlc.arg(batch_limit)
);

-- Never prune a token from an active family, even if its access has expired
-- or its refresh was spent: that hash is still required for replay detection.
-- name: DeleteRetiredOAuthTokens :execrows
DELETE FROM oauth_tokens WHERE id IN (
    SELECT t.id FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id
    WHERE g.expires_at <= sqlc.arg(cutoff)
        OR (g.revoked_at > 0 AND g.revoked_at <= sqlc.arg(cutoff))
    ORDER BY t.id LIMIT sqlc.arg(batch_limit)
);

-- Drain dependent previews explicitly so family deletion cannot cascade
-- through an unbounded number of historical share records.
-- name: DeleteRetiredOAuthPreviews :execrows
DELETE FROM preview_links WHERE id IN (
    SELECT p.id FROM preview_links p JOIN oauth_grants g ON g.id = p.issuing_grant_id
    WHERE g.expires_at <= sqlc.arg(cutoff)
        OR (g.revoked_at > 0 AND g.revoked_at <= sqlc.arg(cutoff))
    ORDER BY p.id LIMIT sqlc.arg(batch_limit)
);

-- name: DeleteRetiredOAuthGrants :execrows
DELETE FROM oauth_grants WHERE id IN (
    SELECT g.id FROM oauth_grants g
    WHERE (g.expires_at <= sqlc.arg(cutoff)
        OR (g.revoked_at > 0 AND g.revoked_at <= sqlc.arg(cutoff)))
    AND NOT EXISTS (SELECT 1 FROM oauth_codes c WHERE c.grant_id = g.id)
    AND NOT EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id)
    AND NOT EXISTS (SELECT 1 FROM preview_links p WHERE p.issuing_grant_id = g.id)
    ORDER BY g.id LIMIT sqlc.arg(batch_limit)
);

-- A grant is created at consent, before any token exists. One that never issued
-- a token and is older than the cutoff will never be redeemed (its code lived
-- 60 seconds), so it goes. Its single code row follows by cascade, which is
-- why the batch limit still bounds the work. A grant with a token, or one that
-- issued a preview link, is never touched here.
-- name: DeleteUnredeemedOAuthGrants :execrows
DELETE FROM oauth_grants WHERE id IN (
    SELECT g.id FROM oauth_grants g
    WHERE g.created_at <= sqlc.arg(cutoff)
    AND NOT EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id)
    AND NOT EXISTS (SELECT 1 FROM preview_links p WHERE p.issuing_grant_id = g.id)
    ORDER BY g.created_at, g.id LIMIT sqlc.arg(batch_limit)
);

-- name: DeleteRetainedOAuthClients :execrows
DELETE FROM oauth_clients WHERE id IN (
    SELECT c.id FROM oauth_clients c WHERE c.expires_at <= sqlc.arg(cutoff)
    AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = c.id)
    ORDER BY c.expires_at, c.id LIMIT sqlc.arg(batch_limit)
);
