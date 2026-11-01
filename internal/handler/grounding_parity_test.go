// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestGroundingMatchesFrontendLanguageAliasIDAndRedirectRoutes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q, ctx := store.New(db), context.Background()
	now := time.Now()
	user, err := q.CreateUser(ctx, store.CreateUserParams{Email: "route-parity@example.test", Role: "admin", Name: "Route fixture", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.UpsertConfig(ctx, store.UpsertConfigParams{Key: "site_url", Value: "https://example.test", Type: "string", LanguageCode: "en", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for slug, language := range map[string]string{"published": "en", "russian": "ru"} {
		page, err := q.CreatePage(ctx, store.CreatePageParams{Title: "Marker " + slug, Slug: slug, Body: "<p>Published body marker " + slug + "</p>", Summary: "Reference", Status: "published", AuthorID: user.ID, LanguageCode: language, PageType: "post", CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		ids[slug] = page.ID
	}
	for alias, slug := range map[string]string{"old/about": "published", "old-ru": "russian", "category/missing": "published", "tag/missing": "published", "forms/missing": "published"} {
		if _, err = q.CreatePageAlias(ctx, store.CreatePageAliasParams{PageID: ids[slug], Alias: alias, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for source, target := range map[string]string{"/a": "/b", "/b": "/c", "/c": "/published"} {
		if _, err = q.CreateRedirect(ctx, store.CreateRedirectParams{SourcePath: source, TargetUrl: target, StatusCode: 301, TargetType: "_self", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	frontend := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	forms := NewFormsHandler(db, nil, nil, nil, nil, nil, nil, frontend)
	child := chi.NewRouter()
	child.Use(middleware.Language(db))
	child.Get(RoutePageByID, frontend.PageByID)
	child.Get(RouteCategorySlug, frontend.Category)
	child.Get(RouteTagSlug, frontend.Tag)
	child.Get(RouteFormsSlug, forms.Show)
	child.Get("/{slug}", frontend.Page)
	child.NotFound(frontend.NotFound)
	root := chi.NewRouter()
	root.Use(middleware.NewRedirectsMiddleware(db).Handler)
	root.Mount("/", child)
	resolver, err := grounding.NewResolver(q, "https://example.test", grounding.Access{})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int64{
		"/./published": 0, "/a/../published": 0, "/old/./about": 0, "/%2e/published": 0,
		"/ru%2Frussian": 0, "/r%75/russian": 0, "/publ%69shed": 0, "/old%2Fabout": ids["published"], "/%6fld/about": ids["published"], "/ru/%72ussian": 0, "/page/%31": 0, "/published": ids["published"], "/en/published": ids["published"], "/ru/russian": ids["russian"], "/russian": 0, "/ru/published": 0, "/old/about": ids["published"], "/old-ru": ids["russian"], "/en/old-ru": 0, "/category/missing": ids["published"], "/tag/missing": ids["published"], "/forms/missing": ids["published"], "/a?q=keep": ids["published"], "/page/" + strconv.FormatInt(ids["russian"], 10): ids["russian"]} {
		t.Run(path, func(t *testing.T) {
			current := path
			var final *httptest.ResponseRecorder
			for range 4 {
				req := httptest.NewRequest("GET", current, nil)
				req.Header.Set("Accept", "text/markdown")
				req.Header.Set("Cookie", "ocms_lang=ru")
				req.Header.Set("Accept-Language", "ru")
				final = httptest.NewRecorder()
				root.ServeHTTP(final, req)
				if final.Code != http.StatusMovedPermanently {
					break
				}
				location, err := url.Parse(final.Header().Get("Location"))
				if err != nil {
					t.Fatal(err)
				}
				current = location.String()
			}
			got, err := resolver.Resolve(ctx, path)
			if want == 0 {
				if final.Code != 404 || err == nil {
					t.Fatalf("routing disagreement: HTTP=%d page=%d err=%v", final.Code, got.ID, err)
				}
				return
			}
			if final.Code != 200 || err != nil || got.ID != want || !strings.Contains(final.Body.String(), "Published body marker") {
				t.Fatalf("routing disagreement: HTTP=%d page=%d want=%d err=%v", final.Code, got.ID, want, err)
			}
		})
	}
}
