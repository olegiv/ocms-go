// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// setupTestDB creates a test database with the api_keys table.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	db.SetMaxOpenConns(1)

	// Create api_keys table
	_, err = db.Exec(`
		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			key_prefix TEXT NOT NULL,
			permissions TEXT NOT NULL DEFAULT '[]',
			last_used_at DATETIME,
			expires_at DATETIME,
			is_active BOOLEAN NOT NULL DEFAULT 1,
			created_by INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE api_key_source_cidrs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_key_id INTEGER NOT NULL,
			cidr TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL,
			category TEXT NOT NULL,
			message TEXT NOT NULL,
			user_id INTEGER,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			ip_address TEXT NOT NULL DEFAULT '',
			request_url TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		t.Fatalf("failed to create events table: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE api_key_source_state (
			api_key_id INTEGER PRIMARY KEY,
			last_ip TEXT NOT NULL,
			last_seen_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("failed to create api_key_source_state table: %v", err)
	}

	return db
}

func setAPIAllowedCIDRsForTest(t *testing.T, entries ...string) {
	t.Helper()
	if err := SetAPIAllowedCIDRs(entries); err != nil {
		t.Fatalf("SetAPIAllowedCIDRs() error: %v", err)
	}
	t.Cleanup(func() {
		if err := SetAPIAllowedCIDRs(nil); err != nil {
			t.Fatalf("reset SetAPIAllowedCIDRs() error: %v", err)
		}
	})
}

func setRequireAPIKeyExpiryForTest(t *testing.T, required bool) {
	t.Helper()
	SetRequireAPIKeyExpiry(required)
	t.Cleanup(func() {
		SetRequireAPIKeyExpiry(false)
	})
}

func setRequireAPIKeySourceCIDRsForTest(t *testing.T, required bool) {
	t.Helper()
	SetRequireAPIKeySourceCIDRs(required)
	t.Cleanup(func() {
		SetRequireAPIKeySourceCIDRs(false)
	})
}

func setRequireAPIAllowedCIDRsForTest(t *testing.T, required bool) {
	t.Helper()
	SetRequireAPIAllowedCIDRs(required)
	t.Cleanup(func() {
		SetRequireAPIAllowedCIDRs(false)
	})
}

func setAPIKeyMaxTTLDaysForTest(t *testing.T, days int) {
	t.Helper()
	SetAPIKeyMaxTTLDays(days)
	t.Cleanup(func() {
		SetAPIKeyMaxTTLDays(0)
	})
}

func setRevokeAPIKeyOnSourceIPChangeForTest(t *testing.T, enabled bool) {
	t.Helper()
	SetRevokeAPIKeyOnSourceIPChange(enabled)
	apiKeySourceTracker = newAPIKeySourceTracker(24 * time.Hour)
	t.Cleanup(func() {
		SetRevokeAPIKeyOnSourceIPChange(false)
		apiKeySourceTracker = newAPIKeySourceTracker(24 * time.Hour)
	})
}

// simpleOKHandler returns an http.Handler that writes 200 OK.
var simpleOKHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// executeRequest creates a test request and executes it against the handler.
// Returns the response recorder.
func executeRequest(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// executeAuthRequest creates a test request with an auth header and executes it.
func executeAuthRequest(handler http.Handler, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", authHeader)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// executeWithAPIKey creates a test request with an API key in context and executes it.
func executeWithAPIKey(handler http.Handler, apiKey store.ApiKey) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, apiKey)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// executeAuthAndCaptureKey executes a request with Bearer token and returns the recorder and captured API key.
func executeAuthAndCaptureKey(middleware func(*sql.DB) func(http.Handler) http.Handler, db *sql.DB, rawKey string) (*httptest.ResponseRecorder, *store.ApiKey) {
	var capturedKey *store.ApiKey
	handler := middleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKey = GetAPIKey(r)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	return w, capturedKey
}

// insertTestAPIKey inserts a test API key and returns the raw key.
func insertTestAPIKey(t *testing.T, db *sql.DB, name string, permissions []string, isActive bool, expiresAt *time.Time) string {
	t.Helper()

	rawKey, keyPrefix, err := model.GenerateAPIKey()
	if err != nil {
		t.Fatalf("failed to generate API key: %v", err)
	}
	keyHash, err := model.HashAPIKey(rawKey)
	if err != nil {
		t.Fatalf("failed to hash API key: %v", err)
	}

	permJSON, _ := json.Marshal(permissions)

	var expires sql.NullTime
	if expiresAt != nil {
		expires = sql.NullTime{Time: *expiresAt, Valid: true}
	}

	_, err = db.Exec(`
		INSERT INTO api_keys (name, key_hash, key_prefix, permissions, is_active, expires_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, 1)
	`, name, keyHash, keyPrefix, string(permJSON), isActive, expires)
	if err != nil {
		t.Fatalf("failed to insert test key: %v", err)
	}

	return rawKey
}

func insertTestAPIKeySourceCIDR(t *testing.T, db *sql.DB, keyPrefix, cidr string) {
	t.Helper()

	var keyID int64
	err := db.QueryRow(`SELECT id FROM api_keys WHERE key_prefix = ?`, keyPrefix).Scan(&keyID)
	if err != nil {
		t.Fatalf("failed to lookup api key by prefix: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO api_key_source_cidrs (api_key_id, cidr)
		VALUES (?, ?)
	`, keyID, cidr)
	if err != nil {
		t.Fatalf("failed to insert api key source cidr: %v", err)
	}
}

