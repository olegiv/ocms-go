// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

func TestHtmlToText_Empty(t *testing.T) {
	got := htmlToText("")
	if got != "" {
		t.Errorf("htmlToText empty: got %q, want %q", got, "")
	}
}

func TestHtmlToText_PlainText(t *testing.T) {
	got := htmlToText("Hello world")
	if got != "Hello world" {
		t.Errorf("htmlToText plain: got %q, want %q", got, "Hello world")
	}
}

func TestHtmlToText_Paragraphs(t *testing.T) {
	input := "<p>First paragraph.</p><p>Second paragraph.</p>"
	got := htmlToText(input)
	want := "First paragraph.\n\nSecond paragraph."
	if got != want {
		t.Errorf("htmlToText paragraphs:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestHtmlToText_Headings(t *testing.T) {
	input := "<h1>Title</h1><p>Content here.</p>"
	got := htmlToText(input)
	want := "Title\n\nContent here."
	if got != want {
		t.Errorf("htmlToText headings:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestHtmlToText_List(t *testing.T) {
	input := "<ul><li>Item one</li><li>Item two</li></ul>"
	got := htmlToText(input)
	if !containsSubstring(got, "- Item one") || !containsSubstring(got, "- Item two") {
		t.Errorf("htmlToText list: got %q, expected list items", got)
	}
}

func TestHtmlToText_LineBreak(t *testing.T) {
	input := "Line one<br>Line two"
	got := htmlToText(input)
	if !containsSubstring(got, "Line one") || !containsSubstring(got, "Line two") {
		t.Errorf("htmlToText br: got %q", got)
	}
}

func TestHtmlToText_StripScript(t *testing.T) {
	input := "<p>Visible</p><script>alert('xss')</script><p>Also visible</p>"
	got := htmlToText(input)
	if containsSubstring(got, "alert") || containsSubstring(got, "script") {
		t.Errorf("htmlToText script: got %q, should not contain script content", got)
	}
	if !containsSubstring(got, "Visible") || !containsSubstring(got, "Also visible") {
		t.Errorf("htmlToText script: got %q, should contain visible text", got)
	}
}

func TestHtmlToText_StripStyle(t *testing.T) {
	input := "<style>.red { color: red; }</style><p>Content</p>"
	got := htmlToText(input)
	if containsSubstring(got, "color") || containsSubstring(got, "style") {
		t.Errorf("htmlToText style: got %q, should not contain style content", got)
	}
	if !containsSubstring(got, "Content") {
		t.Errorf("htmlToText style: got %q, should contain Content", got)
	}
}

func TestHtmlToText_NestedElements(t *testing.T) {
	input := "<div><p>Hello <strong>bold</strong> and <em>italic</em> world.</p></div>"
	got := htmlToText(input)
	if !containsSubstring(got, "Hello bold and italic world.") {
		t.Errorf("htmlToText nested: got %q", got)
	}
}

func TestHtmlToText_CollapseWhitespace(t *testing.T) {
	input := "<p>First</p><p></p><p></p><p></p><p>After empty.</p>"
	got := htmlToText(input)
	// Multiple consecutive empty lines should collapse to at most one blank line
	if containsSubstring(got, "\n\n\n") {
		t.Errorf("htmlToText whitespace: got %q, should collapse multiple empty lines", got)
	}
	if !containsSubstring(got, "First") || !containsSubstring(got, "After empty.") {
		t.Errorf("htmlToText whitespace: got %q, should contain both text blocks", got)
	}
}

func TestCollapseWhitespace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"no collapse", "hello\nworld", "hello\nworld"},
		{"multiple empty lines", "hello\n\n\n\nworld", "hello\n\nworld"},
		{"leading trailing", "\n\nhello\n\n", "hello"},
		{"spaces only lines", "hello\n   \n   \nworld", "hello\n\nworld"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := collapseWhitespace(tc.input)
			if got != tc.want {
				t.Errorf("collapseWhitespace(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestIsBlockElement(t *testing.T) {
	blockElements := []string{"p", "div", "h1", "h2", "h3", "blockquote", "pre", "ul", "ol", "table"}
	for _, tag := range blockElements {
		t.Run(tag, func(t *testing.T) {
			// Parse using html tokenizer to get atom
			input := "<" + tag + ">test</" + tag + ">"
			got := htmlToText(input)
			// Block elements should produce newlines around content
			if got == "" && tag != "table" {
				t.Errorf("block element %q produced empty output", tag)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Helpers shared by the integration tests below
// ---------------------------------------------------------------------------

// seedTestUser inserts a minimal admin user and returns its ID.
func seedTestUser(t *testing.T, q *store.Queries) int64 {
	t.Helper()
	now := time.Now()
	u, err := q.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "admin@test.example",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Admin User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("seedTestUser: %v", err)
	}
	return u.ID
}

// seedConfig inserts a site config key/value pair.
func seedConfig(t *testing.T, q *store.Queries, key, value string) {
	t.Helper()
	_, err := q.UpsertConfig(context.Background(), store.UpsertConfigParams{
		Key:       key,
		Value:     value,
		Type:      "text",
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("seedConfig %q: %v", key, err)
	}
}

// seedPublishedPage inserts a published page and returns it.
func seedPublishedPage(t *testing.T, q *store.Queries, authorID int64, title, slug, body, pageType string) store.Page {
	t.Helper()
	now := time.Now()
	p, err := q.CreatePage(context.Background(), store.CreatePageParams{
		Title:       title,
		Slug:        slug,
		Body:        body,
		Status:      "published",
		AuthorID:    authorID,
		LanguageCode: "en",
		PageType:    pageType,
		PublishedAt: sql.NullTime{Time: now, Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("seedPublishedPage %q: %v", slug, err)
	}
	return p
}

// ---------------------------------------------------------------------------
// GenerateSiteContentMarkdown integration tests
// ---------------------------------------------------------------------------

func TestGenerateSiteContentMarkdown_EmptyDB(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	out, err := GenerateSiteContentMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still produce a heading even with no content.
	if !strings.Contains(out, "Content Library") {
		t.Errorf("expected 'Content Library' heading, got:\n%s", out)
	}
}

func TestGenerateSiteContentMarkdown_WithConfig(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	seedConfig(t, q, "site_name", "My Test Site")
	seedConfig(t, q, "site_url", "https://mytest.example")
	seedConfig(t, q, "site_description", "A helpful test site.")

	out, err := GenerateSiteContentMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "My Test Site") {
		t.Errorf("expected site name in output")
	}
	if !strings.Contains(out, "https://mytest.example") {
		t.Errorf("expected site URL in output")
	}
	if !strings.Contains(out, "A helpful test site.") {
		t.Errorf("expected site description in output")
	}
}

func TestGenerateSiteContentMarkdown_WithPages(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	authorID := seedTestUser(t, q)

	seedPublishedPage(t, q, authorID, "About Us", "about", "<p>We are great.</p>", "page")
	seedPublishedPage(t, q, authorID, "Hello World", "hello-world", "<p>First post.</p>", "post")

	out, err := GenerateSiteContentMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "## Pages") {
		t.Errorf("expected ## Pages section")
	}
	if !strings.Contains(out, "## Blog Posts") {
		t.Errorf("expected ## Blog Posts section")
	}
	if !strings.Contains(out, "About Us") {
		t.Errorf("expected page title 'About Us'")
	}
	if !strings.Contains(out, "Hello World") {
		t.Errorf("expected post title 'Hello World'")
	}
	// The HTML body should be converted to text.
	if !strings.Contains(out, "We are great.") {
		t.Errorf("expected page body text")
	}
	// Author should appear.
	if !strings.Contains(out, "Admin User") {
		t.Errorf("expected author name in output")
	}
	// URL pattern: slug appears after siteURL.
	if !strings.Contains(out, "/about") {
		t.Errorf("expected page URL with slug /about")
	}
}

func TestGenerateSiteContentMarkdown_PageURLUsesConfig(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	seedConfig(t, q, "site_url", "https://example.org/")
	authorID := seedTestUser(t, q)
	seedPublishedPage(t, q, authorID, "Contact", "contact", "", "page")

	out, err := GenerateSiteContentMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Trailing slash on site_url should be trimmed; URL should be https://example.org/contact.
	if !strings.Contains(out, "https://example.org/contact") {
		t.Errorf("expected full contact URL, got:\n%s", out)
	}
}

func TestGenerateSiteContentMarkdown_NoSectionWhenEmpty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	out, err := GenerateSiteContentMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With no pages, posts, categories, or tags the sections should be absent.
	if strings.Contains(out, "## Pages") {
		t.Errorf("unexpected ## Pages section when no pages exist")
	}
	if strings.Contains(out, "## Blog Posts") {
		t.Errorf("unexpected ## Blog Posts section when no posts exist")
	}
	if strings.Contains(out, "## Tags") {
		t.Errorf("unexpected ## Tags section when no tags exist")
	}
	if strings.Contains(out, "## Categories") {
		t.Errorf("unexpected ## Categories section when no categories exist")
	}
}

// ---------------------------------------------------------------------------
// GenerateUserGuideMarkdown integration tests
// ---------------------------------------------------------------------------

func TestGenerateUserGuideMarkdown_EmptyDB(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	out, err := GenerateUserGuideMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should always have the core sections.
	for _, section := range []string{"User Guide", "Navigating the Site", "Accessing Content", "User Accounts", "REST API"} {
		if !strings.Contains(out, section) {
			t.Errorf("expected section %q in output", section)
		}
	}
}

func TestGenerateUserGuideMarkdown_WithConfig(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	seedConfig(t, q, "site_name", "Demo Site")
	seedConfig(t, q, "site_url", "https://demo.example")
	seedConfig(t, q, "admin_email", "webmaster@demo.example")
	seedConfig(t, q, "posts_per_page", "5")
	seedConfig(t, q, "hcaptcha_site_key", "test-key")

	out, err := GenerateUserGuideMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Demo Site") {
		t.Errorf("expected site name")
	}
	if strings.Contains(out, "webmaster@demo.example") {
		t.Errorf("admin email must not appear in guide (privacy fix KB-002)")
	}
	if !strings.Contains(out, "https://demo.example/forms/contact-us") {
		t.Errorf("expected contact-us link instead of admin email")
	}
	if !strings.Contains(out, "5 items per page") {
		t.Errorf("expected posts_per_page value")
	}
	// hcaptcha key is set → captcha note should appear.
	if !strings.Contains(out, "Captcha") {
		t.Errorf("expected captcha mention when hcaptcha_site_key is set")
	}
	// Login/logout URLs.
	if !strings.Contains(out, "https://demo.example/login") {
		t.Errorf("expected login URL")
	}
	if !strings.Contains(out, "https://demo.example/logout") {
		t.Errorf("expected logout URL")
	}
}

func TestGenerateUserGuideMarkdown_DefaultPostsPerPage(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	// Do NOT seed posts_per_page — it should default to "10".

	out, err := GenerateUserGuideMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "10 items per page") {
		t.Errorf("expected default 10 items per page, got:\n%s", out)
	}
}

func TestGenerateUserGuideMarkdown_APISection(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	out, err := GenerateUserGuideMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// API section must list the four standard endpoints.
	for _, endpoint := range []string{
		"/api/v1/pages",
		"/api/v1/tags",
		"/api/v1/categories",
		"/api/v1/media",
	} {
		if !strings.Contains(out, endpoint) {
			t.Errorf("expected API endpoint %q in output", endpoint)
		}
	}
}

func TestGenerateUserGuideMarkdown_LanguageSectionSingleLang(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	// The default migrations seed one English language row; verify single-lang behaviour.
	out, err := GenerateUserGuideMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Language Support") {
		t.Errorf("expected Language Support section")
	}
}

func TestGenerateUserGuideMarkdown_ContactFallback(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	seedConfig(t, q, "admin_email", "help@site.example")
	// No active forms seeded → should fall back to a Contact section.

	out, err := GenerateUserGuideMarkdown(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(out, "help@site.example") {
		t.Errorf("admin email must not appear in contact fallback (privacy fix KB-002)")
	}
	if !strings.Contains(out, "## Contact") {
		t.Errorf("expected Contact section in fallback")
	}
	if !strings.Contains(out, "/forms/contact-us") {
		t.Errorf("expected contact-us link in fallback")
	}
}

// ---------------------------------------------------------------------------
// loadSiteInfo / configValue unit tests
// ---------------------------------------------------------------------------

func TestLoadSiteInfo_Defaults(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	_, siteName, _ := loadSiteInfo(context.Background(), q)

	if siteName != "Site" {
		t.Errorf("expected default site name 'Site', got %q", siteName)
	}
}

func TestLoadSiteInfo_Seeded(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	seedConfig(t, q, "site_name", "Seeded Name")
	seedConfig(t, q, "site_url", "https://seeded.example/")
	seedConfig(t, q, "site_description", "Seeded desc")

	siteURL, siteName, siteDesc := loadSiteInfo(context.Background(), q)

	if siteName != "Seeded Name" {
		t.Errorf("siteName = %q, want 'Seeded Name'", siteName)
	}
	// Trailing slash should be stripped.
	if siteURL != "https://seeded.example" {
		t.Errorf("siteURL = %q, want 'https://seeded.example'", siteURL)
	}
	if siteDesc != "Seeded desc" {
		t.Errorf("siteDesc = %q, want 'Seeded desc'", siteDesc)
	}
}

func TestConfigValue_Missing(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	got := configValue(context.Background(), q, "nonexistent_key")
	if got != "" {
		t.Errorf("configValue for missing key = %q, want ''", got)
	}
}

func TestConfigValue_Present(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	q := store.New(db)
	seedConfig(t, q, "test_config_key", "hello-value")

	got := configValue(context.Background(), q, "test_config_key")
	if got != "hello-value" {
		t.Errorf("configValue = %q, want 'hello-value'", got)
	}
}

// ---------------------------------------------------------------------------
// Handler tests for handleDownloadSiteContent / handleDownloadUserGuide
// ---------------------------------------------------------------------------

func TestHandleDownloadSiteContent(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/admin/embed/dify/kb/site-content.md", nil)
	w := httptest.NewRecorder()

	m.handleDownloadSiteContent(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown prefix", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "site-content.md") {
		t.Errorf("Content-Disposition = %q, want 'site-content.md'", cd)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Content Library") {
		t.Errorf("expected 'Content Library' in body, got:\n%s", body)
	}
}

func TestHandleDownloadSiteContent_WithSeededData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	q := store.New(db)
	seedConfig(t, q, "site_name", "Handler Test Site")
	authorID := seedTestUser(t, q)
	seedPublishedPage(t, q, authorID, "Welcome", "welcome", "<p>Hello there.</p>", "page")

	req := httptest.NewRequest(http.MethodGet, "/admin/embed/dify/kb/site-content.md", nil)
	w := httptest.NewRecorder()

	m.handleDownloadSiteContent(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Handler Test Site") {
		t.Errorf("expected site name in body")
	}
	if !strings.Contains(body, "Welcome") {
		t.Errorf("expected page title in body")
	}
}

func TestHandleDownloadUserGuide(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/admin/embed/dify/kb/user-guide.md", nil)
	w := httptest.NewRecorder()

	m.handleDownloadUserGuide(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown prefix", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "user-guide.md") {
		t.Errorf("Content-Disposition = %q, want 'user-guide.md'", cd)
	}
	body := w.Body.String()
	if !strings.Contains(body, "User Guide") {
		t.Errorf("expected 'User Guide' in body, got:\n%s", body)
	}
}

func TestHandleDownloadUserGuide_WithSeededData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	q := store.New(db)
	seedConfig(t, q, "site_name", "Guide Site")
	seedConfig(t, q, "site_url", "https://guide.example")
	seedConfig(t, q, "admin_email", "admin@guide.example")

	req := httptest.NewRequest(http.MethodGet, "/admin/embed/dify/kb/user-guide.md", nil)
	w := httptest.NewRecorder()

	m.handleDownloadUserGuide(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Guide Site") {
		t.Errorf("expected site name in guide body")
	}
	if strings.Contains(body, "admin@guide.example") {
		t.Errorf("admin email must not appear in guide body (privacy fix KB-002)")
	}
	if !strings.Contains(body, "https://guide.example/forms/contact-us") {
		t.Errorf("expected contact-us link in guide body")
	}
	if !strings.Contains(body, "https://guide.example/login") {
		t.Errorf("expected login URL in guide body")
	}
}
