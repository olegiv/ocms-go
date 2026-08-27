// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

func TestRedirectHandlerSkipsOnlyAdminAndAPISegments(t *testing.T) {
	rm := &RedirectsMiddleware{
		redirects: []store.Redirect{{
			SourcePath: "/administrator",
			TargetUrl:  "/target",
			StatusCode: http.StatusMovedPermanently,
			Enabled:    true,
		}, {
			SourcePath: "/apiary",
			TargetUrl:  "/target",
			StatusCode: http.StatusMovedPermanently,
			Enabled:    true,
		}},
		lastLoad: time.Now(),
		cacheTTL: time.Hour,
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := rm.Handler(next)

	tests := []struct {
		path       string
		wantStatus int
	}{
		{path: "/admin", wantStatus: http.StatusNoContent},
		{path: "/admin/users", wantStatus: http.StatusNoContent},
		{path: "/api", wantStatus: http.StatusNoContent},
		{path: "/api/v2/pages", wantStatus: http.StatusNoContent},
		{path: "/administrator", wantStatus: http.StatusMovedPermanently},
		{path: "/apiary", wantStatus: http.StatusMovedPermanently},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}

func TestMatchPathWithCaptures(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		request      string
		isWildcard   bool
		wantMatch    bool
		wantCaptures []string
	}{
		// Exact matches (no wildcard)
		{
			name:         "exact match",
			source:       "/old-page",
			request:      "/old-page",
			isWildcard:   false,
			wantMatch:    true,
			wantCaptures: nil,
		},
		{
			name:         "exact no match",
			source:       "/old-page",
			request:      "/new-page",
			isWildcard:   false,
			wantMatch:    false,
			wantCaptures: nil,
		},
		// Single wildcard tests
		{
			name:         "single wildcard matches one segment",
			source:       "/blog/*",
			request:      "/blog/post1",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"post1"},
		},
		{
			name:         "single wildcard matches different segment",
			source:       "/blog/*",
			request:      "/blog/another-post",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"another-post"},
		},
		{
			name:         "single wildcard does not match nested paths",
			source:       "/blog/*",
			request:      "/blog/2024/post1",
			isWildcard:   true,
			wantMatch:    false,
			wantCaptures: nil,
		},
		{
			name:         "single wildcard in middle",
			source:       "/products/*/details",
			request:      "/products/123/details",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"123"},
		},
		{
			name:         "single wildcard in middle no match",
			source:       "/products/*/details",
			request:      "/products/123/info",
			isWildcard:   true,
			wantMatch:    false,
			wantCaptures: nil,
		},
		// Double wildcard tests
		{
			name:         "double wildcard matches multiple segments",
			source:       "/old-blog/**",
			request:      "/old-blog/2024/01/post1",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"2024/01/post1"},
		},
		{
			name:         "double wildcard matches zero segments",
			source:       "/old-blog/**",
			request:      "/old-blog",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{""},
		},
		{
			name:         "double wildcard matches one segment",
			source:       "/old-blog/**",
			request:      "/old-blog/post1",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"post1"},
		},
		{
			name:         "double wildcard in middle",
			source:       "/api/**/v1",
			request:      "/api/users/profile/v1",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"users/profile"},
		},
		// Multiple wildcards
		{
			name:         "two single wildcards",
			source:       "/*/products/*",
			request:      "/shop/products/123",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"shop", "123"},
		},
		// Edge cases
		{
			name:         "root path",
			source:       "/",
			request:      "/",
			isWildcard:   false,
			wantMatch:    true,
			wantCaptures: nil,
		},
		{
			name:         "wildcard flag false prevents wildcard matching",
			source:       "/blog/*",
			request:      "/blog/post1",
			isWildcard:   false,
			wantMatch:    false,
			wantCaptures: nil,
		},
		// Prefix wildcard tests (trailing * not preceded by /)
		{
			name:         "prefix wildcard matches exact path",
			source:       "/user/login*",
			request:      "/user/login",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{""},
		},
		{
			name:         "prefix wildcard matches with trailing slash",
			source:       "/user/login*",
			request:      "/user/login/",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{""},
		},
		{
			name:         "prefix wildcard matches path with segment",
			source:       "/user/login*",
			request:      "/user/login/google",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"google"},
		},
		{
			name:         "prefix wildcard matches path suffix",
			source:       "/user/login*",
			request:      "/user/loginX",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"X"},
		},
		{
			name:         "prefix wildcard matches nested path",
			source:       "/user/login*",
			request:      "/user/login/a/b/c",
			isWildcard:   true,
			wantMatch:    true,
			wantCaptures: []string{"a/b/c"},
		},
		{
			name:         "prefix wildcard no match short path",
			source:       "/user/login*",
			request:      "/user/log",
			isWildcard:   true,
			wantMatch:    false,
			wantCaptures: nil,
		},
		{
			name:         "prefix wildcard no match different path",
			source:       "/user/login*",
			request:      "/admin/login",
			isWildcard:   true,
			wantMatch:    false,
			wantCaptures: nil,
		},
	}

	rm := &RedirectsMiddleware{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMatch, gotCaptures := rm.matchPathWithCaptures(tt.request, tt.source, tt.isWildcard)
			if gotMatch != tt.wantMatch {
				t.Errorf("matchPathWithCaptures(%q, %q, %v) match = %v, want %v",
					tt.request, tt.source, tt.isWildcard, gotMatch, tt.wantMatch)
			}
			if tt.wantCaptures != nil {
				if len(gotCaptures) != len(tt.wantCaptures) {
					t.Errorf("matchPathWithCaptures(%q, %q, %v) captures = %v, want %v",
						tt.request, tt.source, tt.isWildcard, gotCaptures, tt.wantCaptures)
				} else {
					for i, want := range tt.wantCaptures {
						if gotCaptures[i] != want {
							t.Errorf("matchPathWithCaptures(%q, %q, %v) captures[%d] = %q, want %q",
								tt.request, tt.source, tt.isWildcard, i, gotCaptures[i], want)
						}
					}
				}
			}
		})
	}
}