func TestAPIKeySourceTrackerObserve(t *testing.T) {
	tracker := newAPIKeySourceTracker(10 * time.Minute)
	now := time.Now()

	changed, previous := tracker.Observe(42, "203.0.113.10", now)
	if changed {
		t.Fatal("first observation should not be marked as changed")
	}
	if previous != "" {
		t.Fatalf("previous IP = %q, want empty", previous)
	}

	changed, previous = tracker.Observe(42, "203.0.113.10", now.Add(1*time.Minute))
	if changed {
		t.Fatal("same IP observation should not be marked as changed")
	}
	if previous != "" {
		t.Fatalf("previous IP = %q, want empty", previous)
	}

	changed, previous = tracker.Observe(42, "198.51.100.20", now.Add(2*time.Minute))
	if !changed {
		t.Fatal("IP change should be marked as changed")
	}
	if previous != "203.0.113.10" {
		t.Fatalf("previous IP = %q, want %q", previous, "203.0.113.10")
	}
}

func TestAPIKeySourceTrackerObserve_ExpiresState(t *testing.T) {
	tracker := newAPIKeySourceTracker(1 * time.Minute)
	base := time.Now()

	changed, previous := tracker.Observe(7, "203.0.113.1", base)
	if changed || previous != "" {
		t.Fatalf("first observation changed=%v previous=%q, want false and empty", changed, previous)
	}

	changed, previous = tracker.Observe(7, "198.51.100.2", base.Add(2*time.Minute))
	if changed {
		t.Fatal("expired state should not produce a change event")
	}
	if previous != "" {
		t.Fatalf("previous IP = %q, want empty", previous)
	}
}

