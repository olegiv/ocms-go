package handler

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"

	"ocms-go/internal/store"
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
			content TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			author_id INTEGER NOT NULL,
			category_id INTEGER,
			featured_image_id INTEGER,
			meta_title TEXT,
			meta_description TEXT,
			meta_keywords TEXT,
			og_title TEXT,
			og_description TEXT,
			og_image TEXT,
			canonical_url TEXT,
			structured_data TEXT,
			no_index BOOLEAN NOT NULL DEFAULT 0,
			scheduled_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (author_id) REFERENCES users(id)
		);
		CREATE INDEX idx_pages_slug ON pages(slug);
		CREATE INDEX idx_pages_status ON pages(status);
		CREATE INDEX idx_pages_author_id ON pages(author_id);

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

		CREATE TABLE media (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			filename TEXT NOT NULL,
			original_filename TEXT NOT NULL,
			filepath TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size INTEGER NOT NULL,
			alt_text TEXT DEFAULT '',
			title TEXT DEFAULT '',
			description TEXT DEFAULT '',
			folder_id INTEGER,
			uploaded_by INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (uploaded_by) REFERENCES users(id)
		);

		CREATE TABLE forms (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			description TEXT DEFAULT '',
			success_message TEXT DEFAULT 'Thank you for your submission.',
			redirect_url TEXT DEFAULT '',
			email_notification BOOLEAN NOT NULL DEFAULT 0,
			notification_email TEXT DEFAULT '',
			is_active BOOLEAN NOT NULL DEFAULT 1,
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
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (form_id) REFERENCES forms(id) ON DELETE CASCADE
		);

		CREATE TABLE webhooks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			secret TEXT DEFAULT '',
			events TEXT NOT NULL DEFAULT '[]',
			is_active BOOLEAN NOT NULL DEFAULT 1,
			retry_count INTEGER NOT NULL DEFAULT 3,
			timeout_seconds INTEGER NOT NULL DEFAULT 30,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE webhook_deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			webhook_id INTEGER NOT NULL,
			event TEXT NOT NULL,
			payload TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			response_code INTEGER,
			response_body TEXT,
			attempt INTEGER NOT NULL DEFAULT 0,
			next_retry_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			delivered_at DATETIME,
			FOREIGN KEY (webhook_id) REFERENCES webhooks(id) ON DELETE CASCADE
		);

		CREATE TABLE config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT NOT NULL UNIQUE,
			value TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		INSERT INTO config (key, value) VALUES ('site_name', 'Test Site');
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
