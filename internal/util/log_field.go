// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"strings"
	"unicode"
)

// BoundedLogField strips control characters and keeps at most 256 runes.
func BoundedLogField(value string) string {
	var b strings.Builder
	kept := 0
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		kept++
		if kept == 256 {
			break
		}
	}
	return b.String()
}
