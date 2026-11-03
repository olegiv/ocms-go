-- Revision retention for AI drafts. An AI draft is exactly what ListOwnAIDrafts
-- selects, for any owner: AI access on, status draft, not scheduled. Published,
-- scheduled and non-AI pages are never listed here.
--
-- Eligible: complete revisions older than the newest `keep` revision numbers,
-- except revisions that something still reads:
--   * the current revision (covered by the age rule; the join keeps it explicit);
--   * a revision pinned by an unrevoked, unexpired preview link;
--   * the newest revision whose evidence carries a prompt contract version, or a
--     translation basis: the lookups behind publication validation and the
--     translation status read "the latest such revision at or before current";
--   * a revision recorded as a reference by the current evidence of an AI draft,
--     because inheriting references re-reads that revision's body.
-- Legacy title/body rows have no revision number and are never selected.
-- Only revisions an agent or API key wrote (provenance source api or oauth)
-- are removed: an editor's saves and system revisions (a scheduled or manual
-- publication, a translation link) are history the site owns, also on a page
-- whose AI access was switched on later or that went back to draft.
-- name: ListPrunableAIDraftRevisions :many
WITH active_pins AS (
    SELECT l.page_id, l.revision FROM preview_links l
    WHERE l.revoked_at = 0 AND l.expires_at > sqlc.arg(unix_now)
)
SELECT pv.id AS version_id, pv.page_id, pv.revision AS revision
FROM page_versions pv
JOIN pages p ON p.id = pv.page_id
JOIN page_ai_access a ON a.page_id = p.id AND a.ai_editable = 1
WHERE p.status = 'draft' AND p.scheduled_at IS NULL
AND pv.legacy = 0 AND pv.revision IS NOT NULL
AND pv.revision <= p.revision - sqlc.arg(keep)
AND EXISTS (
    SELECT 1 FROM content_provenance w
    WHERE w.page_id = pv.page_id AND w.revision = pv.revision AND w.source IN ('api', 'oauth')
)
AND NOT EXISTS (SELECT 1 FROM active_pins pin WHERE pin.page_id = pv.page_id AND pin.revision = pv.revision)
AND pv.revision IS NOT (
    SELECT MAX(c.revision) FROM content_provenance c
    WHERE c.page_id = p.id AND c.revision <= p.revision
    AND json_extract(CASE WHEN json_valid(c.validated_json) THEN c.validated_json ELSE '{}' END, '$.prompt_contract_version') IS NOT NULL
)
AND pv.revision IS NOT (
    SELECT MAX(c.revision) FROM content_provenance c
    WHERE c.page_id = p.id AND c.revision <= p.revision
    AND json_extract(CASE WHEN json_valid(c.validated_json) THEN c.validated_json ELSE '{}' END, '$.translation_basis.source_page_id') IS NOT NULL
)
AND NOT EXISTS (
    SELECT 1 FROM pages holder
    JOIN page_ai_access holder_access ON holder_access.page_id = holder.id AND holder_access.ai_editable = 1
    JOIN content_provenance holder_evidence ON holder_evidence.page_id = holder.id AND holder_evidence.revision = holder.revision
    JOIN json_each(CASE WHEN json_valid(holder_evidence.validated_json) THEN holder_evidence.validated_json ELSE '{}' END, '$.references') reference
    WHERE holder.status = 'draft' AND holder.scheduled_at IS NULL
    AND json_extract(reference.value, '$.page_id') = pv.page_id
    AND json_extract(reference.value, '$.revision') = pv.revision
)
ORDER BY pv.page_id, pv.revision
LIMIT sqlc.arg(batch_limit);

-- name: DeletePrunedPageVersion :execrows
DELETE FROM page_versions WHERE id = ?;

-- Claim acknowledgments of the revision go with it (claims_checks cascades).
-- name: DeletePrunedContentProvenance :execrows
DELETE FROM content_provenance WHERE page_id = ? AND revision = ?;

-- A retained replay receipt of a pruned revision becomes a tombstone, as when
-- its seven days pass: the key, hash and entity stay, so the request is never
-- re-executed and still counts toward the hourly quota.
-- name: CompactPrunedRevisionReplays :execrows
UPDATE idempotency_keys SET result_json = NULL
WHERE entity_type = 'page' AND entity_id = ? AND entity_revision = ? AND result_json IS NOT NULL;
