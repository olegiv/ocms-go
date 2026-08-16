// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"log/slog"
	"testing"
)

// TestPageToViewSanitizesStoredCanonicalURL guards the copy of the canonical URL
// that reaches theme templates.
//
// Sanitizing the SEO metadata is not enough. PageView is handed to themes as
// .Page, and the theme funcmap exposes safeURL, which returns template.URL and
// so defeats the contextual escaping that would otherwise neutralize a
// scripting scheme. A theme writing {{safeURL .Page.CanonicalURL}} in an href
// would execute a legacy value that predates write-time validation.
//
// Bug state: assign p.CanonicalUrl straight to pv.CanonicalURL in pageToView and
// every subtest reports the raw value reaching theme data.
func TestPageToViewSanitizesStoredCanonicalURL(t *testing.T) {
	unusable := map[string]string{
		"javascript scheme": "javascript:alert(1)",
		"data scheme":       "data:text/html;base64,PHNjcmlwdD4=",
		"relative path":     "/about",
		"scheme relative":   "//cdn.example.com/a",
		"credentials":       "https://user:pass@example.com/a",
	}

	for name, raw := range unusable {
		t.Run(name, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			ctx := context.Background()
			admin := createTestAdminUser(t, db)
			slug := "themed-" + name

			if _, err := db.ExecContext(ctx,
				`INSERT INTO pages (title, slug, body, status, author_id, language_code, canonical_url, created_at, updated_at)
				 VALUES (?, ?, ?, 'published', ?, 'en', ?, datetime('now'), datetime('now'))`,
				"Themed", slug, "<p>Body</p>", admin.ID, raw); err != nil {
				t.Fatalf("insert page: %v", err)
			}

			h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
			page, err := h.queries.GetPageBySlug(ctx, slug)
			if err != nil {
				t.Fatalf("GetPageBySlug: %v", err)
			}
			if page.CanonicalUrl != raw {
				t.Fatalf("fixture canonical_url = %q, want the raw legacy value %q", page.CanonicalUrl, raw)
			}

			view := h.pageToView(ctx, page, "en", "")
			if view.CanonicalURL != "" {
				t.Errorf("PageView.CanonicalURL = %q, want it dropped before reaching theme data", view.CanonicalURL)
			}
		})
	}
}

// TestPageToViewKeepsUsableCanonicalURL is the other half: a valid value must
// still reach themes, trimmed.
func TestPageToViewKeepsUsableCanonicalURL(t *testing.T) {
	db, _ := testHandlerSetup(t)
	ctx := context.Background()
	admin := createTestAdminUser(t, db)

	if _, err := db.ExecContext(ctx,
		`INSERT INTO pages (title, slug, body, status, author_id, language_code, canonical_url, created_at, updated_at)
		 VALUES (?, ?, ?, 'published', ?, 'en', ?, datetime('now'), datetime('now'))`,
		"Themed", "themed-ok", "<p>Body</p>", admin.ID, "  https://example.com/kept  "); err != nil {
		t.Fatalf("insert page: %v", err)
	}

	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	page, err := h.queries.GetPageBySlug(ctx, "themed-ok")
	if err != nil {
		t.Fatalf("GetPageBySlug: %v", err)
	}
	if view := h.pageToView(ctx, page, "en", ""); view.CanonicalURL != "https://example.com/kept" {
		t.Errorf("PageView.CanonicalURL = %q, want the trimmed value", view.CanonicalURL)
	}
}
