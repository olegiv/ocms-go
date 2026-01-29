// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/store"
)

// testDB creates an in-memory SQLite database with taxonomy tables for testing.
func testDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	schema := `
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

		INSERT INTO languages (code, name, native_name, is_default, is_active)
		VALUES ('en', 'English', 'English', 1, 1);

		CREATE TABLE tags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			language_id INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (language_id) REFERENCES languages(id)
		);
		CREATE INDEX idx_tags_slug ON tags(slug);

		CREATE TABLE categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			description TEXT,
			parent_id INTEGER,
			position INTEGER NOT NULL DEFAULT 0,
			language_id INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE SET NULL,
			FOREIGN KEY (language_id) REFERENCES languages(id)
		);
		CREATE INDEX idx_categories_slug ON categories(slug);
		CREATE INDEX idx_categories_parent_id ON categories(parent_id);

		CREATE TABLE pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			content TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			author_id INTEGER NOT NULL,
			category_id INTEGER,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

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
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create test schema: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

// testSetup creates a test database and API handler for testing.
func testSetup(t *testing.T) (*sql.DB, *Handler) {
	t.Helper()
	db := testDB(t)
	return db, NewHandler(db)
}

// createTestTag creates a test tag in the database.
func createTestTag(t *testing.T, db *sql.DB, name, slug string) store.Tag {
	t.Helper()
	now := time.Now()

	result, err := db.Exec(
		`INSERT INTO tags (name, slug, language_id, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`,
		name, slug, now, now,
	)
	if err != nil {
		t.Fatalf("failed to create test tag: %v", err)
	}

	id, _ := result.LastInsertId()
	return store.Tag{
		ID:         id,
		Name:       name,
		Slug:       slug,
		LanguageID: 1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// createTestCategory creates a test category in the database.
func createTestCategory(t *testing.T, db *sql.DB, name, slug string, parentID *int64) store.Category {
	t.Helper()
	now := time.Now()

	var result sql.Result
	var err error

	if parentID != nil {
		result, err = db.Exec(
			`INSERT INTO categories (name, slug, parent_id, language_id, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)`,
			name, slug, *parentID, now, now,
		)
	} else {
		result, err = db.Exec(
			`INSERT INTO categories (name, slug, language_id, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`,
			name, slug, now, now,
		)
	}
	if err != nil {
		t.Fatalf("failed to create test category: %v", err)
	}

	id, _ := result.LastInsertId()
	cat := store.Category{
		ID:         id,
		Name:       name,
		Slug:       slug,
		LanguageID: 1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if parentID != nil {
		cat.ParentID = sql.NullInt64{Int64: *parentID, Valid: true}
	}
	return cat
}

// requestWithURLParams adds chi URL parameters to a request.
func requestWithURLParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// newJSONRequest creates an HTTP request with JSON body and optional URL params.
func newJSONRequest(t *testing.T, method, path string, body string, params map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if len(params) > 0 {
		req = requestWithURLParams(req, params)
	}
	return req
}

// newGetRequest creates an HTTP GET request with optional URL params.
func newGetRequest(t *testing.T, path string, params map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if len(params) > 0 {
		req = requestWithURLParams(req, params)
	}
	return req
}

// newDeleteRequest creates an HTTP DELETE request with optional URL params.
func newDeleteRequest(t *testing.T, path string, params map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if len(params) > 0 {
		req = requestWithURLParams(req, params)
	}
	return req
}

// dataResponse is a generic wrapper for API responses with a "data" field.
type dataResponse[T any] struct {
	Data T `json:"data"`
}

// listResponse is a generic wrapper for API list responses with data and meta.
type listResponse[T any] struct {
	Data []T   `json:"data"`
	Meta *Meta `json:"meta"`
}

// unmarshalData unmarshals a JSON response body into the specified type.
func unmarshalData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var resp dataResponse[T]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	return resp.Data
}

// unmarshalList unmarshals a JSON list response body into the specified type.
func unmarshalList[T any](t *testing.T, w *httptest.ResponseRecorder) ([]T, *Meta) {
	t.Helper()
	var resp listResponse[T]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	return resp.Data, resp.Meta
}

// executeHandler executes a handler and returns the response recorder.
func executeHandler(t *testing.T, handler func(http.ResponseWriter, *http.Request), req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}
