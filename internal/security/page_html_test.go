// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"strings"
	"testing"
)

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
