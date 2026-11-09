-- +goose Up
-- Validate new attribution without rewriting historical evidence. A table CHECK
-- requiring a human user would also reject the existing ON DELETE SET NULL
-- anonymization. Keep that narrow foreign-key transition available on updates.
-- +goose StatementBegin
CREATE TRIGGER content_provenance_principal_insert BEFORE INSERT ON content_provenance
WHEN NOT COALESCE(
    (NEW.user_id IS NULL OR (typeof(NEW.user_id) = 'integer' AND NEW.user_id > 0))
    AND CASE NEW.source
        WHEN 'human' THEN NEW.user_id IS NOT NULL AND NEW.api_key_id IS NULL
            AND NEW.oauth_grant_id = '' AND NEW.oauth_client_id = ''
        WHEN 'api' THEN NEW.user_id IS NOT NULL AND typeof(NEW.api_key_id) = 'integer' AND NEW.api_key_id > 0
            AND NEW.oauth_grant_id = '' AND NEW.oauth_client_id = ''
        WHEN 'oauth' THEN NEW.user_id IS NOT NULL AND NEW.api_key_id IS NULL
            AND NEW.oauth_grant_id <> '' AND NEW.oauth_client_id <> ''
        WHEN 'system' THEN NEW.api_key_id IS NULL
            AND NEW.oauth_grant_id = '' AND NEW.oauth_client_id = ''
        WHEN 'migration' THEN NEW.api_key_id IS NULL
            AND NEW.oauth_grant_id = '' AND NEW.oauth_client_id = ''
        ELSE 0
    END, 0)
BEGIN
    SELECT RAISE(ABORT, 'invalid provenance principal');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER content_provenance_principal_update
BEFORE UPDATE OF user_id, source, api_key_id, oauth_grant_id, oauth_client_id ON content_provenance
WHEN (NEW.user_id IS NOT OLD.user_id OR NEW.source IS NOT OLD.source
    OR NEW.api_key_id IS NOT OLD.api_key_id OR NEW.oauth_grant_id IS NOT OLD.oauth_grant_id
    OR NEW.oauth_client_id IS NOT OLD.oauth_client_id)
AND NOT (NEW.user_id IS NULL AND OLD.user_id IS NOT NULL
    AND NEW.source IS OLD.source AND NEW.api_key_id IS OLD.api_key_id
    AND NEW.oauth_grant_id IS OLD.oauth_grant_id AND NEW.oauth_client_id IS OLD.oauth_client_id
    AND NOT EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id))
AND NOT COALESCE(
    (NEW.user_id IS NULL OR (typeof(NEW.user_id) = 'integer' AND NEW.user_id > 0))
    AND CASE NEW.source
        WHEN 'human' THEN NEW.user_id IS NOT NULL AND NEW.api_key_id IS NULL
            AND NEW.oauth_grant_id = '' AND NEW.oauth_client_id = ''
        WHEN 'api' THEN NEW.user_id IS NOT NULL AND typeof(NEW.api_key_id) = 'integer' AND NEW.api_key_id > 0
            AND NEW.oauth_grant_id = '' AND NEW.oauth_client_id = ''
        WHEN 'oauth' THEN NEW.user_id IS NOT NULL AND NEW.api_key_id IS NULL
            AND NEW.oauth_grant_id <> '' AND NEW.oauth_client_id <> ''
        WHEN 'system' THEN NEW.api_key_id IS NULL
            AND NEW.oauth_grant_id = '' AND NEW.oauth_client_id = ''
        WHEN 'migration' THEN NEW.api_key_id IS NULL
            AND NEW.oauth_grant_id = '' AND NEW.oauth_client_id = ''
        ELSE 0
    END, 0)
BEGIN
    SELECT RAISE(ABORT, 'invalid provenance principal');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER content_provenance_principal_update;
DROP TRIGGER content_provenance_principal_insert;
