// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import "testing"

func TestCanonicalLanguagePath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		code      string
		isDefault bool
		want      string
		wantOK    bool
	}{
		{"default language has no prefix", "/hello", "en", true, "/hello", true},
		{"other language is prefixed", "/hello", "ru", false, "/ru/hello", true},
		{"taxonomy path keeps its section", "/tag/go", "de", false, "/de/tag/go", true},
		{"reserved code never routes", "/hello", "api", false, "", false},
		{"reserved code never routes even as default", "/hello", "admin", true, "", false},
		{"invalid code never routes", "/hello", "EN_us", false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CanonicalLanguagePath(tt.path, tt.code, tt.isDefault)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("CanonicalLanguagePath(%q, %q, %v) = (%q, %v), want (%q, %v)",
					tt.path, tt.code, tt.isDefault, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
