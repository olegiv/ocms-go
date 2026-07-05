// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// SuspiciousPageHTMLTokens lists case-insensitive substrings whose presence in
// user-supplied page HTML is a strong signal of an injection attempt. Together
// with EventHandlerAttrPattern and JavascriptURIPattern it backs the
// OCMS_BLOCK_SUSPICIOUS_PAGE_HTML pre-filter.
//
// This is deliberately a COARSE early-warning pre-filter, never a sanitizer: it
// cannot enumerate every dangerous construct and must not be relied on as the
// sole defense. The authoritative control is SanitizePageHTML
// (OCMS_SANITIZE_PAGE_HTML), a bluemonday allowlist that strips ALL disallowed
// elements and every on* event-handler attribute regardless of name. Because
// inline event handlers are matched structurally by EventHandlerAttrPattern,
// this list only enumerates dangerous *elements* and script-bearing attributes
// that do not rely on an on* handler.
var SuspiciousPageHTMLTokens = []string{
	"<script",
	"<iframe",
	"<object",
	"<embed",
	"<svg",
	"<base",
	"srcdoc=",
}

// EventHandlerAttrPattern matches an inline on* event-handler attribute inside a
// start tag (e.g. `<img ... onerror=`, `<button autofocus onfocus=`,
// `<svg/onload=`). Matching the whole on<name>= shape covers the open-ended set
// of handler names (onclick, onfocus, onmouseover, onanimationstart, ontoggle,
// …) that a fixed token list cannot keep up with, while anchoring to a start
// tag and an attribute-name boundary ([\s/]) avoids false positives on prose
// such as "switch it on = off".
var EventHandlerAttrPattern = regexp.MustCompile(`(?i)<[a-z][^>]*[\s/]on[a-z]+\s*=`)

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

// DetectSuspiciousHTMLTokens returns diagnostic labels for suspicious markup in
// body: the subset of SuspiciousPageHTMLTokens present (case-insensitive),
// "on*=" if an inline event-handler attribute is present, and "javascript:" if
// the URI pattern matches. Callers use a non-empty result to warn or block page
// saves. See SuspiciousPageHTMLTokens for why this is a pre-filter, not a
// sanitizer.
func DetectSuspiciousHTMLTokens(body string) []string {
	lower := strings.ToLower(body)
	var matches []string
	for _, token := range SuspiciousPageHTMLTokens {
		if strings.Contains(lower, token) {
			matches = append(matches, token)
		}
	}
	if EventHandlerAttrPattern.MatchString(body) {
		matches = append(matches, "on*=")
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
