// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"embed"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/theme"
)

func TestRegisteredFeedRoutes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if _, err := db.Exec(`
		INSERT INTO config (key, value, type, language_code) VALUES ('site_url', 'https://feeds.example', 'string', 'en')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
		INSERT INTO languages (code, name, native_name) VALUES ('ru', 'Russian', 'Русский');
		INSERT INTO categories (name, slug, language_code) VALUES ('Tech', 'tech', 'en'), ('Технологии', 'tech-ru', 'ru');
		INSERT INTO tags (name, slug, language_code) VALUES ('Go', 'go', 'en'), ('Го', 'go-ru', 'ru');
	`); err != nil {
		t.Fatal(err)
	}
	var emptyFS embed.FS
	themes := theme.NewManager(emptyFS, "", testutil.TestLoggerSilent())
	h := handler.NewFrontendHandler(db, themes, nil, testutil.TestLoggerSilent(), nil, nil)
	root := chi.NewRouter()
	root.Get("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mountLanguageAwareFrontendRoutes(root, middleware.Language(db), func(frontend chi.Router) {
		registerFrontendRoutes(frontend, h)
		frontend.NotFound(h.NotFound)
	})
	for _, format := range []string{"rss", "atom"} {
		for _, scope := range []string{"/", "/en/", "/ru/", "/category/tech/", "/tag/go/", "/ru/category/tech-ru/", "/ru/tag/go-ru/"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				path := scope + format + ".xml"
				req := httptest.NewRequest(method, path+"?lang=ru", nil)
				w := httptest.NewRecorder()
				root.ServeHTTP(w, req)
				if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/"+format+"+xml") {
					t.Fatalf("%s %s: status=%d headers=%v body=%s", method, path, w.Code, w.Header(), w.Body.String())
				}
				if method == http.MethodHead {
					if w.Body.Len() != 0 {
						t.Error("HEAD feed response has a body")
					}
					continue
				}
				var doc struct{ XMLName xml.Name }
				if err := xml.Unmarshal(w.Body.Bytes(), &doc); err != nil ||
					(format == "atom" && doc.XMLName.Space != "http://www.w3.org/2005/Atom") {
					t.Errorf("invalid %s document: %v", format, err)
				}
			}
		}
	}
	for _, path := range []string{"/unknown/rss.xml", "/ru/category/tech/atom.xml", "/category/tech-ru/rss.xml"} {
		w := httptest.NewRecorder()
		root.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s must remain 404, got %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	root.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("feed registration shadowed parent health route: %d", w.Code)
	}
}
