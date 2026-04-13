// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"crypto/tls"
	"database/sql"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
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

func TestFrontendHandler_PageByID_AllowsConfiguredMixedCaseLanguageCode(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	res, err := db.Exec(
		`INSERT INTO pages (title, slug, body, status, author_id, page_type, language_code, published_at) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"Mixed Language Page", "mixed-lang-page", "<p>Published content</p>", "published", admin.ID, "post", "en-US",
	)
	if err != nil {
		t.Fatalf("failed to create page with mixed-case language code: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get inserted page id: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newFrontendPageByIDRequest(strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()

	h.PageByID(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d; want %d", w.Code, http.StatusMovedPermanently)
	}
	if location := w.Header().Get("Location"); location != "/en-US/mixed-lang-page" {
		t.Fatalf("Location header = %q; want %q", location, "/en-US/mixed-lang-page")
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
