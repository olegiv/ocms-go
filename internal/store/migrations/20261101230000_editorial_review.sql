-- +goose Up
CREATE TABLE claims_checks (
    provenance_id INTEGER NOT NULL REFERENCES content_provenance(id) ON DELETE CASCADE,
    claim_index INTEGER NOT NULL CHECK (claim_index >= 0 AND claim_index < 50),
    checked_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    checked_at INTEGER NOT NULL,
    PRIMARY KEY (provenance_id, claim_index)
);

CREATE TABLE preview_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash TEXT NOT NULL UNIQUE,
    page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_version INTEGER NOT NULL,
    issuing_grant_id TEXT REFERENCES oauth_grants(id) ON DELETE CASCADE,
    issuing_key_id INTEGER REFERENCES api_keys(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER NOT NULL DEFAULT 0,
    CHECK (issuing_grant_id IS NULL OR issuing_key_id IS NULL)
);
CREATE INDEX preview_links_owner_expiry ON preview_links(created_by, expires_at) WHERE revoked_at = 0;
CREATE INDEX preview_links_page ON preview_links(page_id);
CREATE INDEX preview_links_owner_created ON preview_links(created_by, created_at);

-- Persist lifecycle invalidation so reversing a page/credential change cannot
-- resurrect a previously disclosed bearer link.
-- +goose StatementBegin
CREATE TRIGGER preview_page_lifecycle AFTER UPDATE OF status, author_id, scheduled_at ON pages
WHEN NEW.status != 'draft' OR NEW.author_id != OLD.author_id OR NEW.scheduled_at IS NOT NULL
BEGIN
    UPDATE preview_links SET revoked_at=unixepoch() WHERE page_id=NEW.id AND revoked_at=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER preview_ai_access AFTER UPDATE OF ai_editable ON page_ai_access
WHEN NEW.ai_editable=0
BEGIN
    UPDATE preview_links SET revoked_at=unixepoch() WHERE page_id=NEW.page_id AND revoked_at=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER preview_user_access AFTER UPDATE OF role, session_version ON users
WHEN NEW.role NOT IN ('admin','editor') OR NEW.session_version != OLD.session_version
BEGIN
    UPDATE preview_links SET revoked_at=unixepoch() WHERE created_by=NEW.id AND revoked_at=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER preview_key_access AFTER UPDATE OF is_active, permissions, created_by ON api_keys
WHEN NEW.is_active=0 OR NEW.permissions!=OLD.permissions OR NEW.created_by!=OLD.created_by
BEGIN
    UPDATE preview_links SET revoked_at=unixepoch() WHERE issuing_key_id=NEW.id AND revoked_at=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER preview_grant_access AFTER UPDATE OF revoked_at, scopes ON oauth_grants
WHEN NEW.revoked_at!=0 OR NEW.scopes!=OLD.scopes
BEGIN
    UPDATE preview_links SET revoked_at=unixepoch() WHERE issuing_grant_id=NEW.id AND revoked_at=0;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER preview_grant_access;
DROP TRIGGER preview_key_access;
DROP TRIGGER preview_user_access;
DROP TRIGGER preview_ai_access;
DROP TRIGGER preview_page_lifecycle;
DROP TABLE preview_links;
DROP TABLE claims_checks;
