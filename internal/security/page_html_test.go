// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"strings"
	"testing"
)

func TestJavascriptURIPattern(t *testing.T) {
	tests := []struct {
		name  string
		input string
		match bool
	}{
		// Basic attribute-context matches
		{"plain js URI", `href="javascript:alert(1)"`, true},
		{"single quotes", `href='javascript:alert(1)'`, true},
		{"no quotes", `href=javascript:alert(1)`, true},
		{"spaces before js", `href="  javascript:alert(1)"`, true},
		{"case insensitive", `href="JavaScript:alert(1)"`, true},
		{"JAVASCRIPT upper", `href="JAVASCRIPT:alert(1)"`, true},

		// Hex entity bypasses
		{"hex tab &#x09;", `href="&#x09;javascript:alert(1)"`, true},
		{"hex LF &#x0A;", `href="&#x0A;javascript:alert(1)"`, true},
		{"hex VT &#x0B;", `href="&#x0B;javascript:alert(1)"`, true},
		{"hex FF &#x0C;", `href="&#x0C;javascript:alert(1)"`, true},
		{"hex CR &#x0D;", `href="&#x0D;javascript:alert(1)"`, true},
		{"hex space &#x20;", `href="&#x20;javascript:alert(1)"`, true},
		{"uppercase X &#X09;", `href="&#X09;javascript:alert(1)"`, true},
		{"leading zeros &#x00000009;", `href="&#x00000009;javascript:alert(1)"`, true},

		// Decimal entity bypasses
		{"decimal tab &#9;", `href="&#9;javascript:alert(1)"`, true},
		{"decimal LF &#10;", `href="&#10;javascript:alert(1)"`, true},
		{"decimal VT &#11;", `href="&#11;javascript:alert(1)"`, true},
		{"decimal FF &#12;", `href="&#12;javascript:alert(1)"`, true},
		{"decimal CR &#13;", `href="&#13;javascript:alert(1)"`, true},
		{"decimal space &#32;", `href="&#32;javascript:alert(1)"`, true},
		{"decimal leading zeros &#0009;", `href="&#0009;javascript:alert(1)"`, true},

		// Named entity bypasses
		{"named tab", `href="&tab;javascript:alert(1)"`, true},
		{"named newline", `href="&newline;javascript:alert(1)"`, true},
		{"named Tab mixed case", `href="&Tab;javascript:alert(1)"`, true},
		{"named NEWLINE upper", `href="&NEWLINE;javascript:alert(1)"`, true},

		// Semicolon-optional (legacy browser compat)
		{"hex no semicolon &#x09", `href="&#x09javascript:alert(1)"`, true},
		{"decimal no semicolon &#9", `href="&#9javascript:alert(1)"`, true},

		// Double-encoded entity prefix (&amp;)
		{"double-encoded hex", `href="&amp;#x09;javascript:alert(1)"`, true},
		{"double-encoded decimal", `href="&amp;#9;javascript:alert(1)"`, true},

		// Multiple chained entities
		{"chained hex entities", `href="&#x09;&#x0A;javascript:alert(1)"`, true},
		{"chained mixed entities", `href="&#9;&tab;javascript:alert(1)"`, true},

		// Literal whitespace bytes
		{"literal tab byte", "href=\"\tjavascript:alert(1)\"", true},
		{"literal LF byte", "href=\"\njavascript:alert(1)\"", true},
		{"literal FF byte", "href=\"\fjavascript:alert(1)\"", true},
		{"literal VT byte", "href=\"\x0bjavascript:alert(1)\"", true},

		// Non-matches (false positive checks)
		{"plain text mention", `<strong>JavaScript:</strong> a language`, false},
		{"paragraph text", `<p>JavaScript: the good parts</p>`, false},
		{"entity in text context", `<p>Use &#x09; for tabs in HTML</p>`, false},
		{"safe href", `href="https://example.com"`, false},
		{"double-encoded display", `&amp;amp;#x09;javascript:alert(1)`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := JavascriptURIPattern.MatchString(tt.input)
			if got != tt.match {
				t.Errorf("JavascriptURIPattern.MatchString(%q) = %v, want %v", tt.input, got, tt.match)
			}
		})
	}
}

func TestDetectSuspiciousHTMLTokens(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		expect []string
	}{
		{"script tag", `<p>Hello</p><script>alert(1)</script>`, []string{"<script"}},
		{"onerror attr", `<img onerror="alert(1)">`, []string{"onerror="}},
		{"iframe tag", `<iframe src="evil.html">`, []string{"<iframe"}},
		{"javascript URI", `<a href="javascript:alert(1)">x</a>`, []string{"javascript:"}},
		{"entity bypass", `<a href="&#x09;javascript:alert(1)">x</a>`, []string{"javascript:"}},
		{"clean HTML", `<p>Hello <strong>world</strong></p>`, nil},
		{"multiple tokens", `<script>x</script><img onerror="y">`, []string{"<script", "onerror="}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectSuspiciousHTMLTokens(tt.body)
			if len(got) != len(tt.expect) {
				t.Fatalf("DetectSuspiciousHTMLTokens() = %v, want %v", got, tt.expect)
			}
			for i, token := range tt.expect {
				if got[i] != token {
					t.Errorf("token[%d] = %q, want %q", i, got[i], token)
				}
			}
		})
	}
}

func TestSanitizePageHTML(t *testing.T) {
	raw := `<p>Hello</p><script>alert('x')</script><a href="javascript:alert(1)">x</a>`
	got := SanitizePageHTML(raw)

	if strings.Contains(got, "<script") {
		t.Fatalf("expected script tags removed, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Fatalf("expected javascript URLs removed, got %q", got)
	}
	if !strings.Contains(got, "<p>Hello</p>") {
		t.Fatalf("expected safe paragraph preserved, got %q", got)
	}
}