func TestWriteAPIError(t *testing.T) {
	w := httptest.NewRecorder()

	WriteAPIError(w, http.StatusBadRequest, "validation_error", "Invalid input", map[string]string{
		"field": "email",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %s", ct)
	}

	var resp APIError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Error.Code != "validation_error" {
		t.Errorf("expected code 'validation_error', got %s", resp.Error.Code)
	}
	if resp.Error.Message != "Invalid input" {
		t.Errorf("expected message 'Invalid input', got %s", resp.Error.Message)
	}
	if resp.Error.Details["field"] != "email" {
		t.Errorf("expected details.field 'email', got %s", resp.Error.Details["field"])
	}
}

func TestAPIKeyAuth_MissingHeader(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	handler := APIKeyAuth(db)(simpleOKHandler)
	w := executeRequest(handler, "GET", "/api/test")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAPIKeyAuth_InvalidFormat(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	handler := APIKeyAuth(db)(simpleOKHandler)

	testCases := []string{
		"InvalidFormat",   // No "Bearer" prefix
		"Basic sometoken", // Wrong auth type
		"Bearer",          // Missing token
		"Bearer ",         // Empty token
	}

	for _, authHeader := range testCases {
		w := executeAuthRequest(handler, authHeader)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("auth header '%s': expected status %d, got %d", authHeader, http.StatusUnauthorized, w.Code)
		}
	}
}

func TestAPIKeyAuth_InvalidKey(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	handler := APIKeyAuth(db)(simpleOKHandler)
	w := executeAuthRequest(handler, "Bearer invalid-key-that-does-not-exist")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAPIKeyAuth_InactiveKey(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	rawKey := insertTestAPIKey(t, db, "Inactive Key", []string{"pages:read"}, false, nil)
	handler := APIKeyAuth(db)(simpleOKHandler)
	w := executeAuthRequest(handler, "Bearer "+rawKey)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAPIKeyAuth_ExpiredKey(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	expires := time.Now().Add(-1 * time.Hour) // Expired 1 hour ago
	rawKey := insertTestAPIKey(t, db, "Expired Key", []string{"pages:read"}, true, &expires)
	handler := APIKeyAuth(db)(simpleOKHandler)
	w := executeAuthRequest(handler, "Bearer "+rawKey)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAPIKeyAuth_ValidKey(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	rawKey := insertTestAPIKey(t, db, "Valid Key", []string{"pages:read"}, true, nil)

	w, receivedAPIKey := executeAuthAndCaptureKey(APIKeyAuth, db, rawKey)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	if receivedAPIKey == nil {
		t.Fatal("expected API key to be in context")
	}

	if receivedAPIKey.Name != "Valid Key" {
		t.Errorf("expected key name 'Valid Key', got %s", receivedAPIKey.Name)
	}
}

func TestAPIKeyAuth_ValidKeyWithFutureExpiry(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	expires := time.Now().Add(24 * time.Hour) // Expires in 24 hours
	rawKey := insertTestAPIKey(t, db, "Future Expiry", []string{"pages:read"}, true, &expires)
	handler := APIKeyAuth(db)(simpleOKHandler)
	w := executeAuthRequest(handler, "Bearer "+rawKey)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestAPIKeyAuth_IPAllowlist(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	setAPIAllowedCIDRsForTest(t, "203.0.113.0/24")
	rawKey := insertTestAPIKey(t, db, "Allowlist Key", []string{"pages:read"}, true, nil)

	handler := APIKeyAuth(db)(simpleOKHandler)

	t.Run("allowed source IP", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKey)
		req.RemoteAddr = "203.0.113.10:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("blocked source IP", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKey)
		req.RemoteAddr = "198.51.100.10:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})
}

func TestAPIKeyAuth_RequireGlobalCIDRPolicy(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setRequireAPIAllowedCIDRsForTest(t, true)

	rawKey := insertTestAPIKey(t, db, "Require Global CIDR", []string{"pages:read"}, true, nil)
	handler := APIKeyAuth(db)(simpleOKHandler)

	t.Run("reject when global CIDR policy not configured", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKey)
		req.RemoteAddr = "203.0.113.25:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("allow when global CIDR policy is configured and matched", func(t *testing.T) {
		setAPIAllowedCIDRsForTest(t, "203.0.113.0/24")
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKey)
		req.RemoteAddr = "203.0.113.25:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})
}

func TestAPIKeyAuth_RequireExpiry(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setRequireAPIKeyExpiryForTest(t, true)

	rawKeyNoExpiry := insertTestAPIKey(t, db, "No Expiry", []string{"pages:read"}, true, nil)
	expires := time.Now().Add(24 * time.Hour)
	rawKeyWithExpiry := insertTestAPIKey(t, db, "With Expiry", []string{"pages:read"}, true, &expires)

	handler := APIKeyAuth(db)(simpleOKHandler)

	t.Run("reject key without expiry", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKeyNoExpiry)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("allow key with expiry", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKeyWithExpiry)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})
}

func TestAPIKeyAuth_MaxTTLPolicy(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setAPIKeyMaxTTLDaysForTest(t, 7)

	rawKeyNoExpiry := insertTestAPIKey(t, db, "No Expiry", []string{"pages:read"}, true, nil)
	expiryWithinTTL := time.Now().Add(3 * 24 * time.Hour)
	rawKeyWithinTTL := insertTestAPIKey(t, db, "Within TTL", []string{"pages:read"}, true, &expiryWithinTTL)
	expiryBeyondTTL := time.Now().Add(30 * 24 * time.Hour)
	rawKeyBeyondTTL := insertTestAPIKey(t, db, "Beyond TTL", []string{"pages:read"}, true, &expiryBeyondTTL)

	handler := APIKeyAuth(db)(simpleOKHandler)

	t.Run("reject key without expiry", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKeyNoExpiry)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("allow key within max ttl", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKeyWithinTTL)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("reject key beyond max ttl", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKeyBeyondTTL)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})
}

