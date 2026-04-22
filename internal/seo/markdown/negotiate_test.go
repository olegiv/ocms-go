// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package markdown

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWantsMarkdown(t *testing.T) {
	cases := []struct {
		name   string
		accept string
		want   bool
	}{
		{"empty header", "", false},
		{"browser default", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", false},
		{"explicit markdown only", "text/markdown", true},
		{"markdown with q", "text/markdown;q=0.9", true},
		{"markdown preferred over html by q", "text/html;q=0.8, text/markdown;q=0.9", true},
		{"html preferred over markdown by q", "text/html;q=0.9, text/markdown;q=0.5", false},
		{"equal q breaks to html", "text/html;q=0.8, text/markdown;q=0.8", false},
		{"wildcard only", "*/*", false},
		{"text wildcard only", "text/*", false},
		{"malformed header yields no match", "not a media type", false},
		{"json with markdown", "application/json, text/markdown", true},
		// RFC 9110 §12.5.1: q=0 means "not acceptable". Drift test for
		// Codex PR #129 review (P2) — a client that explicitly rejects
		// markdown must not receive markdown, even when no HTML entry
		// is listed.
		{"explicit q=0 rejects markdown", "text/markdown;q=0", false},
		{"explicit q=0 with wildcard", "text/markdown;q=0, */*", false},
		{"explicit q=0 wins over html", "text/markdown;q=0, text/html;q=0.5", false},
		// Drift tests for Codex PR #129 round 2: wildcard ranges that
		// cover HTML must cap the HTML-side quality. Without this,
		// markdown would win against any explicit HTML list simply by
		// not mentioning text/html by name.
		{"markdown loses to */* wildcard", "text/markdown;q=0.2, */*;q=0.9", false},
		{"markdown loses to text/* wildcard", "text/markdown;q=0.3, text/*;q=0.8", false},
		{"markdown wins over lower */* wildcard", "text/markdown;q=0.9, */*;q=0.2", true},
		// Drift tests for Codex PR #129 round 3 (RFC 9110 §12.5.1
		// specificity): explicit text/html takes precedence over
		// text/* and */* regardless of their q-values.
		{"explicit html beats higher */*", "text/html;q=0.2, text/markdown;q=0.8, */*;q=0.9", true},
		{"explicit html beats higher text/*", "text/html;q=0.3, text/markdown;q=0.7, text/*;q=0.9", true},
		{"explicit html wins when higher", "text/html;q=0.9, text/markdown;q=0.5, */*;q=0.95", false},
		{"text/* beats */* when no explicit html", "text/*;q=0.4, text/markdown;q=0.6, */*;q=0.9", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.accept != "" {
				r.Header.Set("Accept", c.accept)
			}
			got := WantsMarkdown(r)
			if got != c.want {
				t.Errorf("WantsMarkdown(%q) = %v, want %v", c.accept, got, c.want)
			}
		})
	}
}

func TestWriteMarkdownSetsHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	WriteMarkdown(w, "# Hello\n\nWorld of words here.\n")

	resp := w.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	if ct := resp.Header.Get("Content-Type"); ct != ContentTypeMarkdown {
		t.Errorf("Content-Type = %q, want %q", ct, ContentTypeMarkdown)
	}
	if v := resp.Header.Get("Vary"); v != "Accept" {
		t.Errorf("Vary = %q, want %q", v, "Accept")
	}
	if tok := resp.Header.Get("X-Markdown-Tokens"); tok != "6" {
		// strings.Fields over "# Hello\n\nWorld of words here.\n" -> 6 tokens:
		// "#", "Hello", "World", "of", "words", "here.".
		t.Errorf("X-Markdown-Tokens = %q, want %q", tok, "6")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAddVaryAcceptIdempotent(t *testing.T) {
	w := httptest.NewRecorder()
	AddVaryAccept(w)
	AddVaryAccept(w)
	if got := w.Header().Get("Vary"); got != "Accept" {
		t.Errorf("Vary = %q, want %q (idempotent)", got, "Accept")
	}

	w2 := httptest.NewRecorder()
	w2.Header().Set("Vary", "Cookie")
	AddVaryAccept(w2)
	AddVaryAccept(w2)
	if got := w2.Header().Get("Vary"); got != "Cookie, Accept" {
		t.Errorf("Vary = %q, want %q", got, "Cookie, Accept")
	}
}

func TestPageToMarkdownBasic(t *testing.T) {
	published := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	html := `<p>Hello <strong>world</strong></p><ul><li>one</li><li>two</li></ul>`
	got, err := PageToMarkdown(
		"My Title",
		"A short excerpt.",
		html,
		"https://example.com/my-title",
		&published,
		Labels{PublishedOn: "Published", Source: "Source"},
	)
	if err != nil {
		t.Fatalf("PageToMarkdown error: %v", err)
	}
	for _, want := range []string{
		"# My Title",
		"*Published: 2026-04-22*",
		"> A short excerpt.",
		"**world**",
		"- one",
		"- two",
		"[Source](https://example.com/my-title)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestPageToMarkdownStripsScripts(t *testing.T) {
	html := `<p>Hi</p><script>alert(1)</script><iframe src="javascript:evil"></iframe>`
	got, err := PageToMarkdown("T", "", html, "", nil, Labels{})
	if err != nil {
		t.Fatalf("PageToMarkdown error: %v", err)
	}
	for _, forbidden := range []string{"<script", "alert(1)", "<iframe", "javascript:"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("output unexpectedly contains %q:\n%s", forbidden, got)
		}
	}
}

func TestPageToMarkdownSizeCap(t *testing.T) {
	huge := strings.Repeat("x", MaxHTMLBytes+1)
	_, err := PageToMarkdown("T", "", huge, "", nil, Labels{})
	if err == nil {
		t.Fatal("expected error on oversized input")
	}
}

func TestHomeToMarkdown(t *testing.T) {
	when := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	recent := []RecentPost{
		{Title: "First", URL: "https://example.com/first", PublishedAt: &when, Excerpt: "Intro"},
		{Title: "Second", URL: "https://example.com/second"},
	}
	got := HomeToMarkdown(
		"My Site", "A nice site.", "https://example.com/",
		recent,
		Labels{RecentPosts: "Recent Posts", Source: "Source"},
	)
	for _, want := range []string{
		"# My Site",
		"A nice site.",
		"## Recent Posts",
		"- [First](https://example.com/first) — 2026-04-22",
		"  Intro",
		"- [Second](https://example.com/second)",
		"[Source](https://example.com/)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestHomeToMarkdownEmpty(t *testing.T) {
	got := HomeToMarkdown("", "", "", nil, Labels{})
	if got != "" {
		t.Errorf("expected empty string for empty inputs, got %q", got)
	}
}

// TestPageToMarkdownTitleNewlineStripped guards against heading-break-out:
// a stored title with embedded newlines must not inject additional headings
// into the markdown output. Drift test for audit finding FIND-002.
//
// After sanitization the title collapses to a single line, so although the
// literal characters "##" remain in the text, they are not at column 0 and
// CommonMark does not interpret them as a heading.
func TestPageToMarkdownTitleNewlineStripped(t *testing.T) {
	got, err := PageToMarkdown(
		"Harmless\n\n## Injected Heading\n\nExtra",
		"", "<p>Body</p>", "", nil, Labels{},
	)
	if err != nil {
		t.Fatalf("PageToMarkdown error: %v", err)
	}
	assertNoInjectedHeading(t, got)
	if !strings.HasPrefix(got, "# Harmless") {
		t.Errorf("output does not start with collapsed H1:\n%s", got)
	}
}

// TestHomeToMarkdownSiteNameNewlineStripped is the analogue for the
// homepage site-name heading. Drift test for audit finding FIND-002.
func TestHomeToMarkdownSiteNameNewlineStripped(t *testing.T) {
	got := HomeToMarkdown(
		"Site\n\n## Injected",
		"", "", nil, Labels{},
	)
	assertNoInjectedHeading(t, got)
	if !strings.HasPrefix(got, "# Site") {
		t.Errorf("output does not start with collapsed H1:\n%s", got)
	}
}

// assertNoInjectedHeading fails the test if any line past the first starts
// with '#' — the only # prefix allowed is the original H1 on line 0. This
// is what "no heading injection" means at the CommonMark level.
func assertNoInjectedHeading(t *testing.T, markdown string) {
	t.Helper()
	lines := strings.Split(markdown, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		if strings.HasPrefix(line, "#") {
			t.Errorf("line %d is an injected heading %q; full output:\n%s", i, line, markdown)
		}
	}
}

// TestSanitizeHeading covers the collapsing helper directly so the contract
// is pinned regardless of which call site uses it.
func TestSanitizeHeading(t *testing.T) {
	cases := map[string]string{
		"plain":              "plain",
		"with\nnewline":      "with newline",
		"crlf\r\nstyle":      "crlf style",
		"bare\rcr":           "bare cr",
		"multi\n\nnewlines":  "multi  newlines",
		"mixed\r\n\nline":    "mixed  line",
	}
	for in, want := range cases {
		if got := sanitizeHeading(in); got != want {
			t.Errorf("sanitizeHeading(%q) = %q, want %q", in, got, want)
		}
	}
}

func BenchmarkPageToMarkdown(b *testing.B) {
	// Representative blog post: 20 paragraphs + headings + list + image.
	var sb strings.Builder
	sb.WriteString("<h2>Intro</h2>")
	for i := 0; i < 20; i++ {
		sb.WriteString(`<p>Lorem <em>ipsum</em> dolor <a href="https://x.test">sit</a> amet, consectetur adipiscing elit.</p>`)
	}
	sb.WriteString(`<ul><li>a</li><li>b</li><li>c</li></ul>`)
	sb.WriteString(`<img src="/x.jpg" alt="x">`)
	body := sb.String()

	labels := Labels{PublishedOn: "Published", Source: "Source"}
	when := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := PageToMarkdown("Title", "Excerpt", body, "https://x.test/t", &when, labels); err != nil {
			b.Fatal(err)
		}
	}
}
