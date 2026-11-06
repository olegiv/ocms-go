-- name: CreatePreviewLink :one
INSERT INTO preview_links(token_hash,page_id,revision,created_by,session_version,issuing_grant_id,issuing_key_id,created_at,expires_at)
VALUES(?,?,?,?,?,?,?,?,?) RETURNING *;

-- name: GetPreviewLinkByHash :one
SELECT l.* FROM preview_links l
JOIN pages p ON p.id=l.page_id
JOIN users u ON u.id=l.created_by
JOIN page_ai_access a ON a.page_id=p.id
WHERE l.token_hash=?1 AND l.revoked_at=0 AND l.expires_at>?2
AND p.author_id=u.id AND p.status='draft' AND p.scheduled_at IS NULL AND a.ai_editable=1
AND u.role IN ('admin','editor') AND u.session_version=l.session_version
AND (l.issuing_grant_id IS NULL OR EXISTS (
    SELECT 1 FROM oauth_grants g WHERE g.id=l.issuing_grant_id AND g.user_id=u.id
    AND g.revoked_at=0 AND g.expires_at>?2 AND g.session_version=u.session_version
    AND instr(' '||g.scopes||' ',' mcp:access ')>0 AND instr(' '||g.scopes||' ',' mcp:drafts ')>0
))
AND (l.issuing_key_id IS NULL OR EXISTS (
    SELECT 1 FROM api_keys k WHERE k.id=l.issuing_key_id AND k.created_by=u.id AND k.is_active=1
    AND (k.expires_at IS NULL OR unixepoch(k.expires_at)>?2)
    AND (SELECT COUNT(DISTINCT value) FROM json_each(CASE WHEN json_valid(k.permissions) THEN k.permissions ELSE '[]' END)
         WHERE value IN ('mcp:access','mcp:drafts'))=2
));

-- name: CountActivePreviewLinks :one
SELECT COUNT(*) FROM preview_links WHERE created_by=? AND revoked_at=0 AND expires_at>?;

-- name: CountIssuedPreviewLinks :one
SELECT COUNT(*) FROM preview_links WHERE created_by=? AND created_at>=?;

-- name: GetPreviewIssuanceCutoff :one
SELECT created_at FROM preview_links
WHERE created_by=sqlc.arg(created_by) AND created_at>=sqlc.arg(since)
ORDER BY created_at DESC,id DESC LIMIT 1 OFFSET sqlc.arg(issuance_offset);

-- name: GetLatestAuthoringProvenance :one
SELECT * FROM content_provenance WHERE page_id=? AND revision<=?
AND json_extract(CASE WHEN json_valid(validated_json) THEN validated_json ELSE '{}' END,'$.prompt_contract_version') IS NOT NULL
ORDER BY revision DESC LIMIT 1;

-- name: ListPagePreviewLinks :many
SELECT id,page_id,revision,created_by,created_at,expires_at,revoked_at
FROM preview_links WHERE page_id=? AND revoked_at=0 AND expires_at>? ORDER BY id DESC LIMIT 20;

-- name: RevokePagePreviewLink :execrows
UPDATE preview_links SET revoked_at=? WHERE id=? AND page_id=? AND revoked_at=0;

-- name: ListClaimChecks :many
SELECT c.*,COALESCE(u.name,'') AS checked_by_name FROM claims_checks c LEFT JOIN users u ON u.id=c.checked_by
WHERE c.provenance_id=? ORDER BY c.claim_index;

-- name: CheckClaim :exec
INSERT INTO claims_checks(provenance_id,claim_index,checked_by,checked_at) VALUES(?,?,?,?)
ON CONFLICT(provenance_id,claim_index) DO UPDATE SET checked_by=excluded.checked_by,checked_at=excluded.checked_at;

-- name: UncheckClaim :exec
DELETE FROM claims_checks WHERE provenance_id=? AND claim_index=?;

-- name: GetPageReviewState :one
SELECT p.id,COALESCE(a.ai_editable,0) AS ai_editable,
CAST(MAX(0,COALESCE(json_array_length(CASE WHEN json_valid(c.reported_json) THEN c.reported_json ELSE '{}' END,'$.agent.claims_to_verify'),
 json_array_length(CASE WHEN json_valid(c.reported_json) THEN c.reported_json ELSE '{}' END,'$.claims_to_verify'),0)
 -(SELECT COUNT(*) FROM claims_checks cc WHERE cc.provenance_id=c.id)) AS INTEGER) AS unchecked_claims
FROM pages p LEFT JOIN page_ai_access a ON a.page_id=p.id
LEFT JOIN content_provenance c ON c.page_id=p.id AND c.revision=p.revision WHERE p.id=?;

-- name: GetPreviousPageVersion :one
SELECT * FROM page_versions WHERE page_id=? AND id<? ORDER BY id DESC LIMIT 1;
