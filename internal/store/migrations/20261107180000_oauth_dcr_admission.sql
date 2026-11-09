-- +goose Up
-- Keep admission records for the full budget window even when a client is
-- evicted. Only new, provisional clients have an evictable source record.
CREATE TABLE oauth_dcr_registrations (
    client_id TEXT PRIMARY KEY,
    source_key TEXT NOT NULL,
    registered_at INTEGER NOT NULL,
    provisional INTEGER NOT NULL DEFAULT 1 CHECK (provisional IN (0, 1))
);
CREATE INDEX oauth_dcr_registrations_time ON oauth_dcr_registrations(registered_at);
CREATE INDEX oauth_dcr_registrations_source_time ON oauth_dcr_registrations(source_key, registered_at);

-- Consent makes the client ineligible even if the grant is later removed.
-- Keep this in SQLite so it also applies during a binary-only rollback.
-- +goose StatementBegin
CREATE TRIGGER oauth_dcr_confirm_registration
AFTER INSERT ON oauth_grants
BEGIN
    UPDATE oauth_dcr_registrations SET provisional = 0 WHERE client_id = NEW.client_id;
END;
-- +goose StatementEnd

CREATE VIEW oauth_dcr_pending AS
SELECT r.client_id, r.source_key, r.registered_at
FROM oauth_dcr_registrations r JOIN oauth_clients c ON c.id = r.client_id
WHERE c.source = 'dcr' AND r.provisional = 1
AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = c.id);

-- +goose Down
DROP VIEW oauth_dcr_pending;
DROP TRIGGER oauth_dcr_confirm_registration;
DROP TABLE oauth_dcr_registrations;
