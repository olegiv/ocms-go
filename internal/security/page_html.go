// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

// SuspiciousPageHTMLTokens lists case-insensitive substrings whose presence in
// user-supplied page HTML is a strong signal of an injection attempt. Together
// with hasEventHandlerAttr and JavascriptURIPattern it backs the
// OCMS_BLOCK_SUSPICIOUS_PAGE_HTML pre-filter.
//
// This is deliberately a COARSE early-warning pre-filter, never a sanitizer: it
// cannot enumerate every dangerous construct and must not be relied on as the
// sole defense. The authoritative control is SanitizePageHTML
// (OCMS_SANITIZE_PAGE_HTML), a bluemonday allowlist that strips ALL disallowed
// elements and every on* event-handler attribute regardless of name. Because
// inline event handlers are matched by hasEventHandlerAttr, this list only
// enumerates dangerous *elements* and script-bearing attributes that do not
// rely on an on* handler.
var SuspiciousPageHTMLTokens = []string{
	"<script",
	"<iframe",
	"<object",
	"<embed",
	"<svg",
	"<base",
	"srcdoc=",
}

// hasEventHandlerAttr reports whether body contains an element carrying an
// inline on* event-handler attribute (onclick, onerror, onload, onfocus,
// onanimationstart, …). It tokenizes the HTML so attributes are delimited
// exactly as a browser parses them, rather than matching raw bytes: a
// byte-level regex misses handlers that are separated from the previous
// attribute only by a closing quote (`<img src="x"onerror=…>`) or hidden behind
// a quoted `>` (`<img alt=">" onerror=…>`) — both of which browsers execute. The
// handler-name set is open-ended, so a fixed token list cannot keep up; the
// tokenizer covers every `on<letter>…` attribute name.
func hasEventHandlerAttr(body string) bool {
	z := html.NewTokenizer(strings.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			// io.EOF or a tokenizer error: nothing more to inspect.
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			_, hasAttr := z.TagName()
			for hasAttr {
				var key []byte
				key, _, hasAttr = z.TagAttr()
				// TagAttr lowercases keys. Match on<letter>… (e.g. onclick) but
				// not "on" alone or "on-" so we mirror the on[a-z]+ shape.
				if len(key) > 2 && key[0] == 'o' && key[1] == 'n' && key[2] >= 'a' && key[2] <= 'z' {
					return true
				}
			}
		}
	}
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
	if hasEventHandlerAttr(body) {
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
