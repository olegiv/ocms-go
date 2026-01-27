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

	return db
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
	keyHash := model.HashAPIKey(rawKey)

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

func TestGlobalRateLimiter_DifferentIPs(t *testing.T) {
	rl := NewGlobalRateLimiter(1, 1)
	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First IP exhausts its limit
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Second IP should still be able to make requests
	req = httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.2:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("second IP: expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestGlobalRateLimiter_XRealIP(t *testing.T) {
	rl := NewGlobalRateLimiter(1, 1)
	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request with X-Real-IP
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Real-IP", "10.0.0.1")
	req.RemoteAddr = "127.0.0.1:12345" // Proxy address
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Second request from same X-Real-IP should be limited
	req = httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Real-IP", "10.0.0.1")
	req.RemoteAddr = "127.0.0.1:12346"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}

func TestGlobalRateLimiter_XForwardedFor(t *testing.T) {
	rl := NewGlobalRateLimiter(1, 1)
	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request with X-Forwarded-For
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.2")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Second request from same X-Forwarded-For should be limited
	req = httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.2")
	req.RemoteAddr = "127.0.0.1:12346"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}

func TestRequirePermission_EmptyPermissions(t *testing.T) {
	handler := RequirePermission("pages:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// API key with empty permissions
	apiKey := store.ApiKey{
		ID:          1,
		Permissions: "",
	}
	req := httptest.NewRequest("GET", "/api/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, apiKey)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
	}
}

func TestRequirePermission_EmptyArrayPermissions(t *testing.T) {
	handler := RequirePermission("pages:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// API key with empty array permissions
	apiKey := store.ApiKey{
		ID:          1,
		Permissions: "[]",
	}
	req := httptest.NewRequest("GET", "/api/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, apiKey)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
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
	// Match the working test exactly: rate=1, burst=1
	rl := NewGlobalRateLimiter(1, 1)
	handler := rl.HTMLMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First IP exhausts its limit
	req := httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Second IP should still be able to make requests
	req = httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "192.168.1.2:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("second IP: expected status %d, got %d", http.StatusOK, w.Code)
	}
}
