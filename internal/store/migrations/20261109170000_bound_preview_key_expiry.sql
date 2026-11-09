-- +goose Up
-- Preserve the shortest observed deadline even if the key is extended later.
-- A malformed non-null expiry already denies redemption, so cap it at zero.
UPDATE preview_links AS link
SET expires_at = COALESCE(unixepoch(key.expires_at), 0)
FROM api_keys AS key
WHERE link.issuing_key_id = key.id
  AND link.revoked_at = 0
  AND key.expires_at IS NOT NULL
  AND link.expires_at > COALESCE(unixepoch(key.expires_at), 0);

-- +goose StatementBegin
CREATE TRIGGER preview_key_expiry AFTER UPDATE OF expires_at ON api_keys
WHEN NEW.expires_at IS NOT NULL AND NEW.expires_at IS NOT OLD.expires_at
BEGIN
    UPDATE preview_links SET expires_at = COALESCE(unixepoch(NEW.expires_at), 0)
    WHERE issuing_key_id = NEW.id
      AND revoked_at = 0
      AND expires_at > COALESCE(unixepoch(NEW.expires_at), 0);
END;
-- +goose StatementEnd

-- +goose Down
-- Keep the shorter deadlines on rollback.
DROP TRIGGER preview_key_expiry;
