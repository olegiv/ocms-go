// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestTaxonomyWritesRefreshSitemap checks the public response after writes
// on both transports. Each write starts with a warm cache, even on the parent.
func TestTaxonomyWritesRefreshSitemap(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	ctx, queries := t.Context(), store.New(db)
	user := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)
	const siteURL = "https://sitemap.example.test"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO config (key, value, type, language_code) VALUES ('site_url', ?, 'string', 'en')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, siteURL); err != nil {
		t.Fatal(err)
	}
	rawKey, prefix, err := model.GenerateAPIAccessKey()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := model.HashAPIKey(rawKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = queries.CreateAPIKey(ctx, store.CreateAPIKeyParams{
		Name: "Sitemap writer", KeyHash: hash, KeyPrefix: prefix, CreatedBy: user.ID, IsActive: true,
		KeyLookup:   sql.NullString{String: model.APIKeyLookup(rawKey), Valid: true},
		Permissions: model.PermissionsToJSON([]string{model.PermissionTaxonomyWrite}), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	manager := cache.NewManager(queries)
	pageID := createPublishedLanguagePage(t, db, "unrelated-page", "en", user.ID)
	if _, err = manager.GetPublishedPageByID(ctx, cache.DefaultContext(), pageID); err != nil {
		t.Fatal(err)
	}
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	admin := NewTaxonomyHandler(db, renderer, sm)
	admin.SetCacheManager(manager)
	front := NewFrontendHandler(db, nil, manager, testutil.TestLoggerSilent(), nil, nil)
	router := chi.NewRouter()
	router.Get("/sitemap.xml", front.Sitemap)
	router.Route("/admin", func(r chi.Router) {
		r.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { next.ServeHTTP(w, withUser(req, user)) })
		})
		r.Post(RouteTags, admin.CreateTag)
		r.Post(RouteTagsID, admin.UpdateTag)
		r.Delete(RouteTagsID, admin.DeleteTag)
		r.Post(RouteTagsID+RouteSuffixTranslate, admin.TranslateTag)
		r.Post(RouteCategories, admin.CreateCategory)
		r.Post(RouteCategoriesID, admin.UpdateCategory)
		r.Delete(RouteCategoriesID, admin.DeleteCategory)
		r.Post(RouteCategoriesID+RouteSuffixTranslate, admin.TranslateCategory)
	})
	router.Route("/api/v2", func(r chi.Router) {
		r.Use(middleware.APIKeyAuth(db))
		api := apiv2.Register(r, apiv2.Deps{DB: db, Queries: queries, Cache: manager})
		svc := taxonomy.NewService(db, queries, nil)
		svc.SetCacheManager(manager)
		taxonomy.Register(api.API, svc)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	for _, transport := range []struct {
		name, prefix, updateMethod, contentType string
		created, updated, deleted               int
	}{
		{"rest", "/api/v2", http.MethodPut, "application/json", http.StatusCreated, http.StatusOK, http.StatusNoContent},
		{"admin", "/admin", http.MethodPost, "application/x-www-form-urlencoded", http.StatusSeeOther, http.StatusSeeOther, http.StatusOK},
	} {
		for _, term := range []struct{ collection, archive, kind string }{
			{RouteTags, "/tag/", "tag"},
			{RouteCategories, "/category/", "category"},
		} {
			t.Run(transport.name+"/"+term.kind, func(t *testing.T) {
				request := func(method, path, body string, wantStatus int) []byte {
					t.Helper()
					req, reqErr := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
					if reqErr != nil {
						t.Fatal(reqErr)
					}
					req.Header.Set("Content-Type", transport.contentType)
					req.Header.Set("HX-Request", "true")
					req.Header.Set("Sec-Fetch-Site", "same-origin")
					if transport.prefix == "/api/v2" && method != http.MethodGet {
						req.Header.Set("Authorization", "Bearer "+rawKey)
					}
					res, reqErr := client.Do(req)
					if reqErr != nil {
						t.Fatal(reqErr)
					}
					data, readErr := io.ReadAll(res.Body)
					closeErr := res.Body.Close()
					if readErr != nil || closeErr != nil || res.StatusCode != wantStatus {
						t.Fatalf("%s %s: status=%d, read=%v, close=%v, want %d\n%s", method, path, res.StatusCode, readErr, closeErr, wantStatus, data)
					}
					return data
				}
				checkURL := func(path string, want bool) {
					t.Helper()
					var sitemap seo.Sitemap
					if parseErr := xml.Unmarshal(request(http.MethodGet, "/sitemap.xml", "", http.StatusOK), &sitemap); parseErr != nil {
						t.Fatal(parseErr)
					}
					found := slices.ContainsFunc(sitemap.URLs, func(entry seo.SitemapURL) bool { return entry.Loc == siteURL+path })
					if found != want {
						t.Errorf("sitemap URL %s present=%t, want %t", path, found, want)
					}
					if !manager.Sitemap.IsCached() {
						t.Error("expected the sitemap response to populate the cache")
					}
				}
				body := func(slug string) string {
					if transport.prefix == "/api/v2" {
						return fmt.Sprintf(`{"name":"Fixture","slug":%q}`, slug)
					}
					return url.Values{"name": {"Fixture"}, "slug": {slug}}.Encode()
				}
				collection := transport.prefix + term.collection
				slug := transport.name + "-" + term.kind
				oldURL, newURL := term.archive+slug, term.archive+slug+"-renamed"
				checkURL(oldURL, false)
				request(http.MethodPost, collection, body(slug), transport.created)
				checkURL(oldURL, true)

				var id int64
				switch term.kind {
				case "tag":
					tag, lookupErr := queries.GetTagBySlug(ctx, slug)
					if lookupErr != nil {
						t.Fatal(lookupErr)
					}
					id = tag.ID
				case "category":
					category, lookupErr := queries.GetCategoryBySlug(ctx, slug)
					if lookupErr != nil {
						t.Fatal(lookupErr)
					}
					id = category.ID
				}
				resource := fmt.Sprintf("%s/%d", collection, id)
				manager.InvalidateSitemap()
				checkURL(oldURL, true)
				request(transport.updateMethod, resource, body(slug+"-renamed"), transport.updated)
				checkURL(oldURL, false)
				checkURL(newURL, true)

				manager.InvalidateSitemap()
				checkURL(newURL, true)
				if transport.prefix == "/api/v2" {
					request(http.MethodPost, collection, body(slug+"-renamed"), http.StatusUnprocessableEntity)
					if !manager.Sitemap.IsCached() {
						t.Error("refused create invalidated the sitemap")
					}
				} else {
					// Category creation still commits if the translation link fails.
					if term.kind == "category" {
						if _, triggerErr := db.ExecContext(ctx, `CREATE TRIGGER refuse_category_translation BEFORE INSERT ON translations
							WHEN NEW.entity_type = 'category' BEGIN SELECT RAISE(ABORT, 'fixture link failure'); END`); triggerErr != nil {
							t.Fatal(triggerErr)
						}
					}
					request(http.MethodPost, resource+"/translate/fr", "", http.StatusSeeOther)
					checkURL("/fr"+newURL+"-fr", true)
				}
				manager.InvalidateSitemap()
				checkURL(newURL, true)
				request(http.MethodDelete, resource, "", transport.deleted)
				checkURL(newURL, false)
				if manager.Page.Stats().Items != 1 {
					t.Error("unrelated page was evicted from the page cache")
				}
			})
		}
	}
}
