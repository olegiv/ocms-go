// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import "testing"

func TestSanitizeTablePrefix(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"valid simple", "elefant_", "elefant_", false},
		{"valid alphanumeric", "cms2_", "cms2_", false},
		{"valid uppercase", "CMS_", "CMS_", false},
		{"invalid special char", "drop;--", "", true},
		{"invalid space", "my prefix", "", true},
		{"invalid dash", "my-prefix", "", true},
		{"max length", "12345678901234567890", "12345678901234567890", false},
		{"exceeds max length", "123456789012345678901", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeTablePrefix(tt.prefix)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizeTablePrefix(%q) error = %v, wantErr %v", tt.prefix, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("sanitizeTablePrefix(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}
}
