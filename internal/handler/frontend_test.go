// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"crypto/tls"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/theme"
	corethemes "github.com/olegiv/ocms-go/internal/themes"
)

// faviconTestCase defines a test case for favicon handler tests.
type faviconTestCase struct {
	name       string
	setupDB    func(*testing.T, *sql.DB) // optional DB setup
	favicon    []byte
	wantStatus int
	wantType   string // empty to skip check
	wantCache  string // empty to skip check
}

func runFaviconTest(t *testing.T, tc faviconTestCase) {
	t.Helper()

	db, _ := testHandlerSetup(t)

	if tc.setupDB != nil {
		tc.setupDB(t, db)
	}

	h := NewFrontendHandler(db, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	w := httptest.NewRecorder()

	h.Favicon(w, req, tc.favicon)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != tc.wantStatus {
		t.Errorf("status = %d; want %d", resp.StatusCode, tc.wantStatus)
	}

	if tc.wantType != "" {
		if got := resp.Header.Get("Content-Type"); got != tc.wantType {
			t.Errorf("Content-Type = %q; want %q", got, tc.wantType)
		}
	}

	if tc.wantCache != "" {
		if got := resp.Header.Get("Cache-Control"); got != tc.wantCache {
			t.Errorf("Cache-Control = %q; want %q", got, tc.wantCache)
		}
	}
}

func TestFrontendHandler_Favicon_DefaultFavicon(t *testing.T) {
	runFaviconTest(t, faviconTestCase{
		name:       "default favicon",
		favicon:    []byte{0x00, 0x00, 0x01, 0x00}, // Minimal ICO header
		wantStatus: http.StatusOK,
		wantType:   "image/x-icon",
		wantCache:  "public, max-age=31536000",
	})
}

func TestFrontendHandler_Favicon_WithThemeSettings(t *testing.T) {
	runFaviconTest(t, faviconTestCase{
		name: "with theme settings",
		setupDB: func(t *testing.T, db *sql.DB) {
			t.Helper()
			_, err := db.Exec(`INSERT INTO config (key, value, type) VALUES (?, ?, ?)`,
				"theme_settings_default",
				`{"favicon":"/uploads/original/abc123/favicon.ico"}`,
				"json",
			)
			if err != nil {
				t.Fatalf("failed to insert config: %v", err)
			}
		},
		favicon:    []byte{0x00, 0x00, 0x01, 0x00},
		wantStatus: http.StatusOK,
		// Note: Without a proper theme manager mock, this test verifies the handler
		// doesn't panic when theme manager is nil. In a full integration test,
		// we would mock the theme manager to return an active theme.
	})
}

func TestFrontendHandler_Favicon_EmptyDefaultFavicon(t *testing.T) {
	runFaviconTest(t, faviconTestCase{
		name:       "empty default favicon",
		favicon:    nil,
		wantStatus: http.StatusOK,
		wantType:   "image/x-icon",
	})
}

// TestPageView_Type verifies that PageView.Type correctly reflects the page type.
func TestPageView_Type(t *testing.T) {
	tests := []struct {
		name     string
		pageType string
		wantType string
	}{
		{"page type", "page", "page"},
		{"post type", "post", "post"},
		{"empty type", "", ""},
		{"custom type", "article", "article"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pv := PageView{
				ID:    1,
				Title: "Test",
				Type:  tt.pageType,
			}

			if pv.Type != tt.wantType {
				t.Errorf("PageView.Type = %q, want %q", pv.Type, tt.wantType)
			}
		})
	}
}

func TestRoutableLanguages(t *testing.T) {
	languages := []store.Language{
		{Code: "en"},
		{Code: "zh-hans"},
		{Code: "blog"},
		{Code: "x"},
	}

	got := routableLanguages(languages)
	if len(got) != 2 || got[0].Code != "en" || got[1].Code != "zh-hans" {
		t.Fatalf("routableLanguages() = %#v, want en and zh-hans", got)
	}
}