func TestAPIKeyAuth_PerKeySourceCIDRAllowlist(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	rawKey := insertTestAPIKey(t, db, "Per-Key Allowlist", []string{"pages:read"}, true, nil)
	insertTestAPIKeySourceCIDR(t, db, model.ExtractAPIKeyPrefix(rawKey), "203.0.113.0/24")

	handler := APIKeyAuth(db)(simpleOKHandler)

	t.Run("allowed by per-key CIDR", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKey)
		req.RemoteAddr = "203.0.113.25:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("blocked by per-key CIDR", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKey)
		req.RemoteAddr = "198.51.100.25:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})
}

func TestAPIKeyAuth_PerKeySourceCIDRAllowlist_MissingTableBackwardCompatible(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	rawKey := insertTestAPIKey(t, db, "No Table Key", []string{"pages:read"}, true, nil)
	if _, err := db.Exec(`DROP TABLE api_key_source_cidrs`); err != nil {
		t.Fatalf("failed to drop api_key_source_cidrs: %v", err)
	}

	handler := APIKeyAuth(db)(simpleOKHandler)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	req.RemoteAddr = "198.51.100.25:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestAPIKeyAuth_RequirePerKeySourceCIDRs(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setRequireAPIKeySourceCIDRsForTest(t, true)

	rawKeyNoCIDRs := insertTestAPIKey(t, db, "No CIDRs", []string{"pages:read"}, true, nil)
	rawKeyWithCIDRs := insertTestAPIKey(t, db, "With CIDRs", []string{"pages:read"}, true, nil)
	insertTestAPIKeySourceCIDR(t, db, model.ExtractAPIKeyPrefix(rawKeyWithCIDRs), "203.0.113.0/24")

	handler := APIKeyAuth(db)(simpleOKHandler)

	t.Run("reject key without per-key CIDRs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKeyNoCIDRs)
		req.RemoteAddr = "203.0.113.25:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("allow key with per-key CIDRs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+rawKeyWithCIDRs)
		req.RemoteAddr = "203.0.113.25:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})
}

func TestAPIKeyAuth_RequirePerKeySourceCIDRs_MissingTable(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setRequireAPIKeySourceCIDRsForTest(t, true)

	rawKey := insertTestAPIKey(t, db, "No Table Key", []string{"pages:read"}, true, nil)
	if _, err := db.Exec(`DROP TABLE api_key_source_cidrs`); err != nil {
		t.Fatalf("failed to drop api_key_source_cidrs: %v", err)
	}

	handler := APIKeyAuth(db)(simpleOKHandler)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	req.RemoteAddr = "203.0.113.25:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAPIKeyAuth_RevokeOnSourceIPChange(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setRevokeAPIKeyOnSourceIPChangeForTest(t, true)

	rawKey := insertTestAPIKey(t, db, "Revoke On Source Change", []string{"pages:read"}, true, nil)
	handler := APIKeyAuth(db)(simpleOKHandler)

	req1 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req1.Header.Set("Authorization", "Bearer "+rawKey)
	req1.RemoteAddr = "203.0.113.10:12345"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", w1.Code, http.StatusOK)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req2.Header.Set("Authorization", "Bearer "+rawKey)
	req2.RemoteAddr = "198.51.100.20:12345"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("second request status = %d, want %d", w2.Code, http.StatusUnauthorized)
	}

	var isActive bool
	err := db.QueryRow(`SELECT is_active FROM api_keys WHERE key_prefix = ?`, model.ExtractAPIKeyPrefix(rawKey)).Scan(&isActive)
	if err != nil {
		t.Fatalf("failed to load key active status: %v", err)
	}
	if isActive {
		t.Fatal("expected key to be deactivated after source IP anomaly")
	}

	var eventMessage string
	var eventCategory string
	var eventMetadata string
	err = db.QueryRow(`
		SELECT message, category, metadata
		FROM events
		ORDER BY id DESC
		LIMIT 1
	`).Scan(&eventMessage, &eventCategory, &eventMetadata)
	if err != nil {
		t.Fatalf("failed to load anomaly event: %v", err)
	}
	if eventCategory != model.EventCategorySecurity {
		t.Fatalf("event category = %q, want %q", eventCategory, model.EventCategorySecurity)
	}
	if eventMessage != "API key deactivated due to source IP anomaly" {
		t.Fatalf("event message = %q, want revoked anomaly message", eventMessage)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(eventMetadata), &meta); err != nil {
		t.Fatalf("failed to decode event metadata: %v", err)
	}
	if meta["status"] != "revoked" {
		t.Fatalf("event metadata status = %v, want %q", meta["status"], "revoked")
	}
}

