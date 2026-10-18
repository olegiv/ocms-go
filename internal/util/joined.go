// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import "strings"

// JoinedValueHas reports whether option is one of the values joined with
// ", " in value, the way a checkbox form field's checked options are stored.
// It matches whole values, so "Post" is not found in "Postcard", and an
// option that itself contains ", " is still found.
func JoinedValueHas(value, option string) bool {
	return value == option || strings.HasPrefix(value, option+", ") ||
		strings.HasSuffix(value, ", "+option) || strings.Contains(value, ", "+option+", ")
}
