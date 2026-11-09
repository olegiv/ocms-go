-- +goose Up
-- Old links exposed their bearer to ingress URL logs. They must not remain
-- usable by converting an already-disclosed credential to the new transport.
UPDATE preview_links SET revoked_at = unixepoch() WHERE revoked_at = 0;

-- +goose Down
-- Revocation of potentially disclosed credentials must not be undone.
SELECT 1;
