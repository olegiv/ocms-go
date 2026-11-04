// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"slices"
	"testing"
)

func TestRemovedMarkup(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after string
		want          []string
	}{
		{"identical", `<p>hi</p>`, `<p>hi</p>`, nil},
		{"nofollow added", `<a href="/x">l</a>`, `<a href="/x" rel="nofollow">l</a>`, nil},
		{"rel rewritten", `<a href="/x" rel="noopener noreferrer">l</a>`, `<a href="/x" rel="nofollow">l</a>`, nil},
		{"entity normalized", `<p>caf&eacute;</p>`, `<p>café</p>`, nil},
		{"style reformatted", `<p style="text-align:center;">x</p>`, `<p style="text-align: center">x</p>`, nil},
		{"element dropped", `<font color="red">x</font>`, `x`, []string{"<font>"}},
		{"attributes of a dropped element not repeated", `<p><iframe src="x" width="5"></iframe></p>`, `<p></p>`, []string{"<iframe>"}},
		{"attribute dropped", `<a href="/x" onclick="go()">l</a>`, `<a href="/x" rel="nofollow">l</a>`, []string{`<a onclick="…">`}},
		{"whole style dropped", `<p style="color:red">x</p>`, `<p>x</p>`, []string{`<p style="color: …">`}},
		{"style without declarations dropped", `<p style="not-a-declaration">x</p>`, `<p>x</p>`, []string{`<p style="…">`}},
		{"one declaration dropped", `<p style="text-align:center;position:fixed">x</p>`, `<p style="text-align: center">x</p>`, []string{`<p style="position: …">`}},
		{"several, sorted", `<p onclick="x" style="z-index:9"><b>a</b><iframe></iframe></p>`, `<p><b>a</b></p>`, []string{"<iframe>", `<p onclick="…">`, `<p style="z-index: …">`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RemovedMarkup(tc.before, tc.after); !slices.Equal(got, tc.want) {
				t.Errorf("RemovedMarkup() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRemovedMarkupAgainstThePolicy runs the real page sanitizer, so the
// detector is pinned to what the policy actually removes and rewrites.
func TestRemovedMarkupAgainstThePolicy(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`<p><a href="https://example.com" target="_blank">x</a> caf&eacute;</p>`, nil},
		{`<p><iframe src="https://www.youtube.com/embed/x"></iframe></p>`, []string{"<iframe>"}},
		{`<p style="position: fixed; text-align: center">x</p>`, []string{`<p style="position: …">`}},
		{`<img src="/x.png" alt="" onerror="alert(1)">`, []string{`<img onerror="…">`}},
		{`<script>alert(1)</script><p>x</p>`, []string{"<script>"}},
		{`<p style="not-a-declaration">x</p>`, []string{`<p style="…">`}},
	} {
		if got := RemovedMarkup(tc.body, SanitizePageHTML(tc.body)); !slices.Equal(got, tc.want) {
			t.Errorf("RemovedMarkup(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}
