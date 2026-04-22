// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newMarkdownPageRequest builds a /{slug} request with Accept: text/markdown.
func newMarkdownPageRequest(slug string) *http.Request {
	r := newFrontendPageRequest(slug)
	r.Header.Set("Accept", "text/markdown")
	return r
}

// seedPageWithHTML inserts a single published page with the given HTML body.
// Helper for markdown-negotiation tests; uses the same schema as
// createPublishedPage but lets each test control the body.
func seedPageWithHTML(t *testing.T, db *sql.DB, slug, title, bodyHTML, summary string, authorID int64) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO pages (title, slug, body, summary, status, author_id, page_type, published_at)
		 VALUES (?, ?, ?, ?, 'published', ?, 'post', CURRENT_TIMESTAMP)`,
		title, slug, bodyHTML, summary, authorID,
	)
	if err != nil {
		t.Fatalf("seed page %q: %v", slug, err)
	}
}

// TestFrontendHandler_Page_Markdown_Negotiated verifies that a request with
// Accept: text/markdown on a published page returns a markdown representation.
func TestFrontendHandler_Page_Markdown_Negotiated(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	seedPageWithHTML(t, db, "hello",
		"Hello World",
		`<p>Welcome to <strong>oCMS</strong>.</p><ul><li>one</li><li>two</li></ul>`,
		"A short summary.",
		admin.ID,
	)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newMarkdownPageRequest("hello")
	w := httptest.NewRecorder()

	h.Page(w, req)
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type = %q; want text/markdown; charset=utf-8", ct)
	}
	if v := resp.Header.Get("Vary"); !strings.Contains(v, "Accept") {
		t.Errorf("Vary = %q; want to contain Accept", v)
	}
	if resp.Header.Get("X-Markdown-Tokens") == "" {
		t.Errorf("X-Markdown-Tokens header missing")
	}

	body := w.Body.String()
	for _, want := range []string{"# Hello World", "> A short summary.", "**oCMS**", "- one", "- two"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nfull body:\n%s", want, body)
		}
	}
}

// TestFrontendHandler_Page_Markdown_HTMLFallbackHasVary verifies that a
// browser-style request still receives HTML with Vary: Accept so caches
// keep the two representations keyed separately.
func TestFrontendHandler_Page_Markdown_HTMLFallbackHasVary(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	seedPageWithHTML(t, db, "about", "About", `<p>Hi</p>`, "About this site.", admin.ID)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newFrontendPageRequest("about")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	w := httptest.NewRecorder()

	h.Page(w, req)
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q; want text/html prefix", ct)
	}
	if v := resp.Header.Get("Vary"); !strings.Contains(v, "Accept") {
		t.Errorf("Vary = %q; want to contain Accept", v)
	}
}

// TestFrontendHandler_Page_Markdown_MissingSlug verifies that a markdown
// request for a non-existent slug returns 404 (not an empty markdown body).
func TestFrontendHandler_Page_Markdown_MissingSlug(t *testing.T) {
	db, _ := testHandlerSetup(t)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newMarkdownPageRequest("does-not-exist")
	w := httptest.NewRecorder()

	h.Page(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", w.Code)
	}
}

// TestFrontendHandler_Page_Markdown_DraftAnonymousGets404 enforces auth parity:
// a draft page must not be served in markdown to an anonymous caller, matching
// the HTML-path guard at frontend.go in the Page handler.
func TestFrontendHandler_Page_Markdown_DraftAnonymousGets404(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createDraftPage(t, db, admin.ID)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newMarkdownPageRequest("draft-page")
	w := httptest.NewRecorder()

	h.Page(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 (draft must not leak via markdown)", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "Draft content") {
		t.Errorf("draft body leaked in markdown response:\n%s", body)
	}
}

// TestFrontendHandler_Page_Markdown_DraftEditorSeesMarkdown verifies that an
// authenticated editor receives a draft rendered as markdown — symmetric to
// the HTML draft-preview path.
func TestFrontendHandler_Page_Markdown_DraftEditorSeesMarkdown(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	createDraftPage(t, db, admin.ID)
	editor := createTestUser(t, db, testUser{
		Email: "editor-md@example.com",
		Name:  "Editor",
		Role:  "editor",
	})

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := withUser(newMarkdownPageRequest("draft-page"), editor)
	w := httptest.NewRecorder()

	h.Page(w, req)
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("editor status = %d; want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Errorf("editor Content-Type = %q; want text/markdown", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "# Draft Page") {
		t.Errorf("editor draft markdown missing H1 title:\n%s", body)
	}
}

// TestFrontendHandler_Page_Markdown_ScriptsStripped verifies that scripts
// and javascript: URLs in page HTML are not emitted in the markdown.
func TestFrontendHandler_Page_Markdown_ScriptsStripped(t *testing.T) {
	db, _ := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	seedPageWithHTML(t, db, "evil",
		"Safe",
		`<p>Hello</p><script>alert(1)</script><iframe src="javascript:evil"></iframe>`,
		"",
		admin.ID,
	)

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	req := newMarkdownPageRequest("evil")
	w := httptest.NewRecorder()

	h.Page(w, req)

	body := w.Body.String()
	for _, forbidden := range []string{"<script", "alert(1)", "<iframe", "javascript:"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("markdown unexpectedly contains %q\nbody:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(body, "# Safe") {
		t.Errorf("body missing title\n%s", body)
	}
}
