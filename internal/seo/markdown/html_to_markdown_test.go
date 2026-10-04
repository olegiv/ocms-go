// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package markdown

import (
	"strings"
	"testing"
)

func TestHTMLToMarkdown(t *testing.T) {
	got, err := HTMLToMarkdown(`<h2>Heading</h2><p>Some <strong>bold</strong> text and a <a href="https://example.com/x">link</a>.</p>`)
	if err != nil {
		t.Fatalf("HTMLToMarkdown error: %v", err)
	}
	for _, want := range []string{"## Heading", "**bold**", "[link](https://example.com/x)"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

// TestHTMLToMarkdownStripsActiveContent guards the shared conversion path: the
// MCP get_page tool returns this output to AI agents, so scripts and iframes
// must not survive it any more than they survive the public representation.
func TestHTMLToMarkdownStripsActiveContent(t *testing.T) {
	got, err := HTMLToMarkdown(`<p>Hi</p><script>alert(1)</script><iframe src="javascript:evil"></iframe>`)
	if err != nil {
		t.Fatalf("HTMLToMarkdown error: %v", err)
	}
	for _, forbidden := range []string{"<script", "alert(1)", "<iframe", "javascript:"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("output unexpectedly contains %q:\n%s", forbidden, got)
		}
	}
}

func TestHTMLToMarkdownSizeCap(t *testing.T) {
	if _, err := HTMLToMarkdown(strings.Repeat("x", MaxHTMLBytes+1)); err == nil {
		t.Fatal("expected error on oversized input")
	}
	if _, err := HTMLToMarkdown(strings.Repeat("x", MaxHTMLBytes)); err != nil {
		t.Fatalf("input at the cap must convert: %v", err)
	}
}
