-- +goose Up
CREATE INDEX oauth_codes_expiry ON oauth_codes(expires_at, code_hash);
CREATE INDEX oauth_codes_grant ON oauth_codes(grant_id);
CREATE INDEX oauth_grants_expiry ON oauth_grants(expires_at);
CREATE INDEX oauth_grants_revoked ON oauth_grants(revoked_at) WHERE revoked_at > 0;
CREATE INDEX oauth_grants_client ON oauth_grants(client_id);
CREATE INDEX preview_links_grant ON preview_links(issuing_grant_id) WHERE issuing_grant_id IS NOT NULL;

-- +goose Down
DROP INDEX preview_links_grant;
DROP INDEX oauth_grants_client;
DROP INDEX oauth_grants_revoked;
DROP INDEX oauth_grants_expiry;
DROP INDEX oauth_codes_grant;
DROP INDEX oauth_codes_expiry;
