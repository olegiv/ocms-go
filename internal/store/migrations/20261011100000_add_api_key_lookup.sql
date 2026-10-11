-- +goose Up
-- Exact-match lookup for API keys. key_lookup holds SHA-256(raw key) so
-- authentication finds the one candidate row directly instead of running the
-- Argon2 verifier over every active key that shares a four-character prefix.
-- Keys created before this column exists keep NULL until their first
-- successful use backfills it.
ALTER TABLE api_keys ADD COLUMN key_lookup TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_lookup ON api_keys(key_lookup);

-- +goose Down
DROP INDEX IF EXISTS idx_api_keys_lookup;
ALTER TABLE api_keys DROP COLUMN key_lookup;
