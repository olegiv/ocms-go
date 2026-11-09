-- +goose Up
CREATE INDEX oauth_tokens_grant_refresh_used ON oauth_tokens(grant_id, refresh_used_at)
WHERE refresh_used_at > 0;

-- Account admission can skip old grants without visiting their token rows.
CREATE TABLE oauth_grant_refresh_activity (
    grant_id TEXT PRIMARY KEY NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    last_refresh_at INTEGER NOT NULL
) WITHOUT ROWID;
INSERT INTO oauth_grant_refresh_activity (grant_id, user_id, last_refresh_at)
SELECT g.id, g.user_id, MAX(t.refresh_used_at)
FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id
WHERE t.refresh_used_at > 0
GROUP BY g.id, g.user_id;
CREATE INDEX oauth_grant_refresh_activity_user ON oauth_grant_refresh_activity(user_id, last_refresh_at);

-- The predecessor's spent timestamp and this marker commit together.
-- +goose StatementBegin
CREATE TRIGGER oauth_grants_track_refresh
AFTER UPDATE OF refresh_used_at ON oauth_tokens
WHEN NEW.refresh_used_at > 0
BEGIN
    INSERT INTO oauth_grant_refresh_activity (grant_id, user_id, last_refresh_at)
    SELECT id, user_id, NEW.refresh_used_at FROM oauth_grants WHERE id = NEW.grant_id
    ON CONFLICT(grant_id) DO UPDATE SET last_refresh_at = MAX(last_refresh_at, excluded.last_refresh_at);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER oauth_grants_track_refresh;
DROP TABLE oauth_grant_refresh_activity;
DROP INDEX oauth_tokens_grant_refresh_used;
