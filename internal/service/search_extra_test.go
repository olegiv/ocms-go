// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"strings"
	"testing"
)

func TestGenerateExcerpt_MatchInMiddle(t *testing.T) {
	svc := &SearchService{}

	// Build a body where the match is well past the first maxLen/3 characters,
	// so that start > 0 and the excerpt gets a leading "..."
	prefix := strings.Repeat("x ", 100) // ~200 chars before the keyword
	body := prefix + "targetword " + strings.Repeat("y ", 50)
	got := svc.generateExcerpt(body, "targetword", 50)

	if !strings.HasPrefix(got, "...") {
		t.Errorf("expected excerpt to start with '...', got %q", got)
	}
	if !strings.Contains(got, "targetword") {
		t.Errorf("expected excerpt to contain 'targetword', got %q", got)
	}
}

func TestGenerateExcerpt_MatchNearEnd(t *testing.T) {
	svc := &SearchService{}

	// Match near the end: excerpt should end with "..." only when end < len(body)
	prefix := strings.Repeat("a ", 5) // short prefix
	body := prefix + "keyword " + strings.Repeat("z ", 200)
	got := svc.generateExcerpt(body, "keyword", 30)

	if !strings.Contains(got, "keyword") {
		t.Errorf("expected excerpt to contain 'keyword', got %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected excerpt to end with '...', got %q", got)
	}
}

func TestGenerateExcerpt_BodyExactlyMaxLen(t *testing.T) {
	svc := &SearchService{}

	// Body exactly equals maxLen - no ellipsis for no-match case
	body := strings.Repeat("a", 20)
	got := svc.generateExcerpt(body, "nomatch", 20)

	if got != body {
		t.Errorf("generateExcerpt(exact maxLen, no match) = %q, want %q", got, body)
	}
}
