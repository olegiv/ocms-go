// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/middleware"
)

func newLanguageRouteTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open language route database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
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
		INSERT INTO languages (code, name, native_name, is_default, is_active, position) VALUES
			('en', 'English', 'English', 1, 1, 0),
			('fr', 'French', 'Français', 0, 1, 1),
			('zh-hans', 'Chinese', '简体中文', 0, 1, 2),
			('abcdefghij', 'Maximum', 'Maximum', 0, 1, 3),
			('a1-b2', 'Alphanumeric', 'Alphanumeric', 0, 1, 4),
			('de', 'German', 'Deutsch', 0, 0, 5),
			('blog', 'Legacy reserved', 'Legacy reserved', 0, 1, 6);
	`)
	if err != nil {
		t.Fatalf("create language route schema: %v", err)
	}

	return db
}

func TestMountLanguageAwareFrontendRoutes(t *testing.T) {
	root := chi.NewRouter()
	root.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("parent-health"))
	})

	rewriteBeforeRouting := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			routeCtx := chi.RouteContext(r.Context())
			if routeCtx.RoutePath == "/fr/article" {
				routeCtx.RoutePath = "/article"
			}
			next.ServeHTTP(w, r)
		})
	}

	mountLanguageAwareFrontendRoutes(root, rewriteBeforeRouting, func(frontend chi.Router) {
		frontend.Get("/{slug}", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("frontend-" + chi.URLParam(r, "slug")))
		})
		frontend.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "frontend-notfound", http.StatusNotFound)
		})
	})
	root.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "root-notfound", http.StatusNotFound)
	})

	// Parent core routes may be registered after the frontend mount and must
	// still outrank its catch-all page-slug route.
	root.Get("/login", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("parent-login"))
	})
	root.Get("/forms/{slug}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("parent-form-" + chi.URLParam(r, "slug")))
	})
	root.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("parent-static"))
	}))

	tests := []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{path: "/fr/article", wantStatus: http.StatusOK, wantBody: "frontend-article"},
		{path: "/article", wantStatus: http.StatusOK, wantBody: "frontend-article"},
		{path: "/fr/article/extra", wantStatus: http.StatusNotFound, wantBody: "frontend-notfound\n"},
		{path: "/health", wantStatus: http.StatusOK, wantBody: "parent-health"},
		{path: "/login", wantStatus: http.StatusOK, wantBody: "parent-login"},
		{path: "/forms/contact", wantStatus: http.StatusOK, wantBody: "parent-form-contact"},
		{path: "/static/dist/app.css", wantStatus: http.StatusOK, wantBody: "parent-static"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()
			root.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantBody != "" && rr.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rr.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestLanguageAwareFrontendRouteMatrix(t *testing.T) {
	root := chi.NewRouter()

	writeSelection := func(route string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			langCode := ""
			if lang := middleware.GetLanguage(r); lang != nil {
				langCode = lang.Code
			}
			_, _ = fmt.Fprintf(
				w,
				"%s|%s|%s|%s",
				route,
				langCode,
				middleware.GetLanguagePrefix(r),
				chi.URLParam(r, "slug"),
			)
		}
	}

	mountLanguageAwareFrontendRoutes(root, middleware.Language(newLanguageRouteTestDB(t)), func(frontend chi.Router) {
		frontend.Get("/", writeSelection("home"))
		frontend.Get("/blog", writeSelection("blog"))
		frontend.Get("/{slug}", writeSelection("page"))
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "two character active prefix beats query", path: "/fr/article?lang=en", wantStatus: http.StatusOK, wantBody: "page|fr|fr|article"},
		{name: "hyphenated active prefix", path: "/zh-hans/article", wantStatus: http.StatusOK, wantBody: "page|zh-hans|zh-hans|article"},
		{name: "ten character active prefix", path: "/abcdefghij", wantStatus: http.StatusOK, wantBody: "home|abcdefghij|abcdefghij|"},
		{name: "alphanumeric active prefix", path: "/a1-b2/article", wantStatus: http.StatusOK, wantBody: "page|a1-b2|a1-b2|article"},
		{name: "inactive one segment is a page slug", path: "/de", wantStatus: http.StatusOK, wantBody: "page|en||de"},
		{name: "inactive nested prefix is unknown", path: "/de/article", wantStatus: http.StatusNotFound},
		{name: "unknown nested prefix is unknown", path: "/zz/article", wantStatus: http.StatusNotFound},
		{name: "legacy reserved code remains core route", path: "/blog", wantStatus: http.StatusOK, wantBody: "blog|en||"},
		{name: "legacy reserved nested prefix is unknown", path: "/blog/article", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()
			root.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantBody != "" && rr.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rr.Body.String(), tt.wantBody)
			}
		})
	}
}
