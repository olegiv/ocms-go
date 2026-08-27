// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import "testing"

// TestWildcardRedirectMatchesPath covers the matcher directly. It is now the
// single implementation behind three migrator sources, but until this test its
// only coverage was indirect, through Drupal and Elefant importer tests — and
// the "**" branch, the one genuinely tricky part, had none at all.
func TestWildcardRedirectMatchesPath(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		path    string
		want    bool
	}{
		// "*" is exactly one segment.
		{"/blog/*", "/blog/x", true},
		{"/blog/*", "/blog/x/y", false},
		{"/blog/*", "/blog", false},

		// "**" is any number of segments, including none.
		{"/blog/**", "/blog", true},
		{"/blog/**", "/blog/x", true},
		{"/blog/**", "/blog/x/y/z", true},
		{"/a/**/z", "/a/z", true},
		{"/a/**/z", "/a/b/z", true},
		{"/a/**/z", "/a/b/c/z", true},
		{"/a/**/z", "/a/b/c", false},

		// A trailing "*" not preceded by "/" is a prefix match.
		{"/news*", "/news", true},
		{"/news*", "/news/", true},
		{"/news*", "/newsletter", true},
		{"/news*", "/archive/news", false},

		// ...but "/news/*" is a segment wildcard, not a prefix.
		{"/news/*", "/newsletter", false},
		{"/news/*", "/news/latest", true},

		// Surrounding slashes must not change the answer.
		{"blog/*", "/blog/x", true},
		{"/blog/*", "blog/x", true},

		// Literal patterns still work through the same entry point.
		{"/exact", "/exact", true},
		{"/exact", "/other", false},
	} {
		t.Run(tc.pattern+" vs "+tc.path, func(t *testing.T) {
			if got := WildcardRedirectMatchesPath(tc.pattern, tc.path); got != tc.want {
				t.Errorf("WildcardRedirectMatchesPath(%q, %q) = %v, want %v",
					tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

// TestWildcardMatcherIsStableUnderSlashNormalization pins the property the
// importer depends on: the answer cannot change based on how the operator
// happened to type the path.
func TestWildcardMatcherIsStableUnderSlashNormalization(t *testing.T) {
	for _, pattern := range []string{"/blog/**", "/blog/*", "/news*"} {
		for _, path := range []string{"/blog/x", "blog/x", "/blog/x/"} {
			bare := WildcardRedirectMatchesPath(pattern, path)
			slashed := WildcardRedirectMatchesPath(pattern, "/"+trimSlashes(path)+"")
			if bare != slashed {
				t.Errorf("pattern %q: %q gave %v but its normalized form gave %v",
					pattern, path, bare, slashed)
			}
		}
	}
}

func trimSlashes(s string) string {
	for len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
