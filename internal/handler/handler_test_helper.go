// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

// testDB creates an in-memory SQLite database with the required schema for testing.
func testDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create required tables
	schema := `
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'editor',
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_login_at DATETIME
		);
		CREATE INDEX idx_users_email ON users(email);
		CREATE INDEX idx_users_role ON users(role);

		CREATE TABLE sessions (
			token TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			expiry REAL NOT NULL
		);
		CREATE INDEX idx_sessions_expiry ON sessions(expiry);

		CREATE TABLE pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			body TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			author_id INTEGER NOT NULL,
			featured_image_id INTEGER,
			meta_title TEXT NOT NULL DEFAULT '',
			meta_description TEXT NOT NULL DEFAULT '',
			meta_keywords TEXT NOT NULL DEFAULT '',
			og_image_id INTEGER,
			no_index INTEGER NOT NULL DEFAULT 0,
			no_follow INTEGER NOT NULL DEFAULT 0,
			canonical_url TEXT NOT NULL DEFAULT '',
			scheduled_at DATETIME,
			language_code TEXT NOT NULL DEFAULT 'en',
			hide_featured_image INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			published_at DATETIME,
			FOREIGN KEY (author_id) REFERENCES users(id)
		);
		CREATE INDEX idx_pages_slug ON pages(slug);
		CREATE INDEX idx_pages_status ON pages(status);
		CREATE INDEX idx_pages_author_id ON pages(author_id);

		CREATE TABLE page_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			alias TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX idx_page_aliases_alias ON page_aliases(alias);
		CREATE INDEX idx_page_aliases_page_id ON page_aliases(page_id);

		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL DEFAULT 'info',
			category TEXT NOT NULL DEFAULT 'system',
			message TEXT NOT NULL,
			user_id INTEGER,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
		);
		CREATE INDEX idx_events_level ON events(level);
		CREATE INDEX idx_events_category ON events(category);
		CREATE INDEX idx_events_user_id ON events(user_id);
		CREATE INDEX idx_events_created_at ON events(created_at DESC);

		CREATE TABLE languages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			native_name TEXT NOT NULL,
			is_default BOOLEAN NOT NULL DEFAULT 0,
			is_active BOOLEAN NOT NULL DEFAULT 1,
			direction TEXT NOT NULL DEFAULT 'ltr',
			position INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX idx_languages_code ON languages(code);
		CREATE INDEX idx_languages_active ON languages(is_active);
		CREATE INDEX idx_languages_default ON languages(is_default);

		INSERT INTO languages (code, name, native_name, is_default, is_active, direction, position)
		VALUES ('en', 'English', 'English', 1, 1, 'ltr', 0);

		CREATE TABLE media_folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			parent_id INTEGER REFERENCES media_folders(id) ON DELETE CASCADE,
			position INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE media (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			uuid TEXT NOT NULL UNIQUE,
			filename TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size INTEGER NOT NULL,
			width INTEGER,
			height INTEGER,
			alt TEXT,
			caption TEXT,
			folder_id INTEGER,
			uploaded_by INTEGER NOT NULL,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (uploaded_by) REFERENCES users(id),
			FOREIGN KEY (folder_id) REFERENCES media_folders(id) ON DELETE SET NULL
		);

		CREATE TABLE forms (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			success_message TEXT DEFAULT 'Thank you for your submission.',
			email_to TEXT DEFAULT '',
			is_active BOOLEAN NOT NULL DEFAULT 1,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(slug, language_code)
		);

		CREATE TABLE form_fields (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			form_id INTEGER NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			label TEXT NOT NULL,
			placeholder TEXT DEFAULT '',
			help_text TEXT DEFAULT '',
			options TEXT DEFAULT '[]',
			validation TEXT DEFAULT '{}',
			is_required BOOLEAN NOT NULL DEFAULT 0,
			position INTEGER NOT NULL DEFAULT 0,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE form_submissions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			form_id INTEGER NOT NULL,
			data TEXT NOT NULL DEFAULT '{}',
			is_read BOOLEAN NOT NULL DEFAULT 0,
			ip_address TEXT DEFAULT '',
			user_agent TEXT DEFAULT '',
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (form_id) REFERENCES forms(id) ON DELETE CASCADE
		);

		CREATE TABLE webhooks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			secret TEXT NOT NULL,
			events TEXT NOT NULL DEFAULT '[]',
			is_active BOOLEAN NOT NULL DEFAULT 1,
			headers TEXT NOT NULL DEFAULT '{}',
			created_by INTEGER NOT NULL REFERENCES users(id),
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE webhook_deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			webhook_id INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
			event TEXT NOT NULL,
			payload TEXT NOT NULL,
			response_code INTEGER,
			response_body TEXT DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			next_retry_at DATETIME,
			delivered_at DATETIME,
			status TEXT NOT NULL DEFAULT 'pending',
			error_message TEXT DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'string',
			description TEXT NOT NULL DEFAULT '',
			language_code TEXT NOT NULL DEFAULT 'en',
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_by INTEGER,
			FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL
		);

		INSERT INTO config (key, value, type, language_code) VALUES ('site_name', 'Test Site', 'string', 'en');

		CREATE TABLE tags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX idx_tags_slug ON tags(slug);

		CREATE TABLE categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			description TEXT,
			parent_id INTEGER,
			position INTEGER NOT NULL DEFAULT 0,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE SET NULL
		);
		CREATE INDEX idx_categories_slug ON categories(slug);
		CREATE INDEX idx_categories_parent ON categories(parent_id);

		CREATE TABLE page_tags (
			page_id INTEGER NOT NULL,
			tag_id INTEGER NOT NULL,
			PRIMARY KEY (page_id, tag_id),
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE,
			FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
		);

		CREATE TABLE page_categories (
			page_id INTEGER NOT NULL,
			category_id INTEGER NOT NULL,
			PRIMARY KEY (page_id, category_id),
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE,
			FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
		);

		CREATE TABLE translations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			entity_type TEXT NOT NULL,
			entity_id INTEGER NOT NULL,
			language_id INTEGER NOT NULL,
			translation_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (language_id) REFERENCES languages(id) ON DELETE CASCADE
		);
		CREATE INDEX idx_translations_entity ON translations(entity_type, entity_id);

		CREATE TABLE menus (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(slug, language_code)
		);

		CREATE TABLE menu_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			menu_id INTEGER NOT NULL,
			parent_id INTEGER,
			title TEXT NOT NULL,
			url TEXT NOT NULL DEFAULT '',
			page_id INTEGER,
			category_id INTEGER,
			target TEXT NOT NULL DEFAULT '_self',
			css_class TEXT NOT NULL DEFAULT '',
			icon TEXT NOT NULL DEFAULT '',
			position INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (menu_id) REFERENCES menus(id) ON DELETE CASCADE,
			FOREIGN KEY (parent_id) REFERENCES menu_items(id) ON DELETE SET NULL,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE SET NULL,
			FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL
		);
		CREATE INDEX idx_menu_items_menu ON menu_items(menu_id);
		CREATE INDEX idx_menu_items_position ON menu_items(position);

		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			key_prefix TEXT NOT NULL,
			permissions TEXT NOT NULL DEFAULT '[]',
			last_used_at DATETIME,
			expires_at DATETIME,
			is_active BOOLEAN NOT NULL DEFAULT 1,
			created_by INTEGER NOT NULL REFERENCES users(id),
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX idx_api_keys_prefix ON api_keys(key_prefix);
		CREATE INDEX idx_api_keys_active ON api_keys(is_active);

		CREATE TABLE widgets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			theme TEXT NOT NULL,
			area TEXT NOT NULL,
			widget_type TEXT NOT NULL,
			title TEXT,
			content TEXT,
			settings TEXT,
			position INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 1,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX idx_widgets_theme ON widgets(theme);
		CREATE INDEX idx_widgets_area ON widgets(area);
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create test schema: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

// testSessionManager creates a session manager for testing.
func testSessionManager(t *testing.T) *scs.SessionManager {
	t.Helper()
	sm := scs.New()
	sm.Lifetime = 24 * time.Hour
	return sm
}

// testUser is a test user for testing.
type testUser struct {
	ID           int64
	Email        string
	Name         string
	Role         string
	PasswordHash string
}

// createTestUser creates a test user in the database.
func createTestUser(t *testing.T, db *sql.DB, user testUser) store.User {
	t.Helper()

	if user.PasswordHash == "" {
		// Default password hash for "password123"
		user.PasswordHash = "$argon2id$v=19$m=65536,t=1,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG"
	}

	now := time.Now()
	result, err := db.Exec(
		`INSERT INTO users (email, password_hash, role, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		user.Email, user.PasswordHash, user.Role, user.Name, now, now,
	)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	id, _ := result.LastInsertId()
	return store.User{
		ID:           id,
		Email:        user.Email,
		PasswordHash: user.PasswordHash,
		Role:         user.Role,
		Name:         user.Name,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// createTestAdminUser creates a standard admin user for tests.
func createTestAdminUser(t *testing.T, db *sql.DB) store.User {
	t.Helper()
	return createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin",
		Role:  "admin",
	})
}