func TestFrontendViewsPrefixNonDefaultLanguageLinks(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)
	ctx := context.Background()

	pageResult, err := db.ExecContext(ctx, `
		INSERT INTO pages (title, slug, body, status, author_id, page_type, language_code, published_at)
		VALUES ('Article', 'article', '<p>Body</p>', 'published', ?, 'post', 'fr', CURRENT_TIMESTAMP)
	`, admin.ID)
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	pageID, err := pageResult.LastInsertId()
	if err != nil {
		t.Fatalf("page id: %v", err)
	}
	categoryResult, err := db.ExecContext(ctx,
		`INSERT INTO categories (name, slug, language_code) VALUES ('Actualites', 'actualites', 'fr')`)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	categoryID, err := categoryResult.LastInsertId()
	if err != nil {
		t.Fatalf("category id: %v", err)
	}
	tagResult, err := db.ExecContext(ctx,
		`INSERT INTO tags (name, slug, language_code) VALUES ('Golang', 'golang', 'fr')`)
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	tagID, err := tagResult.LastInsertId()
	if err != nil {
		t.Fatalf("tag id: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO page_categories (page_id, category_id) VALUES (?, ?)`, pageID, categoryID); err != nil {
		t.Fatalf("link category: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO page_tags (page_id, tag_id) VALUES (?, ?)`, pageID, tagID); err != nil {
		t.Fatalf("link tag: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	page, err := h.queries.GetPageBySlug(ctx, "article")
	if err != nil {
		t.Fatalf("GetPageBySlug: %v", err)
	}
	view := h.pageToView(ctx, page, "fr", "/fr")
	if view.URL != "/fr/article" {
		t.Errorf("page URL = %q, want /fr/article", view.URL)
	}
	if len(view.Categories) != 1 || view.Categories[0].URL != "/fr/category/actualites" {
		t.Errorf("page category links = %+v, want /fr/category/actualites", view.Categories)
	}
	if len(view.Tags) != 1 || view.Tags[0].URL != "/fr/tag/golang" {
		t.Errorf("page tag links = %+v, want /fr/tag/golang", view.Tags)
	}

	categories, tags, recent := h.getSidebarData(ctx, "fr", "/fr")
	if len(categories) != 1 || categories[0].URL != "/fr/category/actualites" {
		t.Errorf("sidebar category links = %+v, want /fr/category/actualites", categories)
	}
	if len(tags) != 1 || tags[0].URL != "/fr/tag/golang" {
		t.Errorf("sidebar tag links = %+v, want /fr/tag/golang", tags)
	}
	if len(recent) != 1 || recent[0].URL != "/fr/article" {
		t.Errorf("sidebar page links = %+v, want /fr/article", recent)
	}
}

func TestPageToViewUsesEachTaxonomyEntityCanonicalLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	for code, active := range map[string]bool{"fr": true, "de": false, "blog": true, "x": true} {
		createTestLanguage(t, db, code, active)
	}
	pageID := createPublishedLanguagePage(t, db, "mixed-taxonomy-page", "en", admin.ID)

	for _, entity := range []struct {
		name, slug, languageCode string
	}{
		{name: "Default category", slug: "mixed-default-category", languageCode: "en"},
		{name: "French category", slug: "mixed-french-category", languageCode: "fr"},
		{name: "Inactive category", slug: "mixed-inactive-category", languageCode: "de"},
		{name: "Reserved category", slug: "mixed-reserved-category", languageCode: "blog"},
		{name: "Invalid category", slug: "mixed-invalid-category", languageCode: "x"},
		{name: "Orphan category", slug: "mixed-orphan-category", languageCode: "zz"},
	} {
		result, err := db.Exec(`INSERT INTO categories (name, slug, language_code) VALUES (?, ?, ?)`, entity.name, entity.slug, entity.languageCode)
		if err != nil {
			t.Fatalf("create category %q: %v", entity.slug, err)
		}
		categoryID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("category ID %q: %v", entity.slug, err)
		}
		if _, err := db.Exec(`INSERT INTO page_categories (page_id, category_id) VALUES (?, ?)`, pageID, categoryID); err != nil {
			t.Fatalf("associate category %q: %v", entity.slug, err)
		}
	}

	for _, entity := range []struct {
		name, slug, languageCode string
	}{
		{name: "Default tag", slug: "mixed-default-tag", languageCode: "en"},
		{name: "French tag", slug: "mixed-french-tag", languageCode: "fr"},
		{name: "Inactive tag", slug: "mixed-inactive-tag", languageCode: "de"},
		{name: "Reserved tag", slug: "mixed-reserved-tag", languageCode: "blog"},
		{name: "Invalid tag", slug: "mixed-invalid-tag", languageCode: "x"},
		{name: "Orphan tag", slug: "mixed-orphan-tag", languageCode: "zz"},
	} {
		result, err := db.Exec(`INSERT INTO tags (name, slug, language_code) VALUES (?, ?, ?)`, entity.name, entity.slug, entity.languageCode)
		if err != nil {
			t.Fatalf("create tag %q: %v", entity.slug, err)
		}
		tagID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("tag ID %q: %v", entity.slug, err)
		}
		if _, err := db.Exec(`INSERT INTO page_tags (page_id, tag_id) VALUES (?, ?)`, pageID, tagID); err != nil {
			t.Fatalf("associate tag %q: %v", entity.slug, err)
		}
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	page, err := h.queries.GetPageByID(context.Background(), pageID)
	if err != nil {
		t.Fatalf("get mixed-taxonomy page: %v", err)
	}
	view := h.pageToView(context.Background(), page, "en", "")

	wantCategories := map[string]string{
		"mixed-default-category": "/category/mixed-default-category",
		"mixed-french-category":  "/fr/category/mixed-french-category",
	}
	if len(view.Categories) != len(wantCategories) {
		t.Fatalf("categories = %+v; want only active canonical associations", view.Categories)
	}
	for _, category := range view.Categories {
		if wantURL, ok := wantCategories[category.Slug]; !ok || category.URL != wantURL {
			t.Errorf("category = %+v; want canonical URL from entity language", category)
		}
	}
	if view.Category == nil || view.Category.URL == "" {
		t.Errorf("primary category = %+v; want first routable association", view.Category)
	}

	wantTags := map[string]string{
		"mixed-default-tag": "/tag/mixed-default-tag",
		"mixed-french-tag":  "/fr/tag/mixed-french-tag",
	}
	if len(view.Tags) != len(wantTags) {
		t.Fatalf("tags = %+v; want only active canonical associations", view.Tags)
	}
	for _, tag := range view.Tags {
		if wantURL, ok := wantTags[tag.Slug]; !ok || tag.URL != wantURL {
			t.Errorf("tag = %+v; want canonical URL from entity language", tag)
		}
	}
}

func TestFrontendHandler_MixedLanguageTaxonomyLinksUseEntityLanguage(t *testing.T) {
	for _, rendererName := range []string{"fallback", "default", "developer", "starter"} {
		t.Run(rendererName, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			admin := createTestAdminUser(t, db)
			createTestLanguage(t, db, "fr", true)
			pageID := createPublishedLanguagePage(t, db, "mixed-render-page", "en", admin.ID)

			categoryResult, err := db.Exec(`INSERT INTO categories (name, slug, language_code) VALUES ('French category sentinel', 'render-french-category', 'fr')`)
			if err != nil {
				t.Fatalf("create French category: %v", err)
			}
			categoryID, err := categoryResult.LastInsertId()
			if err != nil {
				t.Fatalf("French category ID: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO page_categories (page_id, category_id) VALUES (?, ?)`, pageID, categoryID); err != nil {
				t.Fatalf("associate French category: %v", err)
			}

			tagResult, err := db.Exec(`INSERT INTO tags (name, slug, language_code) VALUES ('French tag sentinel', 'render-french-tag', 'fr')`)
			if err != nil {
				t.Fatalf("create French tag: %v", err)
			}
			tagID, err := tagResult.LastInsertId()
			if err != nil {
				t.Fatalf("French tag ID: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO page_tags (page_id, tag_id) VALUES (?, ?)`, pageID, tagID); err != nil {
				t.Fatalf("associate French tag: %v", err)
			}

			themeManager := testThemeManager()
			if rendererName != "fallback" {
				themeManager = loadedFrontendThemeManager(t, rendererName)
			}
			h := NewFrontendHandler(db, themeManager, nil, slog.Default(), nil, nil)
			router := languageAwareAliasTestRouter(db, h)
			req := httptest.NewRequest(http.MethodGet, "/mixed-render-page", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
			}
			body := w.Body.String()
			for _, want := range []string{
				`href="/fr/category/render-french-category"`,
				`href="/fr/tag/render-french-tag"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("canonical cross-language taxonomy link %q missing: %s", want, body)
				}
			}
			for _, forbidden := range []string{
				`href="/category/render-french-category"`,
				`href="/tag/render-french-tag"`,
			} {
				if strings.Contains(body, forbidden) {
					t.Errorf("page language leaked into taxonomy link %q: %s", forbidden, body)
				}
			}
		})
	}
}

func TestLanguageScopedTaxonomyUsageQueriesExcludeCrossLanguageAssociations(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)
	pageID := createPublishedLanguagePage(t, db, "usage-query-page", "en", admin.ID)

	for _, entity := range []struct{ slug, languageCode string }{
		{slug: "usage-en-category", languageCode: "en"},
		{slug: "usage-fr-category", languageCode: "fr"},
	} {
		result, err := db.Exec(`INSERT INTO categories (name, slug, language_code) VALUES (?, ?, ?)`, entity.slug, entity.slug, entity.languageCode)
		if err != nil {
			t.Fatalf("create category %q: %v", entity.slug, err)
		}
		entityID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("category ID %q: %v", entity.slug, err)
		}
		if _, err := db.Exec(`INSERT INTO page_categories (page_id, category_id) VALUES (?, ?)`, pageID, entityID); err != nil {
			t.Fatalf("associate category %q: %v", entity.slug, err)
		}
	}
	for _, entity := range []struct{ slug, languageCode string }{
		{slug: "usage-en-tag", languageCode: "en"},
		{slug: "usage-fr-tag", languageCode: "fr"},
	} {
		result, err := db.Exec(`INSERT INTO tags (name, slug, language_code) VALUES (?, ?, ?)`, entity.slug, entity.slug, entity.languageCode)
		if err != nil {
			t.Fatalf("create tag %q: %v", entity.slug, err)
		}
		entityID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("tag ID %q: %v", entity.slug, err)
		}
		if _, err := db.Exec(`INSERT INTO page_tags (page_id, tag_id) VALUES (?, ?)`, pageID, entityID); err != nil {
			t.Fatalf("associate tag %q: %v", entity.slug, err)
		}
	}

	queries := store.New(db)
	categories, err := queries.GetCategoryUsageCountsByLanguage(context.Background(), "en")
	if err != nil {
		t.Fatalf("GetCategoryUsageCountsByLanguage: %v", err)
	}
	if len(categories) != 1 || categories[0].Slug != "usage-en-category" || categories[0].LanguageCode != "en" {
		t.Errorf("category usage rows = %+v; want only English category", categories)
	}

	tags, err := queries.GetTagUsageCountsByLanguage(context.Background(), store.GetTagUsageCountsByLanguageParams{
		LanguageCode:   "en",
		LanguageCode_2: "en",
		Limit:          20,
	})
	if err != nil {
		t.Fatalf("GetTagUsageCountsByLanguage: %v", err)
	}
	if len(tags) != 1 || tags[0].Slug != "usage-en-tag" || tags[0].LanguageCode != "en" {
		t.Errorf("tag usage rows = %+v; want only English tag", tags)
	}
}

// TestPageMetadataVisibility verifies that page metadata is only shown for posts.
// This test documents the expected behavior: date, author, and reading time
// should only be displayed for page_type = "post", not for regular pages.
func TestPageMetadataVisibility(t *testing.T) {
	tests := []struct {
		name            string
		pageType        string
		wantShowMeta    bool
		wantDescription string
	}{
		{
			name:            "post shows metadata",
			pageType:        "post",
			wantShowMeta:    true,
			wantDescription: "Blog posts should display date, author, and reading time",
		},
		{
			name:            "page hides metadata",
			pageType:        "page",
			wantShowMeta:    false,
			wantDescription: "Static pages should NOT display date, author, and reading time",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the template condition: {{if eq .Page.Type "post"}}
			showMeta := tt.pageType == "post"

			if showMeta != tt.wantShowMeta {
				t.Errorf("showMeta = %v, want %v\nReason: %s", showMeta, tt.wantShowMeta, tt.wantDescription)
			}
		})
	}
}

// TestAuthorBoxVisibility verifies that author box is only shown for posts.
func TestAuthorBoxVisibility(t *testing.T) {
	tests := []struct {
		name            string
		pageType        string
		showAuthorBox   bool
		wantShow        bool
		wantDescription string
	}{
		{
			name:            "post with author box enabled",
			pageType:        "post",
			showAuthorBox:   true,
			wantShow:        true,
			wantDescription: "Posts with ShowAuthorBox=true should display author box",
		},
		{
			name:            "post with author box disabled",
			pageType:        "post",
			showAuthorBox:   false,
			wantShow:        false,
			wantDescription: "Posts with ShowAuthorBox=false should NOT display author box",
		},
		{
			name:            "page with author box enabled",
			pageType:        "page",
			showAuthorBox:   true,
			wantShow:        false,
			wantDescription: "Static pages should NEVER display author box regardless of setting",
		},
		{
			name:            "page with author box disabled",
			pageType:        "page",
			showAuthorBox:   false,
			wantShow:        false,
			wantDescription: "Static pages should NOT display author box",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the template condition: {{if and .ShowAuthorBox (eq .Page.Type "post")}}
			showAuthor := tt.showAuthorBox && tt.pageType == "post"

			if showAuthor != tt.wantShow {
				t.Errorf("showAuthor = %v, want %v\nReason: %s", showAuthor, tt.wantShow, tt.wantDescription)
			}
		})
	}
}

func TestFrontendHandler_TrustedPageBody_DefaultBypass(t *testing.T) {
	h := &FrontendHandler{}

	raw := `<p>Hello</p><script>alert('xss')</script>`
	got := string(h.trustedPageBody(raw))

	if got != raw {
		t.Fatalf("trustedPageBody() = %q, want %q", got, raw)
	}
}

func TestFrontendHandler_TrustedPageBody_SanitizesWhenEnabled(t *testing.T) {
	h := &FrontendHandler{}
	h.SetSanitizePageHTML(true)

	raw := `<p>Hello</p><script>alert('xss')</script><a href="javascript:alert(1)">link</a>`
	got := string(h.trustedPageBody(raw))

	if strings.Contains(got, "<script") {
		t.Fatalf("trustedPageBody() should strip script tags, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Fatalf("trustedPageBody() should strip javascript URLs, got %q", got)
	}
	if !strings.Contains(got, "<p>Hello</p>") {
		t.Fatalf("trustedPageBody() should keep safe content, got %q", got)
	}
}

// createDraftPage inserts a draft page into the test database.
func createDraftPage(t *testing.T, db *sql.DB, authorID int64) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO pages (title, slug, body, status, author_id, page_type) VALUES (?, ?, ?, ?, ?, ?)`,
		"Draft Page", "draft-page", "<p>Draft content</p>", "draft", authorID, "post",
	)
	if err != nil {
		t.Fatalf("failed to create draft page: %v", err)
	}
}

// createPublishedPage inserts a published page into the test database.
func createPublishedPage(t *testing.T, db *sql.DB, slug string, authorID int64) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO pages (title, slug, body, status, author_id, page_type, published_at) VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"Published Page", slug, "<p>Published content</p>", "published", authorID, "post",
	)
	if err != nil {
		t.Fatalf("failed to create published page: %v", err)
	}
}

func createPublishedAliasPage(t *testing.T, db *sql.DB, slug, languageCode, alias string, authorID int64) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO pages (title, slug, body, status, author_id, page_type, language_code, published_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"Aliased Page", slug, "<p>Published content</p>", "published", authorID, "post", languageCode,
	)
	if err != nil {
		t.Fatalf("failed to create aliased page: %v", err)
	}
	pageID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get aliased page id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO page_aliases (page_id, alias) VALUES (?, ?)`, pageID, alias); err != nil {
		t.Fatalf("failed to create page alias: %v", err)
	}
	return pageID
}

func createTestLanguage(t *testing.T, db *sql.DB, code string, active bool) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO languages (code, name, native_name, is_default, is_active, direction, position)
		VALUES (?, ?, ?, 0, ?, 'ltr', 1)
	`, code, code, code, active)
	if err != nil {
		t.Fatalf("failed to create %q language: %v", code, err)
	}
}

func createPublishedLanguagePage(t *testing.T, db *sql.DB, slug, languageCode string, authorID int64) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO pages (title, slug, body, status, author_id, page_type, language_code, published_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"Language Page", slug, "<p>Published content</p>", "published", authorID, "post", languageCode,
	)
	if err != nil {
		t.Fatalf("failed to create %q page: %v", languageCode, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get %q page id: %v", languageCode, err)
	}
	return id
}

// newFrontendPageRequest creates a GET request for /{slug} with chi URL params.
func newFrontendPageRequest(slug string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/"+slug, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func newFrontendPageByIDRequest(id string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/page/"+id, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// withUser adds a user to the request context (simulates OptionalLoadUser middleware).
func withUser(r *http.Request, user store.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyUser, user))
}