func TestAPIKeyAuth_RevokeOnSourceIPChange_SkipsWhenPerKeyCIDRsConfigured(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setRevokeAPIKeyOnSourceIPChangeForTest(t, true)

	rawKey := insertTestAPIKey(t, db, "No Revoke With CIDRs", []string{"pages:read"}, true, nil)
	insertTestAPIKeySourceCIDR(t, db, model.ExtractAPIKeyPrefix(rawKey), "203.0.113.0/24")
	handler := APIKeyAuth(db)(simpleOKHandler)

	req1 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req1.Header.Set("Authorization", "Bearer "+rawKey)
	req1.RemoteAddr = "203.0.113.10:12345"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", w1.Code, http.StatusOK)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req2.Header.Set("Authorization", "Bearer "+rawKey)
	req2.RemoteAddr = "203.0.113.11:12345"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second request status = %d, want %d", w2.Code, http.StatusOK)
	}

	var isActive bool
	err := db.QueryRow(`SELECT is_active FROM api_keys WHERE key_prefix = ?`, model.ExtractAPIKeyPrefix(rawKey)).Scan(&isActive)
	if err != nil {
		t.Fatalf("failed to load key active status: %v", err)
	}
	if !isActive {
		t.Fatal("expected key to remain active when per-key source CIDRs are configured")
	}

	var eventMetadata string
	err = db.QueryRow(`
		SELECT metadata
		FROM events
		ORDER BY id DESC
		LIMIT 1
	`).Scan(&eventMetadata)
	if err != nil {
		t.Fatalf("failed to load anomaly event: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(eventMetadata), &meta); err != nil {
		t.Fatalf("failed to decode event metadata: %v", err)
	}
	if meta["status"] != "observed" {
		t.Fatalf("event metadata status = %v, want %q", meta["status"], "observed")
	}
}

func TestAPIKeyAuth_RevokeOnSourceIPChange_FailClosedOnDeactivateError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setRevokeAPIKeyOnSourceIPChangeForTest(t, true)

	rawKey := insertTestAPIKey(t, db, "Revoke Fail Closed", []string{"pages:read"}, true, nil)
	handler := APIKeyAuth(db)(simpleOKHandler)

	req1 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req1.Header.Set("Authorization", "Bearer "+rawKey)
	req1.RemoteAddr = "203.0.113.10:12345"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", w1.Code, http.StatusOK)
	}

	if _, err := db.Exec(`PRAGMA query_only = ON`); err != nil {
		t.Fatalf("failed to enable sqlite query_only mode: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req2.Header.Set("Authorization", "Bearer "+rawKey)
	req2.RemoteAddr = "198.51.100.20:12345"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("second request status = %d, want %d", w2.Code, http.StatusUnauthorized)
	}
}

