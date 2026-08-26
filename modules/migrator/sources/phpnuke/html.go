// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

// summaryLimit bounds the plain-text teaser derived from a story's hometext.
const summaryLimit = 300

// assetRef is one local file reference found in imported HTML.
//
// Raw and Path are kept apart because the body must be rewritten using the
// exact attribute text it contains — the same file is written as both
// "tourism/x.jpg" and "/tourism/x.jpg" across a PHP-Nuke site — while the file
// is opened, and imported, only once per normalized path.
type assetRef struct {
	Raw  string // exactly as it appeared in the source attribute
	Path string // normalized, root-relative, slash-separated
}

// assembleStoryBody joins the two halves of a PHP-Nuke article.
//
// hometext is the teaser rendered on the front page and bodytext is the
// remainder behind "read more". Concatenating them is what makes the article
// whole; importing either alone silently truncates most of the archive.
func assembleStoryBody(s *Story) string {
	home := strings.TrimSpace(shared.NullString(s.HomeText))
	body := strings.TrimSpace(s.BodyText)
	switch {
	case home == "":
		return body
	case body == "":
		return home
	default:
		return home + "\n\n" + body
	}
}

// assembleStaticPageBody joins the header, text, footer, and signature columns
// PHP-Nuke renders in sequence for a static page.
func assembleStaticPageBody(p *StaticPage) string {
	parts := make([]string, 0, 4)
	for _, part := range []string{p.Header, p.Text, p.Footer, p.Signature} {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n")
}

// buildEncyclopediaBody renders an encyclopedia and its terms as one page.
//
// PHP-Nuke serves each term through its own query-string URL, which has no
// oCMS equivalent. Rendering the terms as a definition list inside the parent
// entry keeps the content and its ordering while collapsing hundreds of
// unreachable URLs into one readable page.
func buildEncyclopediaBody(entry *EncyclopediaEntry, terms []EncyclopediaTerm) string {
	var b strings.Builder
	if description := strings.TrimSpace(entry.Description); description != "" {
		b.WriteString(description)
		b.WriteString("\n\n")
	}
	if len(terms) == 0 {
		return strings.TrimSpace(b.String())
	}
	b.WriteString("<dl>\n")
	for i := range terms {
		title := strings.TrimSpace(terms[i].Title)
		text := strings.TrimSpace(terms[i].Text)
		if title == "" && text == "" {
			continue
		}
		b.WriteString("<dt>")
		b.WriteString(html.EscapeString(title))
		b.WriteString("</dt>\n<dd>")
		// Term bodies are stored as HTML on the source site, so they are
		// emitted unescaped and left for the sanitizer to police.
		b.WriteString(text)
		b.WriteString("</dd>\n")
	}
	b.WriteString("</dl>")
	return b.String()
}

// deriveSummary reduces HTML to a short plain-text teaser.
func deriveSummary(fragment string) string {
	text := textContent(fragment)
	if text == "" {
		return ""
	}
	if utf8.RuneCountInString(text) <= summaryLimit {
		return text
	}
	// Cut on a rune boundary, then back off to the last word break so the
	// teaser does not end mid-word.
	cut := len(text)
	runes := 0
	for index := range text {
		if runes == summaryLimit {
			cut = index
			break
		}
		runes++
	}
	truncated := text[:cut]
	if idx := strings.LastIndex(truncated, " "); idx > 0 {
		truncated = truncated[:idx]
	}
	return strings.TrimRight(truncated, " ,;:-") + "…"
}

// textContent strips markup and collapses whitespace.
func textContent(fragment string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(fragment))
	var b strings.Builder
	skip := 0
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case html.StartTagToken:
			if name, _ := tokenizer.TagName(); isNonRenderedTag(string(name)) {
				skip++
			}
		case html.EndTagToken:
			if name, _ := tokenizer.TagName(); isNonRenderedTag(string(name)) && skip > 0 {
				skip--
			}
		case html.TextToken:
			if skip == 0 {
				b.Write(tokenizer.Text())
				b.WriteByte(' ')
			}
		}
	}
}

// isNonRenderedTag reports whether a tag's text content is invisible to a reader.
func isNonRenderedTag(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "head", "title":
		return true
	default:
		return false
	}
}

// extractAssetRefs finds every local file reference in imported HTML.
//
// Only paths that resolve to an importable MIME type are returned, so the
// walk yields the handful of images an article actually uses rather than every
// link on the page. Absolute URLs, protocol-relative URLs, and anything
// carrying a query string are left untouched: they either point off-site or
// address a PHP script rather than a file on disk.
func extractAssetRefs(fragment string) []assetRef {
	tokenizer := html.NewTokenizer(strings.NewReader(fragment))
	seen := make(map[string]assetRef)
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return sortedRefs(seen)
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokenizer.TagName()
			attrName := attributeForTag(string(name))
			if attrName == "" || !hasAttr {
				continue
			}
			for hasAttr {
				var key, value []byte
				key, value, hasAttr = tokenizer.TagAttr()
				if string(key) != attrName {
					continue
				}
				raw := string(value)
				if path, ok := normalizeAssetPath(raw); ok {
					seen[raw] = assetRef{Raw: raw, Path: path}
				}
			}
		}
	}
}

// attributeForTag returns the attribute that carries a file reference for a
// given tag, or "" when the tag cannot reference one.
func attributeForTag(name string) string {
	switch strings.ToLower(name) {
	case "img":
		return "src"
	case "a":
		return "href"
	default:
		return ""
	}
}

// normalizeAssetPath converts a source attribute value into a root-relative
// path, reporting whether it names an importable local file.
func normalizeAssetPath(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	// Off-site, protocol-relative, script-generated, and in-page references are
	// not files this importer can resolve beneath the source root.
	if strings.HasPrefix(value, "//") || strings.ContainsAny(value, "?#") {
		return "", false
	}
	// A colon before the first slash is a URL scheme ("http:", "data:",
	// "mailto:"), not a directory name.
	if head, _, _ := strings.Cut(value, "/"); strings.Contains(head, ":") {
		return "", false
	}
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		return "", false
	}
	// Reject traversal outright rather than cleaning it: a path that needs
	// normalizing to stay inside the root is not one this importer should
	// silently accept.
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	mimeType := shared.MimeTypeFromExt(value)
	if mimeType == "" || !shared.IsAllowedMediaMime(mimeType) {
		return "", false
	}
	return value, true
}

// sortedRefs returns the collected references in a stable order so that an
// import processes files deterministically across runs.
func sortedRefs(seen map[string]assetRef) []assetRef {
	refs := make([]assetRef, 0, len(seen))
	for _, ref := range seen {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Path != refs[j].Path {
			return refs[i].Path < refs[j].Path
		}
		return refs[i].Raw < refs[j].Raw
	})
	return refs
}