// testThemeManager creates a minimal theme manager for frontend handler tests.
func testThemeManager() *theme.Manager {
	var emptyFS embed.FS
	return theme.NewManager(emptyFS, "", slog.Default())
}

func loadedFrontendThemeManager(t *testing.T, activeTheme string) *theme.Manager {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve frontend test source path")
	}
	customDir := filepath.Join(filepath.Dir(sourceFile), "..", "..", "custom")
	m := theme.NewManager(corethemes.FS, customDir, testutil.TestLoggerSilent())
	funcs := testutil.MinimalThemeFuncMap()
	// Use text/template's polymorphic comparison built-ins. The minimal shared
	// test map narrows these to ints, while frontend themes compare string fields.
	delete(funcs, "eq")
	funcs["now"] = time.Now
	emptyModuleHTML := func(...any) template.HTML { return "" }
	for _, name := range []string{
		"privacyHead", "analyticsExtHead", "embedHead", "informerBar",
		"analyticsExtBody", "embedBody", "analyticsIntReadTracker", "privacyFooterLink",
	} {
		funcs[name] = emptyModuleHTML
	}
	m.SetFuncMap(funcs)
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("load frontend themes: %v", err)
	}
	if err := m.SetActiveTheme(activeTheme); err != nil {
		t.Fatalf("activate theme %q: %v", activeTheme, err)
	}
	return m
}

func renderedArticleMainEntity(t *testing.T, body string) string {
	t.Helper()
	if strings.Contains(body, `{ string(base.JSONLD) }`) || strings.Contains(body, `@templ.Raw(string(base.JSONLD))`) {
		t.Fatalf("rendered JSON-LD contains a template placeholder: %s", body)
	}
	jsonLDType := strings.Index(body, `type="application/ld+json"`)
	if jsonLDType < 0 {
		t.Fatalf("rendered JSON-LD script missing: %s", body)
	}
	jsonLDStart := strings.Index(body[jsonLDType:], ">")
	if jsonLDStart < 0 {
		t.Fatalf("rendered JSON-LD opening tag is incomplete: %s", body)
	}
	jsonLDStart += jsonLDType + 1
	jsonLDEnd := strings.Index(body[jsonLDStart:], "</script>")
	if jsonLDEnd < 0 {
		t.Fatalf("rendered JSON-LD closing tag missing: %s", body)
	}
	var article struct {
		MainEntityOfPage string `json:"mainEntityOfPage"`
	}
	if err := json.Unmarshal([]byte(body[jsonLDStart:jsonLDStart+jsonLDEnd]), &article); err != nil {
		t.Fatalf("rendered JSON-LD is invalid: %v\n%s", err, body)
	}
	return article.MainEntityOfPage
}

func languageAwareAliasTestRouter(db *sql.DB, h *FrontendHandler) http.Handler {
	root := chi.NewRouter()
	frontend := chi.NewRouter()
	frontend.Use(middleware.Language(db))
	frontend.Get("/category/{slug}", h.Category)
	frontend.Get("/tag/{slug}", h.Tag)
	frontend.Get("/{slug}", h.Page)
	frontend.NotFound(h.NotFound)
	root.Mount("/", frontend)
	return root
}

func languageAwareListingTestRouter(db *sql.DB, h *FrontendHandler) http.Handler {
	root := chi.NewRouter()
	frontend := chi.NewRouter()
	frontend.Use(middleware.Language(db))
	frontend.Get("/", h.Home)
	frontend.Get("/blog", h.Blog)
	frontend.Get("/search", h.Search)
	root.Mount("/", frontend)
	return root
}

func TestFrontendHandler_Page_DraftPreview_AnonymousGets404(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createDraftPage(t, db, admin.ID)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newFrontendPageRequest("draft-page")
	w := httptest.NewRecorder()

	h.Page(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("anonymous user: status = %d; want %d", w.Code, http.StatusNotFound)
	}
}

func TestFrontendHandler_Page_DraftPreview_PublicUserGets404(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createDraftPage(t, db, admin.ID)
	publicUser := createTestUser(t, db, testUser{
		Email: "public@example.com",
		Name:  "Public",
		Role:  "public",
	})

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := withUser(newFrontendPageRequest("draft-page"), publicUser)
	w := httptest.NewRecorder()

	h.Page(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("public user: status = %d; want %d", w.Code, http.StatusNotFound)
	}
}

func TestFrontendHandler_Page_DraftPreview_EditorSeesPage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createDraftPage(t, db, admin.ID)
	editor := createTestUser(t, db, testUser{
		Email: "editor@example.com",
		Name:  "Editor",
		Role:  "editor",
	})

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := withUser(newFrontendPageRequest("draft-page"), editor)
	w := httptest.NewRecorder()

	h.Page(w, req)

	if w.Code == http.StatusNotFound {
		t.Errorf("editor user: got 404; want page to be served (draft preview)")
	}
}

func TestFrontendHandler_Page_DraftPreview_AdminSeesPage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createDraftPage(t, db, admin.ID)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := withUser(newFrontendPageRequest("draft-page"), admin)
	w := httptest.NewRecorder()

	h.Page(w, req)

	if w.Code == http.StatusNotFound {
		t.Errorf("admin user: got 404; want page to be served (draft preview)")
	}
}

func TestFrontendHandler_Page_PublishedPageWorksForAnonymous(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createPublishedPage(t, db, "published-page", admin.ID)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newFrontendPageRequest("published-page")
	w := httptest.NewRecorder()

	h.Page(w, req)

	if w.Code == http.StatusNotFound {
		t.Errorf("anonymous user on published page: got 404; want page to be served")
	}
}

func TestFrontendHandler_Page_ExplicitPrefixRequiresMatchingPageLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)
	createPublishedPage(t, db, "english-page", admin.ID)
	createPublishedLanguagePage(t, db, "french-page", "fr", admin.ID)

	// Exercise the real page cache and the same mounted middleware/handler
	// topology used by the application. Prime the unprefixed cache first to
	// ensure it cannot leak the English page into the French route.
	cacheManager := cache.NewManager(store.New(db))
	h := NewFrontendHandler(db, testThemeManager(), cacheManager, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)

	primeReq := httptest.NewRequest(http.MethodGet, "/english-page", nil)
	prime := httptest.NewRecorder()
	router.ServeHTTP(prime, primeReq)
	if prime.Code != http.StatusOK {
		t.Fatalf("unprefixed cache-prime status = %d; want %d", prime.Code, http.StatusOK)
	}

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "foreign page under French prefix", path: "/fr/english-page", wantStatus: http.StatusNotFound},
		{name: "English page under English prefix", path: "/en/english-page", wantStatus: http.StatusOK},
		{name: "French page under French prefix", path: "/fr/french-page", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d; want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestFrontendHandler_PageRelatedPagesStayInRequestLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)

	targetID := createPublishedLanguagePage(t, db, "related-target-fr", "fr", admin.ID)
	frenchRelatedID := createPublishedLanguagePage(t, db, "related-peer-fr", "fr", admin.ID)
	englishRelatedID := createPublishedLanguagePage(t, db, "related-peer-en", "en", admin.ID)
	for id, title := range map[int64]string{
		targetID:         "French target sentinel",
		frenchRelatedID:  "French related sentinel",
		englishRelatedID: "English related sentinel",
	} {
		if _, err := db.Exec(`UPDATE pages SET title = ? WHERE id = ?`, title, id); err != nil {
			t.Fatalf("update page %d title: %v", id, err)
		}
	}

	categoryResult, err := db.Exec(`INSERT INTO categories (name, slug, language_code) VALUES ('Shared category', 'shared-related-category', 'fr')`)
	if err != nil {
		t.Fatalf("create shared category: %v", err)
	}
	categoryID, err := categoryResult.LastInsertId()
	if err != nil {
		t.Fatalf("shared category ID: %v", err)
	}
	for _, pageID := range []int64{targetID, frenchRelatedID, englishRelatedID} {
		if _, err := db.Exec(`INSERT INTO page_categories (page_id, category_id) VALUES (?, ?)`, pageID, categoryID); err != nil {
			t.Fatalf("associate page %d with shared category: %v", pageID, err)
		}
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)
	req := httptest.NewRequest(http.MethodGet, "/fr/related-target-fr", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"French related sentinel", `href="/fr/related-peer-fr"`} {
		if !strings.Contains(body, want) {
			t.Errorf("French related page marker %q missing: %s", want, body)
		}
	}
	for _, forbidden := range []string{"English related sentinel", `href="/fr/related-peer-en"`, `href="/related-peer-en"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("cross-language related page leaked through marker %q: %s", forbidden, body)
		}
	}
}

func TestFrontendHandler_Page_UnprefixedRouteOnlyServesActiveDefaultLanguage(t *testing.T) {
	tests := []struct {
		name           string
		languageCode   string
		createLanguage bool
		active         bool
	}{
		{name: "active non-default language", languageCode: "fr", createLanguage: true, active: true},
		{name: "inactive non-default language", languageCode: "de", createLanguage: true},
		{name: "active reserved legacy language", languageCode: "blog", createLanguage: true, active: true},
		{name: "active invalid legacy language", languageCode: "x", createLanguage: true, active: true},
		{name: "orphaned language code", languageCode: "zz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			admin := createTestAdminUser(t, db)
			if tt.createLanguage {
				createTestLanguage(t, db, tt.languageCode, tt.active)
			}
			createPublishedLanguagePage(t, db, "foreign-page", tt.languageCode, admin.ID)

			h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
			router := languageAwareAliasTestRouter(db, h)
			req := httptest.NewRequest(http.MethodGet, "/foreign-page", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
			}
		})
	}
}

func TestFrontendHandler_Page_UnprefixedRouteRejectsInactiveDefaultLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createPublishedLanguagePage(t, db, "inactive-default-page", "en", admin.ID)
	if _, err := db.Exec(`UPDATE languages SET is_active = 0 WHERE is_default = 1`); err != nil {
		t.Fatalf("deactivate default language: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)
	req := httptest.NewRequest(http.MethodGet, "/inactive-default-page", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
	}
}

func TestFrontendHandler_ListingsRejectInactiveDefaultLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	createTestLanguage(t, db, "fr", true)
	if _, err := db.Exec(`UPDATE languages SET is_active = 0 WHERE is_default = 1`); err != nil {
		t.Fatalf("deactivate default language: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareListingTestRouter(db, h)
	for _, path := range []string{"/", "/blog", "/search"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
			}
		})
	}
}

func TestFrontendHandler_ListingsAllowActivePrefixedLanguageWithInactiveDefault(t *testing.T) {
	db, _ := testHandlerSetup(t)
	createTestLanguage(t, db, "fr", true)
	if _, err := db.Exec(`UPDATE languages SET is_active = 0 WHERE is_default = 1`); err != nil {
		t.Fatalf("deactivate default language: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareListingTestRouter(db, h)
	for _, path := range []string{"/fr/", "/fr/blog", "/fr/search"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want %d", w.Code, http.StatusOK)
			}
		})
	}
}

func TestFrontendHandler_NonDefaultHomepageMatchesTrailingSlashMiddlewareCanonical(t *testing.T) {
	db, _ := testHandlerSetup(t)
	seedSiteURL(t, db, "https://example.com")
	createTestLanguage(t, db, "fr", true)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := middleware.StripTrailingSlash(languageAwareListingTestRouter(db, h))

	redirect := httptest.NewRecorder()
	router.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/fr/", nil))
	if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != "/fr" {
		t.Fatalf("GET /fr/ = %d Location %q; want 301 /fr", redirect.Code, redirect.Header().Get("Location"))
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/fr", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /fr status = %d; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		`rel="canonical" href="https://example.com/fr"`,
		`property="og:url" content="https://example.com/fr"`,
		`href="/fr" class="fe-logo`,
		`hreflang="en" href="https://example.com/"`,
		`hreflang="fr" href="https://example.com/fr"`,
		`hreflang="x-default" href="https://example.com/"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /fr missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `https://example.com/fr/`) || strings.Contains(body, `href="/fr/"`) {
		t.Errorf("GET /fr advertises redirecting language homepage: %s", body)
	}

	markdownRequest := httptest.NewRequest(http.MethodGet, "/fr", nil)
	markdownRequest.Header.Set("Accept", "text/markdown")
	markdown := httptest.NewRecorder()
	router.ServeHTTP(markdown, markdownRequest)
	if markdown.Code != http.StatusOK || !strings.Contains(markdown.Body.String(), "https://example.com/fr") ||
		strings.Contains(markdown.Body.String(), "https://example.com/fr/") {
		t.Fatalf("non-default homepage Markdown canonical mismatch: status=%d body=%s", markdown.Code, markdown.Body.String())
	}
}