func TestAPIKeyAuth_RevokeOnSourceIPChange_PersistsBaselineAcrossTrackerReset(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	setRevokeAPIKeyOnSourceIPChangeForTest(t, true)

	rawKey := insertTestAPIKey(t, db, "Revoke Persistent Baseline", []string{"pages:read"}, true, nil)
	handler := APIKeyAuth(db)(simpleOKHandler)

	req1 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req1.Header.Set("Authorization", "Bearer "+rawKey)
	req1.RemoteAddr = "203.0.113.10:12345"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", w1.Code, http.StatusOK)
	}

	// Simulate process restart where in-memory tracker is reset.
	apiKeySourceTracker = newAPIKeySourceTracker(24 * time.Hour)
	handlerAfterRestart := APIKeyAuth(db)(simpleOKHandler)

	req2 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req2.Header.Set("Authorization", "Bearer "+rawKey)
	req2.RemoteAddr = "198.51.100.20:12345"
	w2 := httptest.NewRecorder()
	handlerAfterRestart.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("second request status = %d, want %d", w2.Code, http.StatusUnauthorized)
	}

	var isActive bool
	err := db.QueryRow(`SELECT is_active FROM api_keys WHERE key_prefix = ?`, model.ExtractAPIKeyPrefix(rawKey)).Scan(&isActive)
	if err != nil {
		t.Fatalf("failed to load key active status: %v", err)
	}
	if isActive {
		t.Fatal("expected key to be deactivated after source IP anomaly across tracker reset")
	}
}

func TestGetAPIKey_NoKey(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/test", nil)
	apiKey := GetAPIKey(req)

	if apiKey != nil {
		t.Error("expected nil API key when not in context")
	}
}

