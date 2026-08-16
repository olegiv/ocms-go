// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// newCanonicalURLPagesHandler builds a PagesHandler with a real renderer, which
// the create and update flows need to re-render a form that failed validation.
func newCanonicalURLPagesHandler(t *testing.T) (*PagesHandler, *store.Queries, store.User) {
	t.Helper()

	db, sm := testHandlerSetup(t)
	renderer, err := render.New(render.Config{
		TemplatesFS:    os.DirFS("../../web/templates"),
		SessionManager: sm,
		DB:             db,
		IsDev:          true,
	})
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}
	handler := NewPagesHandler(db, renderer, sm)
	user := createTestAdminUser(t, db)
	return handler, store.New(db), user
}

// postPageForm drives the admin create handler the way the router does, with a
// session and an authenticated user already in context.
func postPageForm(t *testing.T, h *PagesHandler, user store.User, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/admin/pages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithSession(h.sessionManager, req)
	req = addUserToContext(req, &user)
	w := httptest.NewRecorder()
	h.Create(w, req)
	return w
}

// TestCreatePageRejectsUnusableCanonicalURL drives the admin form, which is the
// write path that had no canonical URL validation at all: the value was trimmed
// and handed straight to CreatePageParams, while the v2 API rejected the same
// input. A stored canonical URL is emitted into a canonical link and into
// og:url, and a theme can bypass contextual escaping via the safeURL template
// function, so the form has to refuse these values rather than store them.
//
// Bug state: drop the validateCanonicalURL call from Create and every subtest
// reports a redirect plus a row that should not exist.
func TestCreatePageRejectsUnusableCanonicalURL(t *testing.T) {
	rejected := map[string]string{
		"javascript scheme": "javascript:alert(1)",
		"data scheme":       "data:text/html;base64,PHNjcmlwdD4=",
		"relative path":     "/about",
		"scheme relative":   "//cdn.example.com/a",
		"credentials":       "https://user:pass@example.com/a",
	}

	for name, raw := range rejected {
		t.Run(name, func(t *testing.T) {
			h, queries, user := newCanonicalURLPagesHandler(t)
			slug := "canonical-" + strings.ReplaceAll(name, " ", "-")

			w := postPageForm(t, h, user, url.Values{
				"title":         {"Canonical"},
				"slug":          {slug},
				"body":          {"<p>Body</p>"},
				"status":        {"draft"},
				"canonical_url": {raw},
			})

			// A rejected submission re-renders the form in place; only a
			// successful write redirects to the page list.
			if w.Code == http.StatusSeeOther || w.Code == http.StatusFound {
				t.Fatalf("canonical_url=%q produced a redirect (%d), want the form re-rendered with an error", raw, w.Code)
			}
			if _, err := queries.GetPageBySlug(t.Context(), slug); err == nil {
				t.Fatalf("canonical_url=%q was stored, want the write refused", raw)
			}
		})
	}
}

// TestCreatePageAcceptsAbsoluteCanonicalURL is the other half of the check
// above: the rule must not block the values the field exists to hold. It also
// pins that an empty value stays legal, since that is how a page opts out of a
// manual canonical URL.
func TestCreatePageAcceptsAbsoluteCanonicalURL(t *testing.T) {
	accepted := map[string]string{
		"empty":                 "",
		"https":                 "https://example.com/original",
		"http":                  "http://example.com/original",
		"whitespace is trimmed": "  https://example.com/trimmed  ",
	}

	for name, raw := range accepted {
		t.Run(name, func(t *testing.T) {
			h, queries, user := newCanonicalURLPagesHandler(t)
			slug := "ok-" + strings.ReplaceAll(name, " ", "-")

			postPageForm(t, h, user, url.Values{
				"title":         {"Canonical"},
				"slug":          {slug},
				"body":          {"<p>Body</p>"},
				"status":        {"draft"},
				"canonical_url": {raw},
			})

			page, err := queries.GetPageBySlug(t.Context(), slug)
			if err != nil {
				t.Fatalf("canonical_url=%q was refused, want it stored: %v", raw, err)
			}
			if want := strings.TrimSpace(raw); page.CanonicalUrl != want {
				t.Errorf("stored canonical_url = %q, want %q", page.CanonicalUrl, want)
			}
		})
	}
}