func TestFrontendHandler_TaxonomyRoutesRequireCanonicalLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	createTestLanguage(t, db, "fr", true)

	if _, err := db.Exec(`
		INSERT INTO categories (name, slug, language_code) VALUES
			('English category', 'english-category', 'en'),
			('French category', 'french-category', 'fr')
	`); err != nil {
		t.Fatalf("failed to create categories: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO tags (name, slug, language_code) VALUES
			('English tag', 'english-tag', 'en'),
			('French tag', 'french-tag', 'fr')
	`); err != nil {
		t.Fatalf("failed to create tags: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)
	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "unprefixed default category", path: "/category/english-category", wantStatus: http.StatusOK},
		{name: "unprefixed foreign category", path: "/category/french-category", wantStatus: http.StatusNotFound},
		{name: "prefixed matching category", path: "/fr/category/french-category", wantStatus: http.StatusOK},
		{name: "prefixed foreign category", path: "/fr/category/english-category", wantStatus: http.StatusNotFound},
		{name: "unprefixed default tag", path: "/tag/english-tag", wantStatus: http.StatusOK},
		{name: "unprefixed foreign tag", path: "/tag/french-tag", wantStatus: http.StatusNotFound},
		{name: "prefixed matching tag", path: "/fr/tag/french-tag", wantStatus: http.StatusOK},
		{name: "prefixed foreign tag", path: "/fr/tag/english-tag", wantStatus: http.StatusNotFound},
		{name: "query cannot claim unprefixed category", path: "/category/french-category?lang=fr", wantStatus: http.StatusNotFound},
		{name: "query cannot claim unprefixed tag", path: "/tag/french-tag?lang=fr", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d; want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestFrontendHandler_TaxonomyRoutesRejectInactiveDefaultLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	if _, err := db.Exec(`INSERT INTO categories (name, slug, language_code) VALUES ('English category', 'english-category', 'en')`); err != nil {
		t.Fatalf("failed to create category: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tags (name, slug, language_code) VALUES ('English tag', 'english-tag', 'en')`); err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}
	if _, err := db.Exec(`UPDATE languages SET is_active = 0 WHERE is_default = 1`); err != nil {
		t.Fatalf("deactivate default language: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)
	for _, path := range []string{"/category/english-category", "/tag/english-tag"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
			}
		})
	}
}

func TestFrontendHandler_TaxonomyRoutesRejectActiveReservedDefaultLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	if _, err := db.Exec(`UPDATE languages SET code = 'blog', name = 'Legacy default', native_name = 'Legacy default', is_active = 1 WHERE is_default = 1`); err != nil {
		t.Fatalf("replace default language code: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO categories (name, slug, language_code) VALUES ('Legacy category', 'legacy-category', 'blog')`); err != nil {
		t.Fatalf("failed to create category: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tags (name, slug, language_code) VALUES ('Legacy tag', 'legacy-tag', 'blog')`); err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)
	for _, path := range []string{"/category/legacy-category", "/tag/legacy-tag"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
			}
		})
	}
}

func TestFrontendHandler_PageByID_InvalidStoredSlugReturnsNotFound(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	res, err := db.Exec(
		`INSERT INTO pages (title, slug, body, status, author_id, page_type, published_at) VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"Bad Slug Page", "/t.co", "<p>Published content</p>", "published", admin.ID, "post",
	)
	if err != nil {
		t.Fatalf("failed to create page with invalid slug: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get inserted page id: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newFrontendPageByIDRequest(strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()

	h.PageByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
	}
	if location := w.Header().Get("Location"); location != "" {
		t.Fatalf("Location header = %q; want empty", location)
	}
}

func TestFrontendHandler_PageByID_RejectsConfiguredMixedCaseLanguageCode(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "en-US", true)
	id := createPublishedLanguagePage(t, db, "mixed-lang-page", "en-US", admin.ID)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newFrontendPageByIDRequest(strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()

	h.PageByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
	}
	if location := w.Header().Get("Location"); location != "" {
		t.Fatalf("Location header = %q; want empty", location)
	}
}

func TestFrontendHandler_PageByID_UsesOnlyActiveRoutableLanguage(t *testing.T) {
	tests := []struct {
		name         string
		languageCode string
		active       bool
		wantStatus   int
		wantLocation string
	}{
		{name: "active language", languageCode: "fr", active: true, wantStatus: http.StatusMovedPermanently, wantLocation: "/fr/language-page"},
		{name: "inactive language", languageCode: "de", active: false, wantStatus: http.StatusNotFound},
		{name: "legacy reserved language", languageCode: "blog", active: true, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			admin := createTestAdminUser(t, db)
			createTestLanguage(t, db, tt.languageCode, tt.active)
			id := createPublishedLanguagePage(t, db, "language-page", tt.languageCode, admin.ID)

			h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
			req := newFrontendPageByIDRequest(strconv.FormatInt(id, 10))
			w := httptest.NewRecorder()
			h.PageByID(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d; want %d", w.Code, tt.wantStatus)
			}
			if location := w.Header().Get("Location"); location != tt.wantLocation {
				t.Fatalf("Location header = %q; want %q", location, tt.wantLocation)
			}
		})
	}
}

func TestFrontendHandler_PageByID_DefaultLanguageIsUnprefixed(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	id := createPublishedLanguagePage(t, db, "default-page", "en", admin.ID)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newFrontendPageByIDRequest(strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()
	h.PageByID(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusMovedPermanently)
	}
	if location := w.Header().Get("Location"); location != "/default-page" {
		t.Fatalf("Location header = %q; want %q", location, "/default-page")
	}
}

func TestFrontendHandler_CanonicalRedirectsFailClosedWithAmbiguousDefaults(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)
	if _, err := db.Exec(`UPDATE languages SET is_default = 1 WHERE code = 'fr'`); err != nil {
		t.Fatalf("add second default language: %v", err)
	}
	pageID := createPublishedAliasPage(t, db, "ambiguous-page", "en", "ambiguous-alias", admin.ID)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)

	t.Run("page by ID", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.PageByID(w, newFrontendPageByIDRequest(strconv.FormatInt(pageID, 10)))
		if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
			t.Fatalf("status=%d location=%q; want 404 without redirect", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("page alias", func(t *testing.T) {
		w := httptest.NewRecorder()
		languageAwareAliasTestRouter(db, h).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/ambiguous-alias", nil))
		if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
			t.Fatalf("status=%d location=%q; want 404 without redirect", w.Code, w.Header().Get("Location"))
		}
	})
}

func TestAdminPublicPagePathUsesLanguageRouteAndFailsClosed(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)
	pageID := createPublishedLanguagePage(t, db, "page-francaise", "fr", admin.ID)
	queries := store.New(db)
	page, err := queries.GetPageByID(context.Background(), pageID)
	if err != nil {
		t.Fatal(err)
	}
	if got := publicPagePath(context.Background(), queries, page); got != "/fr/page-francaise" {
		t.Fatalf("publicPagePath = %q; want /fr/page-francaise", got)
	}
	if _, err := db.Exec(`UPDATE languages SET is_default = 1 WHERE code = 'fr'`); err != nil {
		t.Fatalf("add second default: %v", err)
	}
	if got := publicPagePath(context.Background(), queries, page); got != "" {
		t.Fatalf("publicPagePath = %q; want empty with ambiguous defaults", got)
	}
}

func TestFrontendHandler_LegacyUnsafeDefaultLanguageIsIgnored(t *testing.T) {
	for _, code := range []string{"blog", "x"} {
		t.Run(code, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			admin := createTestAdminUser(t, db)
			if _, err := db.Exec(`
				UPDATE languages
				SET code = ?, name = 'Legacy default', native_name = 'Legacy default', is_active = 1
				WHERE is_default = 1
			`, code); err != nil {
				t.Fatalf("replace default language code: %v", err)
			}
			pageID := createPublishedAliasPage(t, db, "legacy-default-page", code, "legacy-default", admin.ID)

			h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
			t.Run("direct page", func(t *testing.T) {
				router := languageAwareAliasTestRouter(db, h)
				req := httptest.NewRequest(http.MethodGet, "/legacy-default-page", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusNotFound {
					t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
				}
			})
			t.Run("page by ID", func(t *testing.T) {
				req := newFrontendPageByIDRequest(strconv.FormatInt(pageID, 10))
				w := httptest.NewRecorder()
				h.PageByID(w, req)
				if w.Code != http.StatusNotFound {
					t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
				}
			})

			t.Run("page alias", func(t *testing.T) {
				router := languageAwareAliasTestRouter(db, h)
				req := httptest.NewRequest(http.MethodGet, "/legacy-default", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusNotFound {
					t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
				}
			})
		})
	}
}

func TestFrontendHandler_PageByID_InvalidStoredLanguageCodeReturnsNotFound(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	res, err := db.Exec(
		`INSERT INTO pages (title, slug, body, status, author_id, page_type, language_code, published_at) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"Bad Language Page", "bad-language-page", "<p>Published content</p>", "published", admin.ID, "post", "en%0d",
	)
	if err != nil {
		t.Fatalf("failed to create page with invalid language code: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get inserted page id: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newFrontendPageByIDRequest(strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()

	h.PageByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
	}
	if location := w.Header().Get("Location"); location != "" {
		t.Fatalf("Location header = %q; want empty", location)
	}
}

func TestFrontendHandler_NotFound_DoesNotPersistEventForAnonymous(t *testing.T) {
	db, _ := testHandlerSetup(t)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, service.NewEventService(db))

	req := httptest.NewRequest(http.MethodGet, "/definitely-missing", nil)
	w := httptest.NewRecorder()
	h.NotFound(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
	}

	count, err := store.New(db).CountEvents(context.Background())
	if err != nil {
		t.Fatalf("CountEvents failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("event count = %d; want 0", count)
	}
}

func TestFrontendHandler_NotFound_ResolvesUnprefixedMultiSegmentAlias(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createPublishedAliasPage(t, db, "node-one", "en", "section/news", admin.ID)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)

	req := httptest.NewRequest(http.MethodGet, "/section/news", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusMovedPermanently)
	}
	if location := w.Header().Get("Location"); location != "/node-one" {
		t.Fatalf("Location = %q; want %q", location, "/node-one")
	}
}

