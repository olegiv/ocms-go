// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

// insertTestAPIKeyWithLookup inserts a key the way the admin handler creates
// them today: with the SHA-256 lookup next to the Argon2 hash.
func insertTestAPIKeyWithLookup(t *testing.T, db *sql.DB) string {
	t.Helper()
	rawKey, keyPrefix, err := model.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	keyHash, err := model.HashAPIKey(rawKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO api_keys (name, key_hash, key_prefix, key_lookup, permissions, is_active, created_by)
		VALUES ('lookup', ?, ?, ?, '[]', 1, 1)`, keyHash, keyPrefix, model.APIKeyLookup(rawKey)); err != nil {
		t.Fatal(err)
	}
	return rawKey
}

// samePrefixWrongKey returns a well-formed key sharing rawKey's prefix.
func samePrefixWrongKey(rawKey string) string {
	suffix := []byte(rawKey[model.APIKeyPrefixLength:])
	if suffix[0] == 'A' {
		suffix[0] = 'B'
	} else {
		suffix[0] = 'A'
	}
	return rawKey[:model.APIKeyPrefixLength] + string(suffix)
}

// TestAPIKeyAuth_PrefixHolderNeverReachesArgon2 fails when a request that
// shares only the four-character prefix of a stored key triggers an Argon2
// verification. Before the exact lookup, such requests burned the eight
// shared verification slots and could deny API authentication to everyone
// (finding L-5, 2026-10-11).
func TestAPIKeyAuth_PrefixHolderNeverReachesArgon2(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	rawKey := insertTestAPIKeyWithLookup(t, db)

	before := apiKeyHashChecks.Load()
	w, _ := executeAuthAndCaptureKey(APIKeyAuth, db, samePrefixWrongKey(rawKey))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status = %d, want 401", w.Code)
	}
	if got := apiKeyHashChecks.Load(); got != before {
		t.Fatalf("wrong key with a matching prefix ran %d Argon2 verification(s); want 0", got-before)
	}

	w, key := executeAuthAndCaptureKey(APIKeyAuth, db, rawKey)
	if w.Code != http.StatusOK || key == nil {
		t.Fatalf("real key: status = %d, key = %v", w.Code, key)
	}
	if got := apiKeyHashChecks.Load(); got != before+1 {
		t.Fatalf("real key ran %d Argon2 verification(s); want exactly 1", got-before)
	}
}

// TestAPIKeyAuth_BackfillsLookupForLegacyKey fails when a key created before
// the lookup column stops authenticating, or when its first successful use
// no longer records the lookup that moves it off the prefix path.
func TestAPIKeyAuth_BackfillsLookupForLegacyKey(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	rawKey := insertTestAPIKey(t, db, "legacy", []string{"pages:read"}, true, nil)

	w, key := executeAuthAndCaptureKey(APIKeyAuth, db, rawKey)
	if w.Code != http.StatusOK || key == nil {
		t.Fatalf("legacy key: status = %d", w.Code)
	}
	var lookup sql.NullString
	if err := db.QueryRow(`SELECT key_lookup FROM api_keys WHERE id = ?`, key.ID).Scan(&lookup); err != nil {
		t.Fatal(err)
	}
	if !lookup.Valid || lookup.String != model.APIKeyLookup(rawKey) {
		t.Fatalf("lookup after first use = %v, want the SHA-256 of the key", lookup)
	}

	// From now on a prefix-only guess no longer reaches the verifier.
	before := apiKeyHashChecks.Load()
	if w, _ := executeAuthAndCaptureKey(APIKeyAuth, db, samePrefixWrongKey(rawKey)); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key after backfill: status = %d", w.Code)
	}
	if got := apiKeyHashChecks.Load(); got != before {
		t.Fatalf("prefix guess after backfill ran %d verification(s); want 0", got-before)
	}
}
