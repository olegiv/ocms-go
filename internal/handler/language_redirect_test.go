// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/render"
)

// TestSetLanguagePreferenceRedirect checks that the language switch returns to
// a same-origin Referer and falls back for one that would leave the site.
// A same-host Referer whose path starts with "//" used to become a
// scheme-relative Location (security audit 2026-10-17, L-1).
func TestSetLanguagePreferenceRedirect(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, sm := testHandlerSetup(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}

	const fallback = "/admin"
	for _, tt := range []struct {
		name    string
		referer string
		want    string
	}{
		{"same-origin page", "http://example.com/admin/pages?page=2", "/admin/pages?page=2"},
		{"no referer", "", fallback},
		{"cross-origin", "https://evil.example/admin", fallback},
		{"same-origin double-slash path", "http://example.com//evil.example/x", fallback},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/language", strings.NewReader("lang=ru"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}
			req = requestWithSession(sm, req)
			rr := httptest.NewRecorder()

			setLanguagePreference(rr, req, renderer, fallback)

			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.want {
				t.Errorf("redirect = %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), tt.want)
			}
			if got := renderer.GetAdminLang(req); got != "ru" {
				t.Errorf("saved language = %q, want ru", got)
			}
		})
	}
}
