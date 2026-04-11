// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import "testing"

func TestSafeThemeImageURL(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
	}{
		{name: "empty", in: "", want: ""},
		{name: "uploads path", in: "/uploads/site/logo.png", want: "/uploads/site/logo.png"},
		{name: "static path", in: "/static/themes/default/logo.svg", want: "/static/themes/default/logo.svg"},
		{name: "http url", in: "http://example.com/logo.png", want: "http://example.com/logo.png"},
		{name: "https url", in: "https://cdn.example.com/logo.png", want: "https://cdn.example.com/logo.png"},
		{name: "trim whitespace", in: "   /uploads/site/logo.png  ", want: "/uploads/site/logo.png"},
		{name: "javascript blocked", in: "javascript:alert(1)", want: ""},
		{name: "data blocked", in: "data:image/svg+xml,<svg onload=alert(1)>", want: ""},
		{name: "relative path blocked", in: "images/logo.png", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeThemeImageURL(tt.in); got != tt.want {
				t.Fatalf("safeThemeImageURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
