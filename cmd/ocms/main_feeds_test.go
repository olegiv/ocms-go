// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"embed"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

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
	sessions := scs.New()
	root := chi.NewRouter()
	root.Use(chimw.Logger, chimw.Recoverer, chimw.Compress(5), chimw.GetHead,
		middleware.Timeout(30*time.Second), middleware.StripTrailingSlash,
		middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(true)),
		middleware.RequestPath, sessions.LoadAndSave)
	root.Get("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mountLanguageAwareFrontendRoutes(root, middleware.Language(db), func(frontend chi.Router) {
		frontend.Use(middleware.OptionalLoadUser(sessions, db), middleware.LinkHeaders)
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

	server := httptest.NewServer(root)
	t.Cleanup(server.Close)
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	t.Cleanup(client.CloseIdleConnections)
	checkHeaders := func(path, encoding string, status int) {
		t.Helper()
		responses := make(map[string]*http.Response)
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			req, err := http.NewRequest(method, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept-Encoding", encoding)
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil || res.StatusCode != status || (method == http.MethodHead && len(body) != 0) {
				t.Fatalf("%s %s: status=%d bytes=%d error=%v", method, path, res.StatusCode, len(body), err)
			}
			if res.Header.Get("Content-Encoding") != encoding {
				t.Errorf("%s %s: encoding=%q, want %q", method, path, res.Header.Get("Content-Encoding"), encoding)
			}
			responses[method] = res
		}
		for _, key := range []string{"Content-Type", "Content-Encoding", "Content-Length", "ETag", "Cache-Control", "Vary", "X-Content-Type-Options"} {
			if get, head := responses[http.MethodGet].Header.Get(key), responses[http.MethodHead].Header.Get(key); get != head {
				t.Errorf("%s %s: %s GET=%q HEAD=%q", path, encoding, key, get, head)
			}
		}
	}
	for _, format := range []string{"rss.xml", "atom.xml"} {
		for _, scope := range []string{"/", "/en/", "/ru/", "/category/tech/", "/tag/go/", "/ru/category/tech-ru/", "/ru/tag/go-ru/", "/category/missing/", "/ru/tag/missing/"} {
			for _, encoding := range []string{"", "gzip", "deflate"} {
				t.Run(scope+format+"/"+encoding, func(t *testing.T) {
					status := http.StatusOK
					if strings.Contains(scope, "missing") {
						status = http.StatusNotFound
					}
					checkHeaders(scope+format, encoding, status)
				})
			}
		}
	}
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError} {
		query := `UPDATE config SET value = 'invalid' WHERE key = 'site_url'`
		if status == http.StatusInternalServerError {
			query = `UPDATE config SET value = 'https://feeds.example' WHERE key = 'site_url'; DROP TABLE pages`
		}
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"rss.xml", "atom.xml"} {
			for _, scope := range []string{"/", "/ru/"} {
				for _, encoding := range []string{"", "gzip", "deflate"} {
					checkHeaders(scope+format, encoding, status)
				}
			}
		}
	}
}