func TestOptionalAPIKeyAuth_NoHeader(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	var handlerCalled bool
	handler := OptionalAPIKeyAuth(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		apiKey := GetAPIKey(r)
		if apiKey != nil {
			t.Error("expected no API key in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("expected handler to be called")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestOptionalAPIKeyAuth_InvalidKey(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	var handlerCalled bool
	handler := OptionalAPIKeyAuth(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("expected handler to be called even with invalid key")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestOptionalAPIKeyAuth_ValidKey(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	rawKey := insertTestAPIKey(t, db, "Optional Key", []string{"pages:read"}, true, nil)

	w, receivedAPIKey := executeAuthAndCaptureKey(OptionalAPIKeyAuth, db, rawKey)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	if receivedAPIKey == nil {
		t.Fatal("expected API key to be in context")
	}
}

func TestRequirePermission_NoAPIKey(t *testing.T) {
	handler := RequirePermission("pages:read")(simpleOKHandler)
	w := executeRequest(handler, "GET", "/api/test")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestRequirePermission_HasPermission(t *testing.T) {
	handler := RequirePermission("pages:read")(simpleOKHandler)
	apiKey := store.ApiKey{ID: 1, Permissions: `["pages:read", "pages:write"]`}
	w := executeWithAPIKey(handler, apiKey)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestRequirePermission_LacksPermission(t *testing.T) {
	handler := RequirePermission("pages:write")(simpleOKHandler)
	apiKey := store.ApiKey{ID: 1, Permissions: `["pages:read"]`}
	w := executeWithAPIKey(handler, apiKey)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
	}
}

func TestRequireAnyPermission_HasOnePermission(t *testing.T) {
	handler := RequireAnyPermission("pages:read", "pages:write")(simpleOKHandler)
	apiKey := store.ApiKey{ID: 1, Permissions: `["pages:read"]`}
	w := executeWithAPIKey(handler, apiKey)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestRequireAnyPermission_LacksAllPermissions(t *testing.T) {
	handler := RequireAnyPermission("media:read", "media:write")(simpleOKHandler)
	apiKey := store.ApiKey{ID: 1, Permissions: `["pages:read"]`}
	w := executeWithAPIKey(handler, apiKey)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
	}
}

func TestAPIRateLimit_NoAPIKey(t *testing.T) {
	handler := APIRateLimit(10, 5)(simpleOKHandler)
	w := executeRequest(handler, "GET", "/api/test")

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestAPIRateLimit_WithAPIKey(t *testing.T) {
	handler := APIRateLimit(2, 2)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	apiKey := store.ApiKey{ID: 1}

	// First few requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		ctx := context.WithValue(req.Context(), ContextKeyAPIKey, apiKey)
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected status %d, got %d", i, http.StatusOK, w.Code)
		}
	}

	// Next request should be rate limited
	req := httptest.NewRequest("GET", "/api/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, apiKey)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}

func TestAPIRateLimit_DifferentKeys(t *testing.T) {
	handler := APIRateLimit(1, 1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First key exhausts its limit
	apiKey1 := store.ApiKey{ID: 1}
	req := httptest.NewRequest("GET", "/api/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, apiKey1)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("key1 first request: expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Second key should still be able to make requests
	apiKey2 := store.ApiKey{ID: 2}
	req = httptest.NewRequest("GET", "/api/test", nil)
	ctx = context.WithValue(req.Context(), ContextKeyAPIKey, apiKey2)
	req = req.WithContext(ctx)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("key2 first request: expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestGlobalRateLimiter(t *testing.T) {
	rl := NewGlobalRateLimiter(2, 2)
	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First few requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected status %d, got %d", i, http.StatusOK, w.Code)
		}
	}

	// Next request should be rate limited
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}

// testRateLimiterDifferentIPs is a helper for testing rate limiting with different IPs.
func testRateLimiterDifferentIPs(t *testing.T, handler http.Handler, path string) {
	t.Helper()
	// First IP exhausts its limit
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Second IP should still be able to make requests
	req = httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "192.168.1.2:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("second IP: expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestGlobalRateLimiter_DifferentIPs(t *testing.T) {
	rl := NewGlobalRateLimiter(1, 1)
	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	testRateLimiterDifferentIPs(t, handler, "/api/test")
}

// testRateLimiterProxyHeader is a helper for testing rate limiting with proxy headers.
func testRateLimiterProxyHeader(t *testing.T, handler http.Handler, headerName, headerValue string) {
	t.Helper()
	setTrustedProxiesForTest(t, "127.0.0.1/32")

	// First request with proxy header
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set(headerName, headerValue)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Second request from same proxy header should be limited
	req = httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set(headerName, headerValue)
	req.RemoteAddr = "127.0.0.1:12346"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}

func TestGlobalRateLimiter_XRealIP(t *testing.T) {
	rl := NewGlobalRateLimiter(1, 1)
	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	testRateLimiterProxyHeader(t, handler, "X-Real-IP", "10.0.0.1")
}

func TestGlobalRateLimiter_XForwardedFor(t *testing.T) {
	rl := NewGlobalRateLimiter(1, 1)
	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	testRateLimiterProxyHeader(t, handler, "X-Forwarded-For", "10.0.0.2")
}

func TestRequirePermission_NoPermissions(t *testing.T) {
	tests := []struct {
		name        string
		permissions string
	}{
		{"empty string", ""},
		{"empty array", "[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := RequirePermission("pages:read")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			apiKey := store.ApiKey{ID: 1, Permissions: tt.permissions}
			req := httptest.NewRequest("GET", "/api/test", nil)
			ctx := context.WithValue(req.Context(), ContextKeyAPIKey, apiKey)
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
			}
		})
	}
}

func TestGlobalRateLimiter_HTMLMiddleware(t *testing.T) {
	rl := NewGlobalRateLimiter(2, 2)
	handler := rl.HTMLMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First few requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/login", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected status %d, got %d", i, http.StatusOK, w.Code)
		}
	}

	// Next request should be rate limited with text response (not JSON)
	req := httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}

	// Verify response is plain text, not JSON
	body := w.Body.String()
	if body == "" {
		t.Error("expected non-empty response body")
	}
	// Should not be JSON (which starts with {)
	if body != "" && body[0] == '{' {
		t.Error("expected plain text response, got JSON")
	}
}

func TestGlobalRateLimiter_HTMLMiddleware_DifferentIPs(t *testing.T) {
	rl := NewGlobalRateLimiter(1, 1)
	handler := rl.HTMLMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	testRateLimiterDifferentIPs(t, handler, "/login")
}
