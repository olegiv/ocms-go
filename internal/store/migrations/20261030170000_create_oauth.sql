-- +goose Up
CREATE TABLE oauth_clients (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    redirect_uris TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('cimd', 'dcr')),
    expires_at INTEGER NOT NULL
);

CREATE TABLE oauth_grants (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id TEXT NOT NULL REFERENCES oauth_clients(id),
    scopes TEXT NOT NULL,
    resource TEXT NOT NULL,
    session_version INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER NOT NULL DEFAULT 0,
    last_used_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX oauth_grants_user ON oauth_grants(user_id, created_at);

CREATE TABLE oauth_codes (
    code_hash TEXT PRIMARY KEY,
    grant_id TEXT NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    challenge TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE oauth_tokens (
    id TEXT PRIMARY KEY,
    grant_id TEXT NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
    access_hash TEXT NOT NULL UNIQUE,
    refresh_hash TEXT NOT NULL UNIQUE,
    scopes TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    access_expires_at INTEGER NOT NULL,
    refresh_used_at INTEGER NOT NULL DEFAULT 0,
    access_revoked_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX oauth_tokens_grant ON oauth_tokens(grant_id);

-- +goose Down
DROP TABLE oauth_tokens;
DROP TABLE oauth_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_clients;