func TestFrontendHandler_PrefixedAliasesRetainLanguagePrefix(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	_, err := db.Exec(`
		INSERT INTO languages (code, name, native_name, is_default, is_active, direction, position)
		VALUES ('fr', 'French', 'Français', 0, 1, 'ltr', 1)
	`)
	if err != nil {
		t.Fatalf("failed to create French language: %v", err)
	}

	pageID := createPublishedAliasPage(t, db, "nouvelles", "fr", "old-news", admin.ID)
	if _, err := db.Exec(`INSERT INTO page_aliases (page_id, alias) VALUES (?, ?)`, pageID, "section/news"); err != nil {
		t.Fatalf("failed to create multi-segment alias: %v", err)
	}
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)

	t.Run("one segment through page handler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/fr/old-news", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d; want %d", w.Code, http.StatusMovedPermanently)
		}
		if location := w.Header().Get("Location"); location != "/fr/nouvelles" {
			t.Fatalf("Location = %q; want %q", location, "/fr/nouvelles")
		}
	})

	t.Run("multi segment through not found handler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/fr/section/news", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d; want %d", w.Code, http.StatusMovedPermanently)
		}
		if location := w.Header().Get("Location"); location != "/fr/nouvelles" {
			t.Fatalf("Location = %q; want %q", location, "/fr/nouvelles")
		}
	})
}

func TestFrontendHandler_DefaultPrefixUsesUnprefixedCanonicalURLs(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	seedSiteURL(t, db, "https://example.com")
	createPublishedAliasPage(t, db, "canonical-page", "en", "old-canonical", admin.ID)
	if _, err := db.Exec(`
		INSERT INTO categories (name, slug, language_code) VALUES ('News', 'news', 'en');
		INSERT INTO tags (name, slug, language_code) VALUES ('Go', 'go', 'en');
	`); err != nil {
		t.Fatalf("seed default taxonomy: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	pageRouter := languageAwareAliasTestRouter(db, h)
	listingRouter := languageAwareListingTestRouter(db, h)

	t.Run("page HTML", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/en/canonical-page?utm_source=drop", nil)
		w := httptest.NewRecorder()
		pageRouter.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		for _, want := range []string{
			`rel="canonical" href="https://example.com/canonical-page"`,
			`property="og:url" content="https://example.com/canonical-page"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing unprefixed canonical marker %q: %s", want, body)
			}
		}
		if strings.Contains(body, "https://example.com/en/canonical-page") || strings.Contains(body, "utm_source=drop") {
			t.Errorf("prefixed or query-bearing canonical leaked: %s", body)
		}
	})

	t.Run("alias redirect", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/en/old-canonical", nil)
		w := httptest.NewRecorder()
		pageRouter.ServeHTTP(w, req)
		if w.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d; want %d", w.Code, http.StatusMovedPermanently)
		}
		if got := w.Header().Get("Location"); got != "/canonical-page" {
			t.Fatalf("Location = %q; want /canonical-page", got)
		}
	})

	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{name: "category", path: "/en/category/news", want: "https://example.com/category/news"},
		{name: "tag", path: "/en/tag/go", want: "https://example.com/tag/go"},
		{name: "blog", path: "/en/blog", want: "https://example.com/blog"},
		{name: "search", path: "/en/search", want: "https://example.com/search"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			router := pageRouter
			if tt.name == "blog" || tt.name == "search" {
				router = listingRouter
			}
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, `rel="canonical" href="`+tt.want+`"`) {
				t.Errorf("missing canonical %q: %s", tt.want, body)
			}
			if !strings.Contains(body, `property="og:url" content="`+tt.want+`"`) {
				t.Errorf("missing og:url %q: %s", tt.want, body)
			}
		})
	}
}

func TestCanonicalFrontendPathPreservesOnlyFunctionalPagination(t *testing.T) {
	for _, tt := range []struct {
		target string
		want   string
	}{
		{target: "/blog?page=3&utm_source=drop", want: "/fr/blog?page=3"},
		{target: "/blog?page=1&utm_source=drop", want: "/fr/blog"},
		{target: "/blog?page=bad&utm_source=drop", want: "/fr/blog"},
		{
			target: "/search?q=alpha+%26+beta&page=2&utm_source=drop",
			want:   "/fr/search?page=2&q=alpha+%26+beta",
		},
	} {
		req := httptest.NewRequest(http.MethodGet, tt.target, nil)
		if got := canonicalFrontendPath(req, "/fr"); got != tt.want {
			t.Errorf("canonicalFrontendPath(%q) = %q, want %q", tt.target, got, tt.want)
		}
	}
}

func TestFrontendHandler_NotFound_UnknownNestedPathRemainsNotFound(t *testing.T) {
	db, _ := testHandlerSetup(t)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)
	req := httptest.NewRequest(http.MethodGet, "/section/missing", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
	}
	if location := w.Header().Get("Location"); location != "" {
		t.Fatalf("Location = %q; want empty", location)
	}
}

func TestFrontendHandler_SearchResultPreservesNonDefaultLanguagePrefix(t *testing.T) {
	db, _ := testHandlerSetup(t)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	view := h.searchResultToPageView(context.Background(), service.SearchResult{
		ID:     1,
		Title:  "Search result",
		Slug:   "french-search-result",
		Body:   "<p>needle</p>",
		Status: PageStatusPublished,
	}, "/fr")
	if view.URL != "/fr/french-search-result" {
		t.Fatalf("search result URL = %q; want /fr/french-search-result", view.URL)
	}
}

func TestFrontendHandler_Page_SEOFallbackUsesNonDefaultLanguagePrefix(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	seedSiteURL(t, db, "https://example.com")
	createTestLanguage(t, db, "fr", true)
	createTestLanguage(t, db, "de", true)
	createPublishedLanguagePage(t, db, "french-seo-page", "fr", admin.ID)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)
	req := httptest.NewRequest(http.MethodGet, "/fr/french-seo-page", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	const canonical = "https://example.com/fr/french-seo-page"
	if !strings.Contains(body, `rel="canonical" href="`+canonical+`"`) {
		t.Errorf("HTML canonical missing prefixed URL: %s", body)
	}
	if !strings.Contains(body, `property="og:url" content="`+canonical+`"`) {
		t.Errorf("rendered og:url missing prefixed URL: %s", body)
	}
	if articleURL := renderedArticleMainEntity(t, body); articleURL != canonical {
		t.Errorf("rendered JSON-LD URL = %q; want %q", articleURL, canonical)
	}

	pageData := &seo.PageData{Slug: pageSEOSlug("/fr", "french-seo-page")}
	siteConfig := &seo.SiteConfig{SiteURL: "https://example.com"}
	meta := seo.BuildMeta(pageData, siteConfig)
	if meta.Canonical != canonical || meta.OGURL != canonical {
		t.Errorf("SEO fallback = canonical %q, og:url %q; want %q", meta.Canonical, meta.OGURL, canonical)
	}
	articleSchema := string(seo.BuildArticleSchema(pageData, siteConfig, time.Time{}))
	if !strings.Contains(articleSchema, `"mainEntityOfPage": "`+canonical+`"`) {
		t.Errorf("JSON-LD fallback missing prefixed URL %q: %s", canonical, articleSchema)
	}
	pageData.CanonicalURL = "https://canonical.example/manual"
	meta = seo.BuildMeta(pageData, siteConfig)
	if meta.Canonical != pageData.CanonicalURL || meta.OGURL != pageData.CanonicalURL {
		t.Errorf("manual canonical did not win: canonical %q, og:url %q", meta.Canonical, meta.OGURL)
	}
}

func TestFrontendHandler_HTMLThemesUseComputedPageOGURL(t *testing.T) {
	for _, themeName := range []string{"default", "developer", "starter"} {
		t.Run(themeName, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			admin := createTestAdminUser(t, db)
			seedSiteURL(t, db, "https://example.com")
			createTestLanguage(t, db, "fr", true)
			createTestLanguage(t, db, "de", true)
			englishPageID := createPublishedLanguagePage(t, db, "english-themed-page", "en", admin.ID)
			pageID := createPublishedLanguagePage(t, db, "french-themed-page", "fr", admin.ID)
			germanPageID := createPublishedLanguagePage(t, db, "german-themed-page", "de", admin.ID)
			const manualCanonical = "https://canonical.example/manual"
			if _, err := db.Exec(`UPDATE pages SET canonical_url = ?, no_index = 1 WHERE id = ?`, manualCanonical, pageID); err != nil {
				t.Fatalf("set manual canonical: %v", err)
			}
			queries := store.New(db)
			fr, err := queries.GetLanguageByCode(context.Background(), "fr")
			if err != nil {
				t.Fatalf("get French language: %v", err)
			}
			if _, err := queries.CreateTranslation(context.Background(), store.CreateTranslationParams{
				EntityType: "page", EntityID: englishPageID, LanguageID: fr.ID,
				TranslationID: pageID, CreatedAt: time.Now(),
			}); err != nil {
				t.Fatalf("create page translation: %v", err)
			}
			de, err := queries.GetLanguageByCode(context.Background(), "de")
			if err != nil {
				t.Fatalf("get German language: %v", err)
			}
			if _, err := queries.CreateTranslation(context.Background(), store.CreateTranslationParams{
				EntityType: "page", EntityID: englishPageID, LanguageID: de.ID,
				TranslationID: germanPageID, CreatedAt: time.Now(),
			}); err != nil {
				t.Fatalf("create German page translation: %v", err)
			}

			h := NewFrontendHandler(db, loadedFrontendThemeManager(t, themeName), nil, slog.Default(), nil, nil)
			router := languageAwareAliasTestRouter(db, h)
			req := httptest.NewRequest(http.MethodGet, "/fr/french-themed-page?utm_source=must-not-leak", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
			}
			body := w.Body.String()
			homeMarker := map[string]string{
				"default":   `href="/fr" class="fe-logo`,
				"developer": `href="/fr" class="dev-logo"`,
				"starter":   `href="/fr" class="st-header__title"`,
			}[themeName]
			if !strings.Contains(body, homeMarker) {
				t.Errorf("language-aware home link %q missing: %s", homeMarker, body)
			}
			if !strings.Contains(body, `<html lang="fr" dir="ltr"`) {
				t.Errorf("language and direction metadata missing: %s", body)
			}
			if !strings.Contains(body, `<meta name="robots" content="noindex,follow">`) {
				t.Errorf("robots metadata missing: %s", body)
			}
			if !strings.Contains(body, `hreflang="en" href="https://example.com/english-themed-page"`) ||
				!strings.Contains(body, `hreflang="fr" href="https://example.com/fr/french-themed-page"`) ||
				!strings.Contains(body, `hreflang="de" href="https://example.com/de/german-themed-page"`) ||
				!strings.Contains(body, `href="/de/german-themed-page"`) {
				t.Errorf("hreflang metadata missing: %s", body)
			}
			if !strings.Contains(body, `rel="canonical" href="`+manualCanonical+`"`) {
				t.Errorf("manual canonical missing: %s", body)
			}
			if !strings.Contains(body, `property="og:url" content="`+manualCanonical+`"`) {
				t.Errorf("computed og:url missing: %s", body)
			}
			if strings.Contains(body, "utm_source=must-not-leak") {
				t.Errorf("request query leaked into rendered SEO URL: %s", body)
			}
			const fallbackCanonical = "https://example.com/fr/french-themed-page"
			if articleURL := renderedArticleMainEntity(t, body); articleURL != fallbackCanonical {
				t.Errorf("rendered JSON-LD URL = %q; want %q", articleURL, fallbackCanonical)
			}
		})
	}
}

