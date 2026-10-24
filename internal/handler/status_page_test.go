// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

const testStatusPageURL = "https://stats.uptimerobot.com/g50rYsPMxR"

// setTestStatusPageURL stores the status page URL the way a config save does.
func setTestStatusPageURL(t *testing.T, queries *store.Queries, value string) {
	t.Helper()
	if _, err := queries.UpsertConfig(context.Background(), store.UpsertConfigParams{
		Key:   model.ConfigKeyStatusPageURL,
		Value: value,
		Type:  model.ConfigTypeString,
	}); err != nil {
		t.Fatalf("UpsertConfig: %v", err)
	}
}

// newStatusTestRenderer builds a renderer without HTML admin templates; the
// templ views these tests render need none.
func newStatusTestRenderer(t *testing.T, db *sql.DB, sm *scs.SessionManager) *render.Renderer {
	t.Helper()
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	return renderer
}

func TestValidateStatusPageURL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty hides the link", "", false},
		{"https status page", testStatusPageURL, false},
		{"http status page", "http://status.example.com/", false},
		{"javascript scheme", "javascript:alert(1)", true},
		{"ftp scheme", "ftp://status.example.com/", true},
		{"relative path", "/status", true},
		{"scheme without host", "https://", true},
		{"no scheme", "stats.uptimerobot.com/g50rYsPMxR", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateStatusPageURL(tt.value, "en")
			if (got != "") != tt.wantErr {
				t.Errorf("validateStatusPageURL(%q) = %q, wantErr %v", tt.value, got, tt.wantErr)
			}
		})
	}
}

// TestFrontendFooterStatusPageLink covers the templ-engine default theme,
// whose footer is frontendFooter rather than a partials/footer.html file.
func TestFrontendFooterStatusPageLink(t *testing.T) {
	renderFooter := func(base BaseTemplateData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := frontendFooter(base).Render(context.Background(), &buf); err != nil {
			t.Fatalf("render frontendFooter: %v", err)
		}
		return buf.String()
	}

	if body := renderFooter(BaseTemplateData{SiteName: "Site", LangCode: "en"}); strings.Contains(body, "fe-footer-status") {
		t.Error("footer rendered a status link with no URL configured")
	}

	body := renderFooter(BaseTemplateData{SiteName: "Site", LangCode: "en", StatusPageURL: testStatusPageURL})
	for _, want := range []string{`href="` + testStatusPageURL + `"`, `target="_blank"`, `rel="noopener noreferrer"`, "fe-footer-status"} {
		if !strings.Contains(body, want) {
			t.Errorf("footer missing %q", want)
		}
	}
}

// TestThemeFootersRenderStatusPageLink renders a page through every bundled
// theme with the setting filled in. A footer that reads a page field inside
// {{with .StatusPageURL}} (where dot is the URL string) fails template
// execution, which only a real render catches.
func TestThemeFootersRenderStatusPageLink(t *testing.T) {
	for _, themeName := range []string{"default", "developer", "starter"} {
		t.Run(themeName, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			setTestStatusPageURL(t, store.New(db), testStatusPageURL)

			h := NewFrontendHandler(db, loadedFrontendThemeManager(t, themeName), nil, slog.Default(), nil, nil)
			w := httptest.NewRecorder()
			languageAwareAliasTestRouter(db, h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/no-such-page", nil))

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d; want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
			}
			if body := w.Body.String(); !strings.Contains(body, `href="`+testStatusPageURL+`"`) {
				t.Errorf("%s footer has no status page link: %s", themeName, body)
			}
		})
	}
}

func TestBaseTemplateDataStatusPageURL(t *testing.T) {
	for _, withCache := range []bool{false, true} {
		name := "database"
		if withCache {
			name = "cache"
		}
		t.Run(name, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			queries := store.New(db)

			var cacheManager *cache.Manager
			if withCache {
				cacheManager = cache.NewManager(queries)
			}
			h := NewFrontendHandler(db, testThemeManager(), cacheManager, slog.Default(), nil, nil)
			req := httptest.NewRequest(http.MethodGet, "/", nil)

			// The first read also warms the cache.
			if got := h.getBaseTemplateData(req, "Home", "").StatusPageURL; got != "" {
				t.Errorf("StatusPageURL without config = %q, want empty", got)
			}

			setTestStatusPageURL(t, queries, testStatusPageURL)
			if cacheManager != nil {
				cacheManager.InvalidateConfig() // what a config save does
			}

			if got := h.getBaseTemplateData(req, "Home", "").StatusPageURL; got != testStatusPageURL {
				t.Errorf("StatusPageURL = %q, want %q", got, testStatusPageURL)
			}
		})
	}
}

