// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// SuspiciousPageHTMLTokens lists substrings that indicate potentially
// malicious markup in user-supplied page HTML.
var SuspiciousPageHTMLTokens = []string{
	"<script",
	"onerror=",
	"onload=",
	"<iframe",
}

// JavascriptURIPattern matches javascript: in attribute contexts only,
// including HTML-entity-encoded leading whitespace bypasses (tab, LF, VT,
// FF, CR, space) in hex, decimal, and named forms. Semicolons on numeric
// entities are optional to match legacy browser behaviour. Double-encoded
// entity prefixes (&amp;) are also matched.
//
// The pattern avoids false positives on plain text like "JavaScript: a
// language" by requiring an = before the value (attribute context).
var JavascriptURIPattern = regexp.MustCompile(
	`(?i)=[\s\x0b]*["']?[\s\x0b]*` +
		`(?:` +
		`(?:&(?:amp;)?#x0*(?:9|a|b|c|d|20);?)` + // hex entities
		`|(?:&(?:amp;)?#0*(?:9|10|11|12|13|32);?)` + // decimal entities
		`|(?:&(?:tab|newline);)` + // named entities
		`|[\s\x0b]` + // literal whitespace incl. vertical tab
		`)*` +
		`javascript:`,
)

var pageHTMLSanitizer = buildPageHTMLSanitizer()

func buildPageHTMLSanitizer() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Allow Prism.js language classes on pre/code elements for syntax highlighting.
	// Pattern permits "language-go", "language-bash", etc.
	p.AllowAttrs("class").Matching(
		regexp.MustCompile(`^language-[a-zA-Z0-9_-]+(\s+line-numbers)?$`),
	).OnElements("pre", "code")
	// Allow the informer module's demo-credential marker so the banner JS can swap
	// in the current demo admin password at runtime.
	p.AllowAttrs("class").Matching(
		regexp.MustCompile(`^ocms-demo-pw$`),
	).OnElements("strong")
	return p
}

// DetectSuspiciousHTMLTokens returns the subset of SuspiciousPageHTMLTokens
// found in body (case-insensitive), plus "javascript:" if the URI pattern
// matches. Callers use the result to warn or block page saves.
func DetectSuspiciousHTMLTokens(body string) []string {
	lower := strings.ToLower(body)
	var matches []string
	for _, token := range SuspiciousPageHTMLTokens {
		if strings.Contains(lower, token) {
			matches = append(matches, token)
		}
	}
	if JavascriptURIPattern.MatchString(body) {
		matches = append(matches, "javascript:")
	}
	return matches
}

// SanitizePageHTML sanitizes rich-text page HTML with a conservative UGC policy.
func SanitizePageHTML(raw string) string {
	return pageHTMLSanitizer.Sanitize(raw)
}
