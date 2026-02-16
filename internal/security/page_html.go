// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import "github.com/microcosm-cc/bluemonday"

var pageHTMLSanitizer = bluemonday.UGCPolicy()

// SanitizePageHTML sanitizes rich-text page HTML with a conservative UGC policy.
func SanitizePageHTML(raw string) string {
	return pageHTMLSanitizer.Sanitize(raw)
}
