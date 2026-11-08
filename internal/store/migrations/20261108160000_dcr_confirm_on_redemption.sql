-- +goose Up
DROP TRIGGER oauth_dcr_confirm_registration;

-- Keep the confirmation durable even after token and grant retention runs.
-- Existing markers and expiry dates may belong to clients whose token
-- history has already been pruned, so leave those rows as recorded.
-- +goose StatementBegin
CREATE TRIGGER oauth_dcr_confirm_registration
AFTER INSERT ON oauth_tokens
BEGIN
    UPDATE oauth_dcr_registrations SET provisional = 0
    WHERE client_id = (SELECT client_id FROM oauth_grants WHERE id = NEW.grant_id)
      AND provisional = 1;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER oauth_dcr_confirm_registration;

-- +goose StatementBegin
CREATE TRIGGER oauth_dcr_confirm_registration
AFTER INSERT ON oauth_grants
BEGIN
    UPDATE oauth_dcr_registrations SET provisional = 0 WHERE client_id = NEW.client_id;
END;
-- +goose StatementEnd
