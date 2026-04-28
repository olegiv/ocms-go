// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestGetUser(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		user := GetUser(req)
		if user != nil {
			t.Errorf("GetUser() = %v, want nil", user)
		}
	})

	t.Run("user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		testUser := store.User{
			ID:    123,
			Email: "test@example.com",
			Role:  "admin",
			Name:  "Test User",
		}
		ctx := context.WithValue(req.Context(), ContextKeyUser, testUser)
		req = req.WithContext(ctx)

		user := GetUser(req)
		if user == nil {
			t.Fatal("GetUser() = nil, want user")
		}
		if user.ID != 123 {
			t.Errorf("GetUser().ID = %d, want 123", user.ID)
		}
		if user.Email != "test@example.com" {
			t.Errorf("GetUser().Email = %q, want %q", user.Email, "test@example.com")
		}
	})
}

func TestGetUserID(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		id := GetUserID(req)
		if id != 0 {
			t.Errorf("GetUserID() = %d, want 0", id)
		}
	})

	t.Run("user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		testUser := store.User{ID: 456}
		ctx := context.WithValue(req.Context(), ContextKeyUser, testUser)
		req = req.WithContext(ctx)

		id := GetUserID(req)
		if id != 456 {
			t.Errorf("GetUserID() = %d, want 456", id)
		}
	})
}

func TestGetUserIDPtr(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		idPtr := GetUserIDPtr(req)
		if idPtr != nil {
			t.Errorf("GetUserIDPtr() = %v, want nil", idPtr)
		}
	})

	t.Run("user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		testUser := store.User{ID: 789}
		ctx := context.WithValue(req.Context(), ContextKeyUser, testUser)
		req = req.WithContext(ctx)

		idPtr := GetUserIDPtr(req)
		if idPtr == nil {
			t.Fatal("GetUserIDPtr() = nil, want pointer")
		}
		if *idPtr != 789 {
			t.Errorf("*GetUserIDPtr() = %d, want 789", *idPtr)
		}
	})
}

func TestGetUserEmail(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		email := GetUserEmail(req)
		if email != "" {
			t.Errorf("GetUserEmail() = %q, want empty", email)
		}
	})

	t.Run("user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		testUser := store.User{Email: "user@example.com"}
		ctx := context.WithValue(req.Context(), ContextKeyUser, testUser)
		req = req.WithContext(ctx)

		email := GetUserEmail(req)
		if email != "user@example.com" {
			t.Errorf("GetUserEmail() = %q, want %q", email, "user@example.com")
		}
	})
}

func TestGetSiteName(t *testing.T) {
	t.Run("no site name in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		name := GetSiteName(req)
		if name != "oCMS" {
			t.Errorf("GetSiteName() = %q, want %q", name, "oCMS")
		}
	})

	t.Run("empty site name in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := context.WithValue(req.Context(), ContextKeySiteName, "")
		req = req.WithContext(ctx)

		name := GetSiteName(req)
		if name != "oCMS" {
			t.Errorf("GetSiteName() = %q, want %q (default)", name, "oCMS")
		}
	})

	t.Run("site name in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := context.WithValue(req.Context(), ContextKeySiteName, "My Site")
		req = req.WithContext(ctx)

		name := GetSiteName(req)
		if name != "My Site" {
			t.Errorf("GetSiteName() = %q, want %q", name, "My Site")
		}
	})
}

func TestRequestPath(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := GetRequestPath(r.Context())
		_, _ = w.Write([]byte(path))
	})

	wrapped := RequestPath(handler)

	req := httptest.NewRequest(http.MethodGet, "/admin/pages", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if body := rr.Body.String(); body != "/admin/pages" {
		t.Errorf("GetRequestPath() = %q, want %q", body, "/admin/pages")
	}
}

func TestGetRequestPath(t *testing.T) {
	t.Run("no path in context", func(t *testing.T) {
		ctx := context.Background()
		path := GetRequestPath(ctx)
		if path != "" {
			t.Errorf("GetRequestPath() = %q, want empty", path)
		}
	})

	t.Run("path in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyRequestPath, "/test/path")
		path := GetRequestPath(ctx)
		if path != "/test/path" {
			t.Errorf("GetRequestPath() = %q, want %q", path, "/test/path")
		}
	})
}

