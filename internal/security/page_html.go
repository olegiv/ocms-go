// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"regexp"
	"slices"
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
	// Link policy is explicit. Forcing nofollow here would override an agent's
	// reviewed keep policy every time the v2 service or frontend sanitizes again.
	p.RequireNoFollowOnLinks(false)
	p.AllowAttrs("rel").Matching(regexp.MustCompile(`^(?:noopener|noreferrer|nofollow)(?: (?:noopener|noreferrer|nofollow))*$`)).OnElements("a")
	// Alt is plain text, escaped by the HTML serializer. The UGC default drops
	// the entire attribute for common punctuation such as colons or ampersands,
	// losing accessible descriptions in both generated and human-authored images.
	p.AllowAttrs("alt").OnElements("img")
	// Generated draft media uses existing local uploads only. Do not accept
	// arbitrary remote srcsets or CSS in responsive-image attributes.
	asset := `/uploads/(?:small|medium|large)/[a-fA-F0-9-]{36}/[^\s"'<>,]+ [1-9][0-9]*w`
	p.AllowAttrs("srcset").Matching(regexp.MustCompile(`^` + asset + `(?:, ` + asset + `)*$`)).OnElements("img")
	p.AllowAttrs("sizes").Matching(regexp.MustCompile(`^\(max-width: 800px\) 100vw, 800px$`)).OnElements("img")
	p.AllowAttrs("data-media-id").Matching(regexp.MustCompile(`^[1-9][0-9]{0,18}$`)).OnElements("img")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(?:lazy|eager)$`)).OnElements("img")
	p.AllowAttrs("decoding").Matching(regexp.MustCompile(`^(?:async|sync|auto)$`)).OnElements("img")
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
	// Keep the value-checked inline styles the admin editor writes (alignment,
	// colours, fonts, indent, image and table layout); see pageStyleRules.
	allowPageStyles(p)
	// Keep "open in new tab": the editor gives every link target="_blank".
	// bluemonday adds rel="noopener" to such a link only while it forces
	// nofollow or noreferrer, which this policy does not, so SanitizePageHTML
	// adds it (ensureNoOpener) and the opened page cannot reach back through
	// window.opener.
	p.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
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

// SanitizePageHTML sanitizes rich-text page HTML with a conservative UGC policy
// that also keeps the editor's allowlisted inline styles (pageStyleRules).
func SanitizePageHTML(raw string) string {
	return ensureNoOpener(pageHTMLSanitizer.Sanitize(raw))
}

// NormalizePageHTMLForStorage applies the configured save-time sanitizer and
// reports removed markup, excluding harmless serialization changes. Disabled
// sanitization preserves the original bytes.
func NormalizePageHTMLForStorage(raw string, sanitize bool) (body string, removed []string) {
	if !sanitize {
		return raw, nil
	}
	body = SanitizePageHTML(raw)
	return body, RemovedMarkup(raw, body)
}

// ensureNoOpener gives every link that opens a new tab (target="_blank")
// rel="noopener", keeping any rel values the policy allowed. The input is
// sanitizer output; tags other than such links are copied byte for byte.
func ensureNoOpener(sanitized string) string {
	if !strings.Contains(sanitized, `target="_blank"`) {
		return sanitized
	}
	var out strings.Builder
	z := html.NewTokenizer(strings.NewReader(sanitized))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out.String()
		}
		raw := z.Raw()
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(raw)
			continue
		}
		tok := z.Token()
		if tok.Data != "a" || !hasAttr(tok.Attr, "target", "_blank") {
			out.Write(raw)
			continue
		}
		found := false
		for i, attr := range tok.Attr {
			if attr.Key != "rel" {
				continue
			}
			found = true
			if !slices.Contains(strings.Fields(attr.Val), "noopener") {
				tok.Attr[i].Val = strings.TrimSpace(attr.Val + " noopener")
			}
		}
		if !found {
			tok.Attr = append(tok.Attr, html.Attribute{Key: "rel", Val: "noopener"})
		}
		out.WriteString(tok.String())
	}
}

func hasAttr(attrs []html.Attribute, key, val string) bool {
	for _, attr := range attrs {
		if attr.Key == key && attr.Val == val {
			return true
		}
	}
	return false
}