// TestConfigUpdateStatusPageURL drives the config save: the status page URL
// is validated and trimmed, and a save that fails validation still clears the
// config cache for the fields it did write.
func TestConfigUpdateStatusPageURL(t *testing.T) {
	// Fully migrated: the save writes config_translations, which testDB lacks.
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	sm := testSessionManager(t)
	queries := store.New(db)
	cacheManager := cache.NewManager(queries)
	h := NewConfigHandler(db, newStatusTestRenderer(t, db, sm), sm, cacheManager)
	admin := createTestAdminUser(t, db)
	ctx := context.Background()

	save := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/admin/config", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = addUserToContext(requestWithSession(sm, req), &admin)
		w := httptest.NewRecorder()
		h.Update(w, req)
		return w
	}

	// Warm the cache, as the frontend does on its first request.
	if _, err := cacheManager.GetConfig(ctx, model.ConfigKeyDefaultOGImage); err != nil {
		t.Fatalf("warm cache: %v", err)
	}

	t.Run("invalid URL is rejected and other fields reach the cache", func(t *testing.T) {
		const ogImage = "https://example.com/og.png"
		w := save(url.Values{
			model.ConfigKeyStatusPageURL:  {"javascript:alert(1)"},
			model.ConfigKeyDefaultOGImage: {ogImage},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d; want %d (form re-rendered with errors)", w.Code, http.StatusOK)
		}
		if cfg, err := queries.GetConfigByKey(ctx, model.ConfigKeyStatusPageURL); err == nil && cfg.Value != "" {
			t.Errorf("rejected status page URL was stored: %q", cfg.Value)
		}
		if got, _ := cacheManager.GetConfig(ctx, model.ConfigKeyDefaultOGImage); got != ogImage {
			t.Errorf("cached default_og_image = %q, want %q: the failed save wrote it but left the cache stale", got, ogImage)
		}
	})

	t.Run("valid URL is trimmed and stored", func(t *testing.T) {
		w := save(url.Values{model.ConfigKeyStatusPageURL: {"  " + testStatusPageURL + "  "}})
		if w.Code != http.StatusSeeOther && w.Code != http.StatusFound {
			t.Fatalf("status = %d; want a redirect after a successful save", w.Code)
		}
		cfg, err := queries.GetConfigByKey(ctx, model.ConfigKeyStatusPageURL)
		if err != nil {
			t.Fatalf("GetConfigByKey: %v", err)
		}
		if cfg.Value != testStatusPageURL {
			t.Errorf("stored status page URL = %q, want %q", cfg.Value, testStatusPageURL)
		}
		if got, _ := cacheManager.GetConfig(ctx, model.ConfigKeyStatusPageURL); got != testStatusPageURL {
			t.Errorf("cached status page URL = %q, want %q", got, testStatusPageURL)
		}
	})
}

// TestAdminPageContextsCarryStatusPageURL checks every admin page context
// builder picks up the URL that LoadSiteConfig puts in the request context,
// so the admin header icon shows on core, module and account pages alike.
func TestAdminPageContextsCarryStatusPageURL(t *testing.T) {
	db, sm := testHandlerSetup(t)
	setTestStatusPageURL(t, store.New(db), testStatusPageURL)
	renderer := newStatusTestRenderer(t, db, sm)
	auth := NewAuthHandler(db, renderer, sm, nil, nil, testMFAService(t, db))
	admin := createTestAdminUser(t, db)

	var got map[string]string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]string{
			"buildPageContext":          buildPageContext(r, sm, renderer, "Test", nil).StatusPageURL,
			"Renderer.BuildPageContext": renderer.BuildPageContext(r, "Test", nil).StatusPageURL,
			"accountPageContext":        auth.accountPageContext(r, &admin, "account.title").StatusPageURL,
		}
	})
	req := addUserToContext(requestWithSession(sm, httptest.NewRequest(http.MethodGet, "/admin/", nil)), &admin)
	middleware.LoadSiteConfig(db, nil)(next).ServeHTTP(httptest.NewRecorder(), req)

	if len(got) != 3 {
		t.Fatalf("page context builders not reached: %v", got)
	}
	for builder, value := range got {
		if value != testStatusPageURL {
			t.Errorf("%s StatusPageURL = %q, want %q", builder, value, testStatusPageURL)
		}
	}
}