func TestBuildTargetURL(t *testing.T) {
	rm := &RedirectsMiddleware{}

	tests := []struct {
		name       string
		sourcePath string
		targetURL  string
		isWildcard bool
		captures   []string
		want       string
	}{
		{
			name:       "non-wildcard returns target as-is",
			sourcePath: "/old-page",
			targetURL:  "/new-page",
			isWildcard: false,
			captures:   nil,
			want:       "/new-page",
		},
		{
			name:       "single wildcard substitution at end",
			sourcePath: "/old-blog/*",
			targetURL:  "/new-blog/*",
			isWildcard: true,
			captures:   []string{"post1"},
			want:       "/new-blog/post1",
		},
		{
			name:       "single wildcard substitution in middle",
			sourcePath: "/products/*/details",
			targetURL:  "/items/*/info",
			isWildcard: true,
			captures:   []string{"123"},
			want:       "/items/123/info",
		},
		{
			name:       "double wildcard substitution",
			sourcePath: "/old-blog/**",
			targetURL:  "/new-blog/**",
			isWildcard: true,
			captures:   []string{"2024/01/post1"},
			want:       "/new-blog/2024/01/post1",
		},
		{
			name:       "double wildcard in middle",
			sourcePath: "/api/**/v1",
			targetURL:  "/api/**/v2",
			isWildcard: true,
			captures:   []string{"users/profile"},
			want:       "/api/users/profile/v2",
		},
		{
			name:       "multiple wildcards substitution",
			sourcePath: "/*/products/*",
			targetURL:  "/store/*/items/*",
			isWildcard: true,
			captures:   []string{"shop", "123"},
			want:       "/store/shop/items/123",
		},
		{
			name:       "external URL with wildcard",
			sourcePath: "/old-blog/*",
			targetURL:  "https://example.com/blog/*",
			isWildcard: true,
			captures:   []string{"my-post"},
			want:       "https://example.com/blog/my-post",
		},
		{
			name:       "external URL without wildcard",
			sourcePath: "/old-page",
			targetURL:  "https://example.com/new-page",
			isWildcard: false,
			captures:   nil,
			want:       "https://example.com/new-page",
		},
		{
			name:       "empty captures returns target as-is",
			sourcePath: "/path/*",
			targetURL:  "/other/*",
			isWildcard: true,
			captures:   []string{},
			want:       "/other/*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rd := store.Redirect{
				SourcePath: tt.sourcePath,
				TargetUrl:  tt.targetURL,
				IsWildcard: tt.isWildcard,
			}
			got := rm.buildTargetURL(rd, tt.captures)
			if got != tt.want {
				t.Errorf("buildTargetURL(%+v, %v) = %q, want %q",
					rd, tt.captures, got, tt.want)
			}
		})
	}
}

// TestWildcardMatcherAgreesWithMigratorCopy is a drift guard.
//
// modules/migrator/sources/shared.WildcardRedirectMatchesPath reimplements this
// middleware's wildcard semantics so the importer can refuse a slug that a
// redirect already answers. They are two independent copies of one rule: if
// this matcher changes and that one does not, the importer hands out slugs the
// middleware then shadows, and the imported pages are unreachable with every
// test in the repo still green.
func TestWildcardMatcherAgreesWithMigratorCopy(t *testing.T) {
	rm := &RedirectsMiddleware{}
	patterns := []string{
		"/blog/*", "/blog/**", "/news*", "/news/*", "/a/**/z", "/a/*/z",
		"/**", "/*", "/exact", "/deep/**/nested/*", "/trailing/**",
	}
	paths := []string{
		"/blog", "/blog/", "/blog/x", "/blog/x/y", "/blog/x/y/z",
		"/news", "/news/", "/newsletter", "/archive/news",
		"/a/z", "/a/b/z", "/a/b/c/z", "/a/b/c",
		"/exact", "/other", "/", "/deep/nested/x", "/deep/1/2/nested/x",
		"/trailing", "/trailing/x/y",
	}

	mismatches := 0
	for _, pattern := range patterns {
		for _, path := range paths {
			mine, _ := rm.matchPathWithCaptures(path, pattern, true)
			theirs := shared.WildcardRedirectMatchesPath(pattern, path)
			if mine != theirs {
				mismatches++
				if mismatches <= 10 {
					t.Errorf("pattern %q path %q: middleware=%v migrator=%v",
						pattern, path, mine, theirs)
				}
			}
		}
	}
	if mismatches > 10 {
		t.Errorf("%d total mismatches (first 10 shown)", mismatches)
	}
}
