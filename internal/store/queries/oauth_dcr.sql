-- Read all budgets in one snapshot. The /48 check reads the limit-th newest
-- row so legacy entries above the limit yield the actual recovery time.
-- name: GetOAuthDCRRegistrationBudget :one
SELECT CAST((SELECT COUNT(*) FROM oauth_dcr_registrations global_count
    WHERE global_count.registered_at > sqlc.arg(global_cutoff)) AS INTEGER) AS total,
    CAST(COALESCE((SELECT MIN(global_first.registered_at) FROM oauth_dcr_registrations global_first
        WHERE global_first.registered_at > sqlc.arg(global_cutoff)), 0) AS INTEGER) AS oldest,
    CAST((SELECT COUNT(*) FROM oauth_dcr_registrations source_count
        WHERE source_count.source_key = sqlc.arg(source_key)
        AND source_count.registered_at > sqlc.arg(source_cutoff)) AS INTEGER) AS source_total,
    CAST(COALESCE((SELECT MIN(source_first.registered_at) FROM oauth_dcr_registrations source_first
        WHERE source_first.source_key = sqlc.arg(source_key)
        AND source_first.registered_at > sqlc.arg(source_cutoff)), 0) AS INTEGER) AS source_oldest,
    CAST(CASE WHEN CAST(sqlc.arg(network_min) AS TEXT) = '' THEN 0 ELSE COALESCE((
        SELECT network.registered_at - sqlc.arg(source_cutoff) FROM oauth_dcr_registrations network
        WHERE network.registered_at > sqlc.arg(source_cutoff)
        AND ((network.source_key >= sqlc.arg(network_min) AND network.source_key < sqlc.arg(network_end))
            OR network.source_key = sqlc.arg(zero_subnet))
        ORDER BY network.registered_at DESC LIMIT 1 OFFSET sqlc.arg(network_offset)
    ), 0) END AS INTEGER) AS network_retry_ns;

-- name: RecordOAuthDCRRegistration :exec
INSERT INTO oauth_dcr_registrations (client_id, source_key, registered_at)
VALUES (?, ?, ?);

-- name: DeleteOldOAuthDCRRegistrations :exec
DELETE FROM oauth_dcr_registrations WHERE registered_at <= sqlc.arg(cutoff)
AND (provisional = 0 OR NOT EXISTS (SELECT 1 FROM oauth_clients WHERE id = oauth_dcr_registrations.client_id));

-- Prefer the oldest provisional client from a source holding more slots than
-- the requester. Give a source's only provisional client the normal grace
-- period, with a shorter minimum age at capacity for a source with no recent
-- successful registration. The pending view excludes clients with grants.
-- name: EvictOAuthDCRClient :execrows
DELETE FROM oauth_clients WHERE id = (
    SELECT p.client_id FROM oauth_dcr_pending p
    JOIN (SELECT d.source_key, COUNT(*) AS clients FROM oauth_dcr_pending d GROUP BY d.source_key) h
        ON h.source_key = p.source_key
    WHERE h.clients > (SELECT COUNT(*) FROM oauth_dcr_pending requester WHERE requester.source_key = sqlc.arg(source_key))
    AND (h.clients > 1 OR p.registered_at <= sqlc.arg(grace_cutoff)
        OR (p.registered_at <= sqlc.arg(pressure_cutoff) AND 0 = (
            SELECT COUNT(*) FROM oauth_dcr_registrations recent
            WHERE recent.source_key = sqlc.arg(source_key) AND recent.registered_at > sqlc.arg(source_cutoff)
        )))
    ORDER BY h.clients DESC, p.registered_at, p.client_id
    LIMIT 1
);
