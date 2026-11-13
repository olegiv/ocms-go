-- +goose Up
-- Preserve the shortest observed deadline even if the key is extended later.
-- A malformed non-null expiry already denies redemption, so cap it at zero.
-- Correlated subqueries instead of UPDATE ... FROM: sqlc's SQLite parser
-- rejects the latter while reading the schema, which breaks `sqlc generate`.
UPDATE preview_links
SET expires_at = (
    SELECT COALESCE(unixepoch(k.expires_at), 0)
    FROM api_keys AS k
    WHERE k.id = preview_links.issuing_key_id
)
WHERE revoked_at = 0
  AND EXISTS (
    SELECT 1
    FROM api_keys AS k
    WHERE k.id = preview_links.issuing_key_id
      AND k.expires_at IS NOT NULL
      AND preview_links.expires_at > COALESCE(unixepoch(k.expires_at), 0)
  );

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
