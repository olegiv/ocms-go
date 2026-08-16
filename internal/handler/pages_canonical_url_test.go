// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// newCanonicalURLPagesHandler builds a PagesHandler with a real renderer, which
// the create and update flows need to re-render a form that failed validation.
func newCanonicalURLPagesHandler(t *testing.T) (*PagesHandler, *store.Queries, store.User) {
	t.Helper()

	// Without a catalog i18n.T echoes the key back, so the assertions below
	// would pass on a message no operator ever sees. Loading it means the tests
	// check the rendered English text.
	if err := i18n.Init(nil); err != nil {
		t.Fatalf("init i18n: %v", err)
	}

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
			// successful write redirects to the page list. Asserting 200 and
			// the rendered field error keeps an unrelated 500 from satisfying
			// this test.
			if w.Code != http.StatusOK {
				t.Fatalf("canonical_url=%q produced status %d, want 200 with the form re-rendered", raw, w.Code)
			}
			if body := w.Body.String(); !strings.Contains(body, "Invalid canonical URL") {
				t.Errorf("canonical_url=%q did not render a canonical URL field error", raw)
			}
			if _, err := queries.GetPageBySlug(t.Context(), slug); err == nil {
				t.Fatalf("canonical_url=%q was stored, want the write refused", raw)
			}
		})
	}
}

// TestUpdatePageRejectsUnusableCanonicalURL covers the admin edit form. Create
// and Update are separate 300-line handlers with their own copy of the
// validation block, so a test that only drives Create leaves half the form
// unguarded — and an editor changing an existing page is the more common
// operation of the two.
//
// Bug state: drop the validateCanonicalURL call from Update and every subtest
// reports the stored value that should have been refused.
func TestUpdatePageRejectsUnusableCanonicalURL(t *testing.T) {
	rejected := map[string]string{
		"javascript scheme": "javascript:alert(1)",
		"relative path":     "/about",
		"credentials":       "https://user:pass@example.com/a",
	}

	for name, raw := range rejected {
		t.Run(name, func(t *testing.T) {
			h, queries, user := newCanonicalURLPagesHandler(t)
			slug := "update-" + strings.ReplaceAll(name, " ", "-")

			// Seed a page through the create handler so the row matches what
			// the edit form would load.
			postPageForm(t, h, user, url.Values{
				"title":         {"Editable"},
				"slug":          {slug},
				"body":          {"<p>Body</p>"},
				"status":        {"draft"},
				"canonical_url": {"https://example.com/original"},
			})
			page, err := queries.GetPageBySlug(t.Context(), slug)
			if err != nil {
				t.Fatalf("seed page: %v", err)
			}

			form := url.Values{
				"title":         {"Editable"},
				"slug":          {slug},
				"body":          {"<p>Body</p>"},
				"status":        {"draft"},
				"canonical_url": {raw},
			}
			req := httptest.NewRequest(http.MethodPost,
				"/admin/pages/"+strconv.FormatInt(page.ID, 10), strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = requestWithURLParams(req, map[string]string{"id": strconv.FormatInt(page.ID, 10)})
			req = requestWithSession(h.sessionManager, req)
			req = addUserToContext(req, &user)
			w := httptest.NewRecorder()
			h.Update(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("canonical_url=%q produced status %d, want 200 with the form re-rendered", raw, w.Code)
			}
			if body := w.Body.String(); !strings.Contains(body, "Invalid canonical URL") {
				t.Errorf("canonical_url=%q did not render a canonical URL field error", raw)
			}
			stored, err := queries.GetPageBySlug(t.Context(), slug)
			if err != nil {
				t.Fatalf("reload page: %v", err)
			}
			if stored.CanonicalUrl != "https://example.com/original" {
				t.Errorf("stored canonical_url = %q, want the original value left untouched", stored.CanonicalUrl)
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