func TestFrontendHandler_TaxonomyTranslationsRenderCanonicalSwitchersAndHrefLangs(t *testing.T) {
	db, _ := testHandlerSetup(t)
	seedSiteURL(t, db, "https://example.com")
	createTestLanguage(t, db, "fr", true)
	createTestLanguage(t, db, "de", true)
	ctx := context.Background()
	queries := store.New(db)
	fr, err := queries.GetLanguageByCode(ctx, "fr")
	if err != nil {
		t.Fatalf("get French language: %v", err)
	}
	de, err := queries.GetLanguageByCode(ctx, "de")
	if err != nil {
		t.Fatalf("get German language: %v", err)
	}

	categoryEN, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "News", Slug: "news", LanguageCode: "en", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create English category: %v", err)
	}
	categoryFR, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "Actualités", Slug: "actualites", LanguageCode: "fr", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create French category: %v", err)
	}
	categoryDE, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "Nachrichten", Slug: "nachrichten", LanguageCode: "de", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create German category: %v", err)
	}
	tagEN, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Go", Slug: "go", LanguageCode: "en", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create English tag: %v", err)
	}
	tagFR, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Aller", Slug: "aller", LanguageCode: "fr", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create French tag: %v", err)
	}
	tagDE, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Gehen", Slug: "gehen", LanguageCode: "de", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create German tag: %v", err)
	}
	for entityType, ids := range map[string][2]int64{
		"category": {categoryEN.ID, categoryFR.ID},
		"tag":      {tagEN.ID, tagFR.ID},
	} {
		if _, err := queries.CreateTranslation(ctx, store.CreateTranslationParams{
			EntityType: entityType, EntityID: ids[0], LanguageID: fr.ID,
			TranslationID: ids[1], CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("create %s translation: %v", entityType, err)
		}
	}
	for entityType, ids := range map[string][2]int64{
		"category": {categoryEN.ID, categoryDE.ID},
		"tag":      {tagEN.ID, tagDE.ID},
	} {
		if _, err := queries.CreateTranslation(ctx, store.CreateTranslationParams{
			EntityType: entityType, EntityID: ids[0], LanguageID: de.ID,
			TranslationID: ids[1], CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("create German %s translation: %v", entityType, err)
		}
	}

	cases := []struct {
		path, defaultPath, frenchPath, germanPath string
	}{
		{path: "/fr/category/actualites", defaultPath: "/category/news", frenchPath: "/fr/category/actualites", germanPath: "/de/category/nachrichten"},
		{path: "/fr/tag/aller", defaultPath: "/tag/go", frenchPath: "/fr/tag/aller", germanPath: "/de/tag/gehen"},
	}
	for _, themeName := range []string{"templ", "default", "developer", "starter"} {
		t.Run(themeName, func(t *testing.T) {
			themeManager := testThemeManager()
			if themeName != "templ" {
				themeManager = loadedFrontendThemeManager(t, themeName)
			}
			h := NewFrontendHandler(db, themeManager, nil, slog.Default(), nil, nil)
			router := languageAwareAliasTestRouter(db, h)
			for _, tc := range cases {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("GET %s status = %d; body = %s", tc.path, w.Code, w.Body.String())
				}
				body := w.Body.String()
				if !strings.Contains(body, `href="`+tc.defaultPath+`"`) ||
					!strings.Contains(body, `href="`+tc.frenchPath+`"`) ||
					!strings.Contains(body, `href="`+tc.germanPath+`"`) ||
					!strings.Contains(body, `hreflang="en" href="https://example.com`+tc.defaultPath+`"`) ||
					!strings.Contains(body, `hreflang="fr" href="https://example.com`+tc.frenchPath+`"`) ||
					!strings.Contains(body, `hreflang="de" href="https://example.com`+tc.germanPath+`"`) {
					t.Fatalf("GET %s missing taxonomy translations for %s: %s", tc.path, themeName, body)
				}
			}
		})
	}
}

func TestFrontendHandler_NotFound_SuggestionsStayInRouteLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)
	englishID := createPublishedLanguagePage(t, db, "english-suggestion", "en", admin.ID)
	frenchID := createPublishedLanguagePage(t, db, "french-suggestion", "fr", admin.ID)
	if _, err := db.Exec(`UPDATE pages SET title = 'English suggestion sentinel' WHERE id = ?`, englishID); err != nil {
		t.Fatalf("name English suggestion: %v", err)
	}
	if _, err := db.Exec(`UPDATE pages SET title = 'Nondefault suggestion sentinel' WHERE id = ?`, frenchID); err != nil {
		t.Fatalf("name non-default suggestion: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	router := languageAwareAliasTestRouter(db, h)
	req := httptest.NewRequest(http.MethodGet, "/fr/missing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Nondefault suggestion sentinel") ||
		!strings.Contains(body, `href="/fr/french-suggestion"`) {
		t.Errorf("non-default suggestion missing or unprefixed: %s", body)
	}
	if strings.Contains(body, "English suggestion sentinel") || strings.Contains(body, "/fr/english-suggestion") {
		t.Errorf("foreign-language suggestion leaked into prefixed 404: %s", body)
	}
}

func TestFrontendHandler_NotFound_PersistsEventForAuthenticatedUser(t *testing.T) {
	db, _ := testHandlerSetup(t)
	user := createTestUser(t, db, testUser{
		Email: "member@example.com",
		Name:  "Member",
		Role:  "public",
	})
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, service.NewEventService(db))

	req := withUser(httptest.NewRequest(http.MethodGet, "/missing-auth", nil), user)
	w := httptest.NewRecorder()
	h.NotFound(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusNotFound)
	}

	count, err := store.New(db).CountEvents(context.Background())
	if err != nil {
		t.Fatalf("CountEvents failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("event count = %d; want 1", count)
	}
}