// requestWithURLParams adds chi URL parameters to a request.
func requestWithURLParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// requestWithSession wraps a request with session context.
func requestWithSession(sm *scs.SessionManager, r *http.Request) *http.Request {
	ctx, err := sm.Load(r.Context(), "")
	if err != nil {
		return r
	}
	return r.WithContext(ctx)
}

// assertStatus checks if the response status code matches the expected value.
func assertStatus(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("status = %d; want %d", got, want)
	}
}

// testHandlerSetup creates a test database and session manager for handler tests.
func testHandlerSetup(t *testing.T) (*sql.DB, *scs.SessionManager) {
	t.Helper()
	return testDB(t), testSessionManager(t)
}

// newTestHealthHandler creates a HealthHandler with a test database and temp uploads directory.
func newTestHealthHandler(t *testing.T) *HealthHandler {
	t.Helper()
	return NewHealthHandler(testDB(t), testSessionManager(t), t.TempDir())
}

// addUserToContext adds a user to the request context (simulating middleware).
func addUserToContext(r *http.Request, user *store.User) *http.Request {
	ctx := r.Context()
	return r.WithContext(context.WithValue(ctx, middleware.ContextKeyUser, *user))
}

// newAuthenticatedDeleteRequest creates a DELETE request with URL params, session, and user context.
func newAuthenticatedDeleteRequest(t *testing.T, sm *scs.SessionManager, path string, params map[string]string, user *store.User) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	req = requestWithURLParams(req, params)
	req = requestWithSession(sm, req)
	req = addUserToContext(req, user)
	return req, httptest.NewRecorder()
}
