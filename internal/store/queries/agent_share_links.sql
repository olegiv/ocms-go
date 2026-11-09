-- name: ListActiveAgentPreviewLinks :many
-- Share links an AI client issued (through an OAuth grant or API key) that
-- are still usable, for the MCP admin page.
SELECT l.id, l.page_id, l.revision, l.created_at, l.expires_at,
       CAST(l.issuing_grant_id IS NOT NULL AS INTEGER) AS via_oauth,
       p.title AS page_title, u.name AS creator_name, u.email AS creator_email
FROM preview_links l
JOIN pages p ON p.id = l.page_id
JOIN users u ON u.id = l.created_by
WHERE l.revoked_at = 0 AND l.expires_at > ?
  AND (l.issuing_grant_id IS NOT NULL OR l.issuing_key_id IS NOT NULL)
ORDER BY l.id DESC
LIMIT 100;

-- name: RevokeAgentPreviewLink :execrows
UPDATE preview_links SET revoked_at = ?
WHERE id = ? AND revoked_at = 0
  AND (issuing_grant_id IS NOT NULL OR issuing_key_id IS NOT NULL);