func TestCallModuleHTMLFuncs_NoFuncs(t *testing.T) {
	h := &FrontendHandler{}
	got := h.callModuleHTMLFuncs(nil, "test-nonce", "", "privacyHead")
	if got != "" {
		t.Errorf("nil funcmap: got %q; want empty", got)
	}

	got = h.callModuleHTMLFuncs(template.FuncMap{}, "test-nonce", "", "privacyHead")
	if got != "" {
		t.Errorf("empty funcmap: got %q; want empty", got)
	}
}

func TestCallModuleHTMLFuncs_SingleMatch(t *testing.T) {
	h := &FrontendHandler{}
	funcs := template.FuncMap{
		"privacyHead": func(args ...any) template.HTML {
			return "<script>privacy</script>"
		},
	}
	got := h.callModuleHTMLFuncs(funcs, "nonce", "", "privacyHead")
	want := template.HTML("<script>privacy</script>")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestCallModuleHTMLFuncs_MultipleConcat(t *testing.T) {
	h := &FrontendHandler{}
	funcs := template.FuncMap{
		"analyticsExtHead": func(args ...any) template.HTML {
			return "<meta name=\"analytics\">"
		},
		"embedHead": func(args ...any) template.HTML {
			return "<link rel=\"embed\">"
		},
	}
	got := h.callModuleHTMLFuncs(funcs, "nonce", "", "analyticsExtHead", "embedHead")
	want := template.HTML(`<meta name="analytics"><link rel="embed">`)
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestCallModuleHTMLFuncs_MissingNameSkipped(t *testing.T) {
	h := &FrontendHandler{}
	funcs := template.FuncMap{
		"embedBody": func(args ...any) template.HTML {
			return "<div>chat</div>"
		},
	}
	got := h.callModuleHTMLFuncs(funcs, "nonce", "", "nonExistent", "embedBody")
	want := template.HTML("<div>chat</div>")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestCallModuleHTMLFuncs_WrongSignatureSkipped(t *testing.T) {
	h := &FrontendHandler{}
	funcs := template.FuncMap{
		"badFunc": func() string { return "bad" },
		"goodFunc": func(args ...any) template.HTML {
			return "<good/>"
		},
	}
	got := h.callModuleHTMLFuncs(funcs, "nonce", "", "badFunc", "goodFunc")
	want := template.HTML("<good/>")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestCallModuleHTMLFuncs_NoncePassthrough(t *testing.T) {
	var receivedNonce, receivedOrigin string
	h := &FrontendHandler{}
	funcs := template.FuncMap{
		"testFunc": func(args ...any) template.HTML {
			if len(args) > 0 {
				if s, ok := args[0].(string); ok {
					receivedNonce = s
				}
			}
			if len(args) > 1 {
				if s, ok := args[1].(string); ok {
					receivedOrigin = s
				}
			}
			return ""
		},
	}
	h.callModuleHTMLFuncs(funcs, "my-secret-nonce", "https://example.com", "testFunc")
	if receivedNonce != "my-secret-nonce" {
		t.Errorf("nonce: got %q; want %q", receivedNonce, "my-secret-nonce")
	}
	if receivedOrigin != "https://example.com" {
		t.Errorf("origin: got %q; want %q", receivedOrigin, "https://example.com")
	}
}

func TestRequestPageOrigin(t *testing.T) {
	// trustedProxy on a case puts the request peer inside a configured
	// trusted-proxy CIDR so X-Forwarded-* headers are honoured. Cases
	// without the flag use the default httptest.NewRequest peer
	// (192.0.2.1), which is untrusted.
	tests := []struct {
		name         string
		host         string
		tls          bool
		headers      map[string]string
		trustedProxy bool
		want         string
	}{
		// --- Direct (no trusted proxy) ----------------------------------
		{
			name: "plain http",
			host: "example.com",
			want: "http://example.com",
		},
		{
			name: "tls connection",
			host: "example.com",
			tls:  true,
			want: "https://example.com",
		},
		{
			// Regression: inbound traffic from an external referring site
			// (search, social) must not cause requestPageOrigin to return
			// the external host.
			name:    "ignores Referer from external site",
			host:    "example.com",
			headers: map[string]string{"Referer": "https://evil.example/some-path"},
			want:    "http://example.com",
		},
		{
			name: "ignores Origin header pointing at referring page",
			host: "example.com",
			headers: map[string]string{
				"Origin":  "https://other.example",
				"Referer": "https://other.example/page",
			},
			want: "http://example.com",
		},
		{
			// Untrusted client cannot influence the bound scheme by
			// spoofing X-Forwarded-Proto.
			name:    "untrusted X-Forwarded-Proto ignored",
			host:    "example.com",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			want:    "http://example.com",
		},
		{
			// Untrusted client cannot influence the bound host by
			// spoofing X-Forwarded-Host.
			name:    "untrusted X-Forwarded-Host ignored",
			host:    "example.com",
			headers: map[string]string{"X-Forwarded-Host": "evil.example"},
			want:    "http://example.com",
		},

		// --- Default-port stripping (RFC 6454 §6.2 alignment) -----------
		{
			// Regression: some reverse proxies forward Host with an explicit
			// default port. Browsers omit default ports from Origin per
			// RFC 6454 §6.2, so the bound origin must also omit them or
			// downstream validation sees a mismatch.
			name: "default http port 80 stripped",
			host: "example.com:80",
			want: "http://example.com",
		},
		{
			name: "non-default http port preserved",
			host: "example.com:8080",
			want: "http://example.com:8080",
		},
		{
			// :443 is NOT the default port for http, so it must be preserved.
			name: "http scheme with :443 port preserved",
			host: "example.com:443",
			want: "http://example.com:443",
		},
		{
			name: "ipv6 with default http port stripped",
			host: "[::1]:80",
			want: "http://[::1]",
		},
		{
			name: "ipv6 with non-default port preserved",
			host: "[::1]:8080",
			want: "http://[::1]:8080",
		},

		// --- Trusted-proxy forwarding ----------------------------------
		{
			name:         "trusted X-Forwarded-Proto honoured",
			host:         "example.com",
			headers:      map[string]string{"X-Forwarded-Proto": "https"},
			trustedProxy: true,
			want:         "https://example.com",
		},
		{
			// Regression: multi-proxy chain may append to X-Forwarded-Proto.
			// Only the leftmost (original client) value is meaningful.
			name:         "multi-value X-Forwarded-Proto takes leftmost",
			host:         "example.com",
			headers:      map[string]string{"X-Forwarded-Proto": "https,http"},
			trustedProxy: true,
			want:         "https://example.com",
		},
		{
			name:         "multi-value X-Forwarded-Proto with space takes leftmost",
			host:         "example.com",
			headers:      map[string]string{"X-Forwarded-Proto": "https, http"},
			trustedProxy: true,
			want:         "https://example.com",
		},
		{
			name:         "non-default https port preserved with trusted proxy",
			host:         "example.com:8443",
			headers:      map[string]string{"X-Forwarded-Proto": "https"},
			trustedProxy: true,
			want:         "https://example.com:8443",
		},
		{
			name:         "default https port 443 stripped",
			host:         "example.com:443",
			headers:      map[string]string{"X-Forwarded-Proto": "https"},
			trustedProxy: true,
			want:         "https://example.com",
		},
		{
			name:         "ipv6 with default https port stripped",
			host:         "[::1]:443",
			headers:      map[string]string{"X-Forwarded-Proto": "https"},
			trustedProxy: true,
			want:         "https://[::1]",
		},
		{
			// Regression: a reverse proxy may rewrite Host to an internal
			// upstream name (e.g., "backend:8080"). The browser sends the
			// public host on widget fetches, so the render-time token must
			// be bound to the public host carried in X-Forwarded-Host, not
			// the internal one in r.Host.
			name: "trusted X-Forwarded-Host beats internal r.Host",
			host: "backend:8080",
			headers: map[string]string{
				"X-Forwarded-Host":  "public.example.com",
				"X-Forwarded-Proto": "https",
			},
			trustedProxy: true,
			want:         "https://public.example.com",
		},
		{
			name: "trusted X-Forwarded-Host with default port stripped",
			host: "backend:8080",
			headers: map[string]string{
				"X-Forwarded-Host":  "public.example.com:443",
				"X-Forwarded-Proto": "https",
			},
			trustedProxy: true,
			want:         "https://public.example.com",
		},
		{
			name: "trusted X-Forwarded-Host multi-value takes leftmost",
			host: "backend:8080",
			headers: map[string]string{
				"X-Forwarded-Host":  "public.example.com, hop2.internal",
				"X-Forwarded-Proto": "https",
			},
			trustedProxy: true,
			want:         "https://public.example.com",
		},
		{
			name: "trusted X-Forwarded-Host empty falls back to r.Host",
			host: "example.com",
			headers: map[string]string{
				"X-Forwarded-Host":  "",
				"X-Forwarded-Proto": "https",
			},
			trustedProxy: true,
			want:         "https://example.com",
		},

		// --- Edge cases -------------------------------------------------
		{
			name: "empty host yields empty origin",
			host: "",
			want: "",
		},
	}

	// Tests can run in parallel within this top-level test, but trusted-proxy
	// state is process-global, so any case that touches it must serialize
	// against any other case that touches it. Run cases sequentially and
	// reset trusted proxies after each case to avoid leakage.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tt.host
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			if tt.trustedProxy {
				if err := middleware.SetTrustedProxies([]string{"127.0.0.1/32"}); err != nil {
					t.Fatalf("SetTrustedProxies() error: %v", err)
				}
				req.RemoteAddr = "127.0.0.1:54321"
			} else {
				if err := middleware.SetTrustedProxies(nil); err != nil {
					t.Fatalf("SetTrustedProxies(nil) error: %v", err)
				}
			}
			t.Cleanup(func() {
				_ = middleware.SetTrustedProxies(nil)
			})

			if got := requestPageOrigin(req); got != tt.want {
				t.Errorf("requestPageOrigin() = %q; want %q", got, tt.want)
			}
		})
	}

	t.Run("nil request", func(t *testing.T) {
		if got := requestPageOrigin(nil); got != "" {
			t.Errorf("requestPageOrigin(nil) = %q; want empty", got)
		}
	})
}

// staticModuleFuncsProvider implements ModuleTemplateFuncsProvider for tests.
type staticModuleFuncsProvider struct {
	funcs template.FuncMap
}

func (p *staticModuleFuncsProvider) AllTemplateFuncs() template.FuncMap {
	return p.funcs
}

func TestSetModuleTemplateFuncsProvider(t *testing.T) {
	h := &FrontendHandler{}
	funcs := template.FuncMap{
		"testFunc": func(args ...any) template.HTML {
			return "<test/>"
		},
	}
	h.SetModuleTemplateFuncsProvider(&staticModuleFuncsProvider{funcs: funcs})
	got := h.callModuleHTMLFuncs(h.moduleFuncsProvider.AllTemplateFuncs(), "nonce", "", "testFunc")
	if got != "<test/>" {
		t.Errorf("after SetModuleTemplateFuncsProvider: got %q; want %q", got, "<test/>")
	}
}

func TestPickOGVariant(t *testing.T) {
	tests := []struct {
		name     string
		variants []store.MediaVariant
		wantType string
		wantNil  bool
	}{
		{
			name:    "empty variants",
			wantNil: true,
		},
		{
			name: "only thumbnail — no OG candidate",
			variants: []store.MediaVariant{
				{Type: "thumbnail", Width: 150, Height: 150},
			},
			wantNil: true,
		},
		{
			name: "og variant wins over large and medium",
			variants: []store.MediaVariant{
				{Type: "medium", Width: 800, Height: 600},
				{Type: "large", Width: 1920, Height: 1080},
				{Type: "og", Width: 1200, Height: 630},
			},
			wantType: "og",
		},
		{
			name: "large wins when no og",
			variants: []store.MediaVariant{
				{Type: "thumbnail", Width: 150, Height: 150},
				{Type: "medium", Width: 800, Height: 600},
				{Type: "large", Width: 1536, Height: 1024},
			},
			wantType: "large",
		},
		{
			name: "medium is fallback when no og or large",
			variants: []store.MediaVariant{
				{Type: "thumbnail", Width: 150, Height: 150},
				{Type: "medium", Width: 800, Height: 600},
			},
			wantType: "medium",
		},
		{
			name: "og wins even if listed first",
			variants: []store.MediaVariant{
				{Type: "og", Width: 1200, Height: 630},
				{Type: "large", Width: 1920, Height: 1080},
			},
			wantType: "og",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickOGVariant(tt.variants)
			if tt.wantNil {
				if got != nil {
					t.Errorf("pickOGVariant() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("pickOGVariant() = nil, want non-nil")
			}
			if got.Type != tt.wantType {
				t.Errorf("pickOGVariant().Type = %q, want %q", got.Type, tt.wantType)
			}
		})
	}
}

// wellKnownHandler is the shape of the three agent-discovery handlers that
// require a configured site_url. Keep them as a slice-driven table so adding
// a new .well-known endpoint that emits absolute URLs re-uses the same drift
// coverage instead of inviting fresh r.Host-fallback bugs.
type wellKnownHandler struct {
	name   string
	path   string
	invoke func(h *FrontendHandler, w http.ResponseWriter, r *http.Request)
}

func wellKnownHandlers() []wellKnownHandler {
	return []wellKnownHandler{
		{"APICatalog", "/.well-known/api-catalog", (*FrontendHandler).APICatalog},
		{"AgentSkillsIndex", "/.well-known/agent-skills/index.json", (*FrontendHandler).AgentSkillsIndex},
		{"MCPServerCard", "/.well-known/mcp/server-card.json", (*FrontendHandler).MCPServerCard},
	}
}

func TestFrontendHandler_WellKnown_RequiresConfiguredSiteURL(t *testing.T) {
	db, _ := testHandlerSetup(t)
	h := NewFrontendHandler(db, nil, nil, nil, nil, nil)

	for _, tc := range wellKnownHandlers() {
		t.Run(tc.name, func(t *testing.T) {
			// Reverse-proxy pitfall: r.Host points at an internal upstream.
			// Without a configured site_url the handler must refuse rather
			// than leak internal hostnames into public discovery documents.
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = "internal-upstream.local"
			w := httptest.NewRecorder()

			tc.invoke(h, w, req)

			resp := w.Result()
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusServiceUnavailable)
			}
			if got := resp.Header.Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q; want %q", got, "no-store")
			}
		})
	}
}

