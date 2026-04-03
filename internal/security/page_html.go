// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

var pageHTMLSanitizer = buildPageHTMLSanitizer()

func buildPageHTMLSanitizer() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Allow Prism.js language classes on pre/code elements for syntax highlighting.
	// Pattern permits "language-go", "language-bash", etc.
	p.AllowAttrs("class").Matching(
		regexp.MustCompile(`^language-[a-zA-Z0-9_-]+(\s+line-numbers)?$`),
	).OnElements("pre", "code")
	return p
}

// SanitizePageHTML sanitizes rich-text page HTML with a conservative UGC policy.
func SanitizePageHTML(raw string) string {
	return pageHTMLSanitizer.Sanitize(raw)
}
