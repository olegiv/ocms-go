-- +goose Up
-- Two-factor authentication. user_totp holds the one authenticator app a user
-- has enrolled. secret_enc is the raw TOTP secret sealed with AES-256-GCM
-- under a key derived from OCMS_MFA_KEY and bound to user_id, so a copied row
-- does not open for another account. last_step is the last accepted RFC 6238
-- time step: a code is accepted once (RFC 6238 section 5.2).
CREATE TABLE user_totp (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_enc BLOB NOT NULL,
    last_step INTEGER NOT NULL DEFAULT 0,
    enrolled_at DATETIME NOT NULL,
    last_used_at DATETIME
);

-- Single-use recovery codes, stored as the SHA-256 of the normalized code.
-- The codes carry 80 random bits, so a fast hash does not make them guessable.
CREATE TABLE user_recovery_codes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    used_at DATETIME,
    UNIQUE (user_id, code_hash)
);

-- +goose Down
DROP TABLE IF EXISTS user_recovery_codes;
DROP TABLE IF EXISTS user_totp;