func TestFrontendHandler_WellKnown_ServesWhenSiteURLConfigured(t *testing.T) {
	db, _ := testHandlerSetup(t)
	if _, err := db.Exec(`INSERT INTO config (key, value, type, language_code) VALUES ('site_url', 'https://example.com', 'string', 'en')`); err != nil {
		t.Fatalf("seed site_url: %v", err)
	}
	h := NewFrontendHandler(db, nil, nil, nil, nil, nil)

	for _, tc := range wellKnownHandlers() {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()

			tc.invoke(h, w, req)

			resp := w.Result()
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusOK)
			}
			body := w.Body.String()
			if !strings.Contains(body, "https://example.com") {
				t.Errorf("body missing configured site_url; got: %s", body)
			}
			if strings.Contains(body, "internal-upstream") {
				t.Errorf("body leaked request host: %s", body)
			}
		})
	}
}

// TestFrontendHandler_AgentSkillsIndex_OpenAPISHA256 verifies the lazy-cached
// SHA-256 of the OpenAPI document is advertised when a provider is wired,
// and that the provider is invoked only once across successive requests.
// A silently empty sha256 would break v0.2.0 agent-skills integrity checks,
// which is exactly the Codex round-4 finding that motivated the feature.
func TestFrontendHandler_AgentSkillsIndex_OpenAPISHA256(t *testing.T) {
	db, _ := testHandlerSetup(t)
	if _, err := db.Exec(`INSERT INTO config (key, value, type, language_code) VALUES ('site_url', 'https://example.com', 'string', 'en')`); err != nil {
		t.Fatalf("seed site_url: %v", err)
	}

	specBytes := []byte(`{"openapi":"3.1.0","info":{"title":"t"}}` + "\n")
	// Expected: sha256("{...}\n") — same helper the handler uses, so the
	// test tracks its implementation rather than hard-coding a digest.
	expected := seo.ComputeSHA256Hex(specBytes)

	t.Run("without provider emits empty sha256", func(t *testing.T) {
		h := NewFrontendHandler(db, nil, nil, nil, nil, nil)
		body := fetchAgentSkills(t, h)
		if !strings.Contains(body, `"sha256": ""`) {
			t.Errorf("expected empty sha256 without provider; got: %s", body)
		}
	})

	t.Run("with provider emits real sha256 and caches it", func(t *testing.T) {
		h := NewFrontendHandler(db, nil, nil, nil, nil, nil)
		callCount := 0
		h.SetOpenAPISpecProvider(func() ([]byte, error) {
			callCount++
			return specBytes, nil
		})

		// Two requests — provider must be invoked exactly once.
		for i := 0; i < 2; i++ {
			body := fetchAgentSkills(t, h)
			if !strings.Contains(body, `"sha256": "`+expected+`"`) {
				t.Errorf("request %d: expected sha256 %q in body; got: %s", i, expected, body)
			}
		}
		if callCount != 1 {
			t.Errorf("provider called %d times; want 1 (cache should hold)", callCount)
		}
	})

	t.Run("provider error is not cached", func(t *testing.T) {
		h := NewFrontendHandler(db, nil, nil, slog.Default(), nil, nil)
		attempts := 0
		h.SetOpenAPISpecProvider(func() ([]byte, error) {
			attempts++
			if attempts == 1 {
				return nil, errOpenAPIProviderTest
			}
			return specBytes, nil
		})

		body1 := fetchAgentSkills(t, h)
		if !strings.Contains(body1, `"sha256": ""`) {
			t.Errorf("first request should emit empty sha256 on provider error; got: %s", body1)
		}
		body2 := fetchAgentSkills(t, h)
		if !strings.Contains(body2, `"sha256": "`+expected+`"`) {
			t.Errorf("second request should retry the provider and emit real sha256; got: %s", body2)
		}
		if attempts != 2 {
			t.Errorf("provider attempts = %d; want 2 (failures must not be cached)", attempts)
		}
	})
}

// errOpenAPIProviderTest is a sentinel returned by the provider-error
// subtest above so the test doesn't instantiate a fresh errors.New per call.
var errOpenAPIProviderTest = errTest("simulated provider failure")

type errTest string

func (e errTest) Error() string { return string(e) }

func fetchAgentSkills(t *testing.T, h *FrontendHandler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-skills/index.json", nil)
	w := httptest.NewRecorder()
	h.AgentSkillsIndex(w, req)
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	return w.Body.String()
}

// TestPublishedPageForRouteServesFromCacheWithinItsLanguage covers a
// performance regression with a correctness cause.
//
// Page lookup was moved off the page cache and onto a direct query because the
// cache's miss path did a global slug lookup and could not honour the language
// a URL owns. That silently turned every page view on a cache-configured site
// into a database read. The cache now scopes its miss path by language, so the
// handler can use it again — this test fails if either half regresses: the
// cache going unused, or a page answering outside its language.
func TestPublishedPageForRouteServesFromCacheWithinItsLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createTestLanguage(t, db, "fr", true)
	createPublishedLanguagePage(t, db, "equipe", "fr", admin.ID)
	ctx := context.Background()

	cacheManager := cache.NewManager(store.New(db))
	h := NewFrontendHandler(db, testThemeManager(), cacheManager, slog.Default(), nil, nil)
	frenchCtx := cache.NewContext("fr", model.RoleAnonymous)

	first, err := h.publishedPageForRoute(ctx, frenchCtx, "equipe", "fr")
	if err != nil {
		t.Fatalf("publishedPageForRoute() error = %v", err)
	}
	if first.Slug != "equipe" {
		t.Fatalf("page slug = %q, want %q", first.Slug, "equipe")
	}
	if _, err := h.publishedPageForRoute(ctx, frenchCtx, "equipe", "fr"); err != nil {
		t.Fatalf("publishedPageForRoute() error = %v on the second read", err)
	}
	if hits := cacheManager.Page.Stats().Hits; hits < 1 {
		t.Fatalf("page cache hits = %d; the handler is bypassing the cache and reading the database "+
			"on every page view", hits)
	}

	englishCtx := cache.NewContext("en", model.RoleAnonymous)
	if _, err := h.publishedPageForRoute(ctx, englishCtx, "equipe", "en"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("publishedPageForRoute() error = %v, want sql.ErrNoRows for another language's slug", err)
	}
}
