// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

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
