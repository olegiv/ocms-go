// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package session

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	db.SetMaxOpenConns(1)

	// Create sessions table required by sqlite3store
	_, err = db.Exec(`
		CREATE TABLE sessions (
			token TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			expiry REAL NOT NULL
		);
		CREATE INDEX sessions_expiry_idx ON sessions(expiry);
	`)
	if err != nil {
		t.Fatalf("failed to create sessions table: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNew(t *testing.T) {
	db := setupTestDB(t)

	sm := New(db, true)

	if sm == nil {
		t.Fatal("expected session manager to be non-nil")
	}
}

func TestNew_DevMode(t *testing.T) {
	db := setupTestDB(t)

	// Development mode
	sm := New(db, true)

	if sm.Cookie.Secure {
		t.Error("expected Cookie.Secure = false in dev mode")
	}
	if sm.Cookie.Name == "__Host-session" {
		t.Error("expected default cookie name in dev mode")
	}
}

func TestNew_ProductionMode(t *testing.T) {
	db := setupTestDB(t)

	// Production mode
	sm := New(db, false)

	if !sm.Cookie.Secure {
		t.Error("expected Cookie.Secure = true in production mode")
	}
	if sm.Cookie.Name != "__Host-session" {
		t.Errorf("expected __Host-session cookie name, got %q", sm.Cookie.Name)
	}
	if sm.Cookie.Path != "/" {
		t.Errorf("expected Cookie.Path = '/', got %q", sm.Cookie.Path)
	}
}

func TestNew_SessionSettings(t *testing.T) {
	db := setupTestDB(t)

	sm := New(db, true)

	// Check session lifetime
	if sm.Lifetime != 24*time.Hour {
		t.Errorf("Lifetime = %v, want 24h", sm.Lifetime)
	}
	if sm.IdleTimeout != 2*time.Hour {
		t.Errorf("IdleTimeout = %v, want 2h", sm.IdleTimeout)
	}

	// Check cookie settings
	if !sm.Cookie.HttpOnly {
		t.Error("expected Cookie.HttpOnly = true")
	}
	if sm.Cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("expected SameSite = Strict, got %v", sm.Cookie.SameSite)
	}
}

// TestNew_SameSiteStrictInProduction is the drift test for audit finding
// FIND-006: the production session cookie must be SameSite=Strict so a
// crafted link in a third-party context cannot ride an authenticated
// admin session on cross-site top-level navigation. Returning to Lax
// would re-open the CSRF-via-link-click surface.
func TestNew_SameSiteStrictInProduction(t *testing.T) {
	db := setupTestDB(t)

	for _, isDev := range []bool{false, true} {
		sm := New(db, isDev)
		if sm.Cookie.SameSite != http.SameSiteStrictMode {
			t.Errorf("isDev=%v: SameSite = %v, want Strict", isDev, sm.Cookie.SameSite)
		}
	}
}

func TestNew_StoreInitialized(t *testing.T) {
	db := setupTestDB(t)

	sm := New(db, true)

	if sm.Store == nil {
		t.Error("expected Store to be initialized")
	}
}

func TestSessionCookieIsHashedInStore(t *testing.T) {
	db := setupTestDB(t)
	sm := New(db, false)
	handler := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sm.Put(r.Context(), "authenticated", true)
		}
		if !sm.GetBool(r.Context(), "authenticated") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "https://cms.test/session", nil))
	cookies := w.Result().Cookies()
	if w.Code != http.StatusNoContent || len(cookies) != 1 || cookies[0].Value == "" {
		t.Fatalf("session creation: status %d, cookies %d", w.Code, len(cookies))
	}
	cookie := cookies[0]
	var stored string
	if err := db.QueryRow("SELECT token FROM sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	if stored == cookie.Value || stored != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatal("database did not store a hash of the session cookie")
	}
	for _, tc := range []struct {
		value string
		want  int
	}{{cookie.Value, http.StatusNoContent}, {stored, http.StatusUnauthorized}} {
		r := httptest.NewRequest(http.MethodGet, "https://cms.test/session", nil)
		r.AddCookie(&http.Cookie{Name: cookie.Name, Value: tc.value})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("session request status=%d, want %d", w.Code, tc.want)
		}
	}
}
