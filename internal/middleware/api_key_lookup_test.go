// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"database/sql"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/testutil/repotree"
)

// insertTestAPIKeyWithLookup inserts a key the way the admin handler creates
// them today: with the SHA-256 lookup next to the Argon2 hash.
func insertTestAPIKeyWithLookup(t *testing.T, db *sql.DB) string {
	t.Helper()
	rawKey, keyPrefix, err := model.GenerateAPIAccessKey()
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
// shared verification slots and could deny API authentication to everyone.
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

// TestAPIKeyAuth_TwoLegacyKeysSharingAPrefix fails when backfilling one
// legacy key hides a sibling that still has no lookup.
func TestAPIKeyAuth_TwoLegacyKeysSharingAPrefix(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	first := insertTestAPIKey(t, db, "legacy-1", []string{"pages:read"}, true, nil)
	second := samePrefixWrongKey(first)
	hash, err := model.HashAPIKey(second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO api_keys (name, key_hash, key_prefix, permissions, is_active, created_by)
		VALUES ('legacy-2', ?, ?, '[]', 1, 1)`, hash, model.ExtractAPIKeyPrefix(second)); err != nil {
		t.Fatal(err)
	}

	w, key := executeAuthAndCaptureKey(APIKeyAuth, db, second)
	if w.Code != http.StatusOK || key == nil || key.Name != "legacy-2" {
		t.Fatalf("second legacy key: status %d, key %v", w.Code, key)
	}
	var lookups int
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE key_lookup IS NOT NULL`).Scan(&lookups); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 {
		t.Fatalf("backfilled rows = %d, want only the key that authenticated", lookups)
	}
	w, key = executeAuthAndCaptureKey(APIKeyAuth, db, first)
	if w.Code != http.StatusOK || key == nil || key.Name != "legacy-1" {
		t.Fatalf("remaining legacy key: status %d, key %v", w.Code, key)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE key_lookup IS NOT NULL`).Scan(&lookups); err != nil || lookups != 2 {
		t.Fatalf("backfilled rows after both = %d, %v; want 2", lookups, err)
	}
}

// TestPrefixLookupOnlyInsideVerifier fails when any non-test Go file other
// than this package's verifier queries API keys by prefix: every bearer-token
// check must go through VerifyAPIKey so the lookup-first rule and the
// verification slots apply everywhere.
func TestPrefixLookupOnlyInsideVerifier(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if repotree.Skip(root, path, d) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".sql.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "GetAPIKeysByPrefix(") {
			rel, _ := filepath.Rel(root, path)
			if rel != filepath.Join("internal", "middleware", "api.go") {
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("API keys are looked up by prefix outside VerifyAPIKey in: %v", offenders)
	}
}
