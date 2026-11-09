// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestRouteConflictRefusesPreviewSegments keeps page paths out of the preview
// namespace. The preview middleware cannot tell "docs/preview/x" from
// "ru/preview/x" before the active languages are known, so a page or alias
// starting with preview, or with preview after a language-shaped segment,
// would be served as a preview (no-store, noindex, no scripts) or, below
// .../preview/share, rewritten into a 404 (security audit 2026-11-03, C-3).
// A preview segment deeper in the path is ordinary content and stays allowed,
// so existing aliases keep saving.
//
// Bug state: drop the util.ShadowsPreviewRoute check from RouteConflict, or
// make it refuse every preview segment, and this fails.
func TestRouteConflictRefusesPreviewSegments(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, path := range []string{
		"preview/x",
		"docs/preview/share/guide",
		"ru/preview",
		"news/preview/guide",
	} {
		conflict, err := pagewrite.RouteConflict(ctx, f.q, path)
		if err != nil || conflict == "" {
			t.Errorf("RouteConflict(%q) = %q, %v; want a refusal", path, conflict, err)
		}
		_, err = pagewrite.AddImportedAlias(ctx, f.q, f.page.ID, path, time.Now().UTC())
		var refusal *pagewrite.Refusal
		if !errors.As(err, &refusal) {
			t.Errorf("AddImportedAlias(%q) = %v; want a refusal, so an imported alias cannot be served as a preview", path, err)
		}
	}
	// Segments that only contain the word, and preview deeper in the path, are
	// ordinary paths.
	for _, path := range []string{"previews", "preview-notes", "docs/previews/x", "about/preview-guide", "docs/guide",
		"blog/preview/x", "blog/preview", "docs/guide/preview", "a/b/preview/c/d"} {
		if conflict, err := pagewrite.RouteConflict(ctx, f.q, path); err != nil || conflict != "" {
			t.Errorf("RouteConflict(%q) = %q, %v; want none", path, conflict, err)
		}
	}
}

// TestSlugAvailableInLanguageRefusesPreviewSegment covers the canonical
// /ru/preview route of an imported non-default-language page.
func TestSlugAvailableInLanguageRefusesPreviewSegment(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := f.q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", Position: 2, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var refusal *pagewrite.Refusal
	if err := pagewrite.SlugAvailableInLanguage(ctx, f.q, "preview", "ru", 0); !errors.As(err, &refusal) {
		t.Fatalf("slug preview in ru = %v; want a refusal", err)
	}
	if err := pagewrite.SlugAvailableInLanguage(ctx, f.q, "preview-notes", "ru", 0); err != nil {
		t.Fatalf("slug preview-notes in ru = %v; want it available", err)
	}
}

// TestNoPageAliasCanBeRecognizedAsPreview is the invariant behind anchoring
// the preview middleware: it judges a leading segment by language-code grammar
// before the active languages are known, so "docs/preview/x" cannot be told
// from "ru/preview/x". Every path it would treat as a preview route (headers,
// context or redaction) must therefore be one that page writes refuse,
// whatever the first segment.
//
// Bug state: make util.ShadowsPreviewRoute return false, or widen
// preview.IsPath, and this names the paths a page could take.
func TestNoPageAliasCanBeRecognizedAsPreview(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	recognized := 0
	for _, prefix := range []string{"", "ru/", "docs/", "blog/", "pt-br/", "documentation/", "a/b/", "x1/", "tag/", "en/news/"} {
		for _, tail := range []string{"preview", "preview/x", "preview/share", "preview/share/guide", "preview/pages/1/revisions/2"} {
			candidate := prefix + tail
			rewritten := false
			preview.RedactRequest(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				rewritten = r.URL.Path != "/"+candidate
			})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/"+candidate, nil))
			if !preview.IsPath("/"+candidate) && !rewritten {
				continue
			}
			recognized++
			conflict, err := pagewrite.RouteConflict(ctx, f.q, candidate)
			if err != nil || conflict == "" {
				t.Errorf("preview middleware recognizes /%s but a page alias may take it (%q, %v), so the page would be served as a preview", candidate, conflict, err)
			}
		}
	}
	if recognized < 20 {
		t.Fatalf("only %d candidate paths were recognized as previews; the corpus no longer exercises the middleware", recognized)
	}
}
