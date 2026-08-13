// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import "testing"

// TestRewriteAbsoluteDrupalURLs covers body HTML that references files by their
// full old-site URL rather than by path.
//
// Rewriting only the path suffix left the old origin in place, producing
// "https://old.example/uploads/originals/…" — a URL that does not exist on
// either host, so every such image stayed broken after migration.
func TestRewriteAbsoluteDrupalURLs(t *testing.T) {
	refs := NewMediaRefs()
	refs.ByPath["a.jpg"] = "/uploads/originals/uuid-1/a.jpg"

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "absolute https URL",
			body: `<img src="https://old.example/sites/default/files/a.jpg">`,
			want: `<img src="/uploads/originals/uuid-1/a.jpg">`,
		},
		{
			name: "absolute http URL",
			body: `<img src="http://old.example/sites/default/files/a.jpg">`,
			want: `<img src="/uploads/originals/uuid-1/a.jpg">`,
		},
		{
			name: "protocol-relative URL",
			body: `<img src="//old.example/sites/default/files/a.jpg">`,
			want: `<img src="/uploads/originals/uuid-1/a.jpg">`,
		},
		{
			name: "already relative",
			body: `<img src="/sites/default/files/a.jpg">`,
			want: `<img src="/uploads/originals/uuid-1/a.jpg">`,
		},
		{
			name: "unknown file keeps its original URL",
			body: `<img src="https://old.example/sites/default/files/missing.jpg">`,
			want: `<img src="https://old.example/sites/default/files/missing.jpg">`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewritePrefixedURLs(tc.body, "/sites/default/files/", refs); got != tc.want {
				t.Errorf("rewritePrefixedURLs()\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}
