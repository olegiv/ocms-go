// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/preview"
)

// TestPreviewPathRecognitionIsAnchoredToThePreviewRoutes fails when the
// preview middleware matches "/preview/" at any depth. A page alias such as
// blog/preview/x is public content: treating it as a preview gave it
// no-store/noindex headers and a script-blocking CSP, and rewrote a path
// below .../preview/share into /redacted so the page answered 404 (security
// audit 2026-11-03, C-3).
func TestPreviewPathRecognitionIsAnchoredToThePreviewRoutes(t *testing.T) {
	for _, p := range []string{
		"/preview/share",
		"/preview/share/open",
		"/preview/pages/1/revisions/2",
		"/ru/preview/pages/1/revisions/2",
		"/pt-br/preview/pages/1/revisions/2",
		"/ru/preview/share",
	} {
		if !preview.IsPath(p) {
			t.Errorf("IsPath(%q) = false, want true: a preview route lost its privacy headers", p)
		}
	}
	for _, p := range []string{
		"/blog/preview/x",                         // blog is owned by an application route, never a language prefix
		"/documentation/preview/share/guide",      // longer than any language code
		"/docs/guide/preview/share",               // not the first segment
		"/ru/docs/preview/pages/1/revisions/2",    // at most one prefix segment
		"/x/y/preview/share/open",                 // two segments before it
		"/uploads/preview/share/x",                // reserved route segment
		"/Preview/share",                          // routes are case-sensitive
		"/preview",                                // no preview route lives at the bare namespace
		"/ru/preview",                             // nor below a prefix
		"/previews/share",                         // a different segment
		"/ru/previews/pages/1/revisions/2",        // a different segment under a prefix
		"/about/preview-notes",                    // substring of a segment
		"/",                                       // empty
		"",                                        // empty
		"//preview/share/abc",                     // an empty segment is not a language code
		"/admin/pages/1/preview-links",            // admin route named like a preview
		"/blog/tag/preview/",                      // deeper path
		"/en-US-extra-long/preview/share/x",       // longer than any language code
		"/a/preview/share/x",                      // one character is no language code
		"/-ru/preview/share/x",                    // malformed code
		"/ru-/preview/share/x",                    // malformed code
		"/r_u/preview/share/x",                    // malformed code
		"/ru//preview/share/x",                    // doubled slash
		"/RU/preview/pages/1/revisions/2/preview", // uppercase is no language code
	} {
		if preview.IsPath(p) {
			t.Errorf("IsPath(%q) = true, want false: an ordinary page would be served as a preview", p)
		}
	}
}

// TestPublicPathContainingPreviewIsServedUnchanged runs the two middlewares
// the way the router stacks them and shows that a public path with
// "preview" in it gets no preview headers or context and is not rewritten,
// while the real preview routes still are.
func TestPublicPathContainingPreviewIsServedUnchanged(t *testing.T) {
	type seen struct {
		path    string
		preview bool
		headers http.Header
	}
	run := func(target string) seen {
		var got seen
		h := preview.RedactRequest(preview.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = seen{path: r.URL.Path, preview: preview.IsRequest(r.Context()), headers: w.Header().Clone()}
		})))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "https://cms.test"+target, nil))
		return got
	}
	for _, target := range []string{
		"/blog/preview/x",
		"/blog/preview/share/guide",
		"/documentation/preview/share/guide",
		"/docs/guide/preview/share/guide",
		"/a/b/preview/share/open",
		"/ru/docs/preview/share/guide",
	} {
		got := run(target)
		if got.path != target {
			t.Errorf("%s was rewritten to %s, so the page is unreachable", target, got.path)
		}
		if got.preview || got.headers.Get("Cache-Control") != "" || got.headers.Get("X-Robots-Tag") != "" || got.headers.Get("Content-Security-Policy") != "" {
			t.Errorf("%s was served with preview headers or context: %v", target, got.headers)
		}
	}
	for _, target := range []string{"/preview/pages/1/revisions/2", "/ru/preview/pages/1/revisions/2", "/preview/share", "/ru/preview/share"} {
		got := run(target)
		if !got.preview || got.headers.Get("Cache-Control") != "private, no-store" || !strings.Contains(got.headers.Get("Content-Security-Policy"), "script-src 'none'") {
			t.Errorf("%s lost its preview headers or context", target)
		}
	}
	if got := run("/ru/preview/share/some-legacy-bearer"); got.path != "/ru/preview/share/redacted" {
		t.Errorf("a language-prefixed legacy share path was not redacted: %s", got.path)
	}
	if got := run("/preview/share/some-legacy-bearer"); got.path != "/preview/share/redacted" {
		t.Errorf("a legacy share path was not redacted: %s", got.path)
	}
}