// newLoadUserTestSetup spins up an in-memory sqlite with the columns LoadUser
// touches, the sessions table SCS expects, an SCS manager, and a single user
// row at the requested session_version. Returns the wired-up scs manager,
// the user row, and the db (for callers that want to mutate state mid-test).
func newLoadUserTestSetup(t *testing.T, userSessionVersion int64) (*sql.DB, *scs.SessionManager, store.User) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'admin',
			name TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_login_at DATETIME,
			avatar TEXT NOT NULL DEFAULT '',
			bio TEXT NOT NULL DEFAULT '',
			website_url TEXT NOT NULL DEFAULT '',
			linkedin_url TEXT NOT NULL DEFAULT '',
			github_url TEXT NOT NULL DEFAULT '',
			telegram_url TEXT NOT NULL DEFAULT '',
			session_version INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE sessions (
			token TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			expiry REAL NOT NULL
		);
		CREATE INDEX sessions_expiry_idx ON sessions(expiry);
	`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	res, err := db.Exec(
		`INSERT INTO users (email, name, role, session_version) VALUES (?, ?, 'admin', ?)`,
		"u@example.com", "Test", userSessionVersion,
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, _ := res.LastInsertId()

	user, err := store.New(db).GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	sm := scs.New()
	sm.Store = sqlite3store.New(db)
	sm.Lifetime = time.Hour
	return db, sm, user
}

// runWithSeededSession runs the given middleware after seeding the session
// with a userID and a recorded session_version. Returns the recorder so the
// caller can assert response code, headers, and body.
func runWithSeededSession(t *testing.T, sm *scs.SessionManager, mw func(http.Handler) http.Handler, sessionUserID, sessionVersion int64) *httptest.ResponseRecorder {
	t.Helper()

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user := GetUser(r); user != nil {
			w.Header().Set("X-Loaded-User", user.Email)
		}
		w.WriteHeader(http.StatusOK)
	})
	wrapped := mw(final)

	chain := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), SessionKeyUserID, sessionUserID)
		sm.Put(r.Context(), SessionKeyUserSessionVersion, sessionVersion)
		wrapped.ServeHTTP(w, r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)
	return rr
}

func TestLoadUser_DestroysSessionWhenVersionMismatch(t *testing.T) {
	db, sm, user := newLoadUserTestSetup(t, 5)

	rr := runWithSeededSession(t, sm, LoadUser(sm, db), user.ID, 3) // session is older than user
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d (redirect to login)", rr.Code, http.StatusSeeOther)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
	if got := rr.Header().Get("X-Loaded-User"); got != "" {
		t.Errorf("user was loaded into context despite stale session: %q", got)
	}
}

func TestLoadUser_AllowsRequestWhenVersionMatches(t *testing.T) {
	db, sm, user := newLoadUserTestSetup(t, 5)

	rr := runWithSeededSession(t, sm, LoadUser(sm, db), user.ID, 5)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("X-Loaded-User"); got != user.Email {
		t.Errorf("X-Loaded-User = %q, want %q", got, user.Email)
	}
}

func TestLoadUser_AllowsPreMigrationSessions(t *testing.T) {
	// Pre-migration users default to session_version=0 and pre-migration
	// sessions also have no recorded value (zero). They must remain valid.
	db, sm, user := newLoadUserTestSetup(t, 0)

	rr := runWithSeededSession(t, sm, LoadUser(sm, db), user.ID, 0)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("X-Loaded-User"); got != user.Email {
		t.Errorf("X-Loaded-User = %q, want %q", got, user.Email)
	}
}

// OptionalLoadUser shares isSessionStale with LoadUser but takes a different
// branch on stale: continue to the next handler with no user, no redirect.

func TestOptionalLoadUser_DropsSessionWhenVersionMismatch(t *testing.T) {
	db, sm, user := newLoadUserTestSetup(t, 5)

	rr := runWithSeededSession(t, sm, OptionalLoadUser(sm, db), user.ID, 3)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (must continue without user, never redirect)", rr.Code, http.StatusOK)
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want empty (no redirect on optional path)", loc)
	}
	if got := rr.Header().Get("X-Loaded-User"); got != "" {
		t.Errorf("user was loaded into context despite stale session: %q", got)
	}
}

func TestOptionalLoadUser_AllowsRequestWhenVersionMatches(t *testing.T) {
	db, sm, user := newLoadUserTestSetup(t, 5)

	rr := runWithSeededSession(t, sm, OptionalLoadUser(sm, db), user.ID, 5)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("X-Loaded-User"); got != user.Email {
		t.Errorf("X-Loaded-User = %q, want %q", got, user.Email)
	}
}

func TestOptionalLoadUser_AllowsPreMigrationSessions(t *testing.T) {
	db, sm, user := newLoadUserTestSetup(t, 0)

	rr := runWithSeededSession(t, sm, OptionalLoadUser(sm, db), user.ID, 0)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("X-Loaded-User"); got != user.Email {
		t.Errorf("X-Loaded-User = %q, want %q", got, user.Email)
	}
}

func TestLoadSiteConfig(t *testing.T) {
	t.Run("without cache manager uses default", func(t *testing.T) {
		// Create middleware without cache (nil cacheManager)
		middleware := LoadSiteConfig(nil, nil)

		var capturedSiteName string
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedSiteName = GetSiteName(r)
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
		rr := httptest.NewRecorder()

		middleware(handler).ServeHTTP(rr, req)

		// Should use default "oCMS" when no cache and no DB
		if capturedSiteName != "oCMS" {
			t.Errorf("GetSiteName() = %q, want %q", capturedSiteName, "oCMS")
		}
	})

	t.Run("stores site name in context", func(t *testing.T) {
		// LoadSiteConfig with nil cache and nil db should set default
		middleware := LoadSiteConfig(nil, nil)

		var capturedCtx context.Context
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()

		middleware(handler).ServeHTTP(rr, req)

		// Verify context has site name
		siteName, ok := capturedCtx.Value(ContextKeySiteName).(string)
		if !ok {
			t.Fatal("site name not stored in context")
		}
		if siteName != "oCMS" {
			t.Errorf("context site name = %q, want %q", siteName, "oCMS")
		}
	})
}
