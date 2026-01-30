// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package session

import (
	"database/sql"
	"net/http"
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

	// Check cookie settings
	if !sm.Cookie.HttpOnly {
		t.Error("expected Cookie.HttpOnly = true")
	}
	if sm.Cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("expected SameSite = Lax, got %v", sm.Cookie.SameSite)
	}
}

func TestNew_StoreInitialized(t *testing.T) {
	db := setupTestDB(t)

	sm := New(db, true)

	if sm.Store == nil {
		t.Error("expected Store to be initialized")
	}
}
