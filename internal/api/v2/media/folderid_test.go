// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package media

import (
	"strings"
	"testing"
)

// TestParseFolderIDRejectsOverflow asserts parseFolderID rejects numeric input
// that doesn't fit in int64, instead of silently wrapping to a positive value
// that could route an upload into the wrong folder. Catches the regression
// Codex flagged on commit d7797f3 where the hand-rolled id*10+digit scan
// wrapped on 20-plus-digit inputs.
func TestParseFolderIDRejectsOverflow(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
	}{
		{"empty", "", true},
		{"zero", "0", true},
		{"negative-sign", "-1", true},
		{"non-numeric", "abc", true},
		{"mixed", "12a3", true},
		{"twenty-nines", strings.Repeat("9", 20), true},
		{"max-int64-plus-one", "9223372036854775808", true},
		{"valid", "42", false},
		{"max-int64", "9223372036854775807", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFolderID(tt.input)
			if tt.wantNil && got != nil {
				t.Errorf("parseFolderID(%q) = %d; want nil", tt.input, *got)
			}
			if !tt.wantNil && got == nil {
				t.Errorf("parseFolderID(%q) = nil; want non-nil", tt.input)
			}
		})
	}
}
