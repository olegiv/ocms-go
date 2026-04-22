// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package markdown implements HTTP Accept: text/markdown content negotiation
// for the public site. It exposes helpers to detect a markdown-preferring
// client, convert a page's HTML body to Markdown, and write a well-formed
// text/markdown response.
//
// The feature is the "Markdown for Agents" capability exercised by the
// isitagentready.com scanner and described in Cloudflare's spec at
// https://developers.cloudflare.com/fundamentals/reference/markdown-for-agents/.
package markdown

import (
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// MaxHTMLBytes caps the size of HTML we are willing to convert to Markdown
// per request. Protects against CPU DoS from a single huge page body.
const MaxHTMLBytes = 2 * 1024 * 1024 // 2 MB

// ContentTypeMarkdown is the RFC 9842 media type for Markdown responses.
const ContentTypeMarkdown = "text/markdown; charset=utf-8"

// RecentPost is a summary used to render a bulleted list on the homepage
// markdown representation.
type RecentPost struct {
	Title       string
	URL         string // absolute URL
	PublishedAt *time.Time
	Excerpt     string
}

// Labels holds user-visible strings used in the markdown rendering. They are
// resolved from the i18n system at the call site; keeping them in a struct
// avoids coupling this package to the translator.
type Labels struct {
	RecentPosts  string // e.g. "Recent Posts"
	PublishedOn  string // e.g. "Published"
	Source       string // e.g. "Source"
}

// WantsMarkdown reports whether the request's Accept header indicates a
// preference for text/markdown over text/html. It treats a missing header
// and wildcard-only values (*/*, text/*) as HTML — so default browsers and
// curl without -H 'Accept:' continue to receive HTML.
//
// Parsing honors q-values per RFC 9110 §12.5.1; ties break in favor of HTML.
func WantsMarkdown(r *http.Request) bool {
	h := r.Header.Get("Accept")
	if h == "" {
		return false
	}
	mdQ, htmlQ, mdSeen := float64(-1), float64(-1), false
	for _, raw := range strings.Split(h, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		q := 1.0
		if qs, ok := params["q"]; ok {
			if parsed, perr := strconv.ParseFloat(qs, 64); perr == nil {
				q = parsed
			}
		}
		switch mediaType {
		case "text/markdown":
			mdSeen = true
			if q > mdQ {
				mdQ = q
			}
		case "text/html", "application/xhtml+xml":
			if q > htmlQ {
				htmlQ = q
			}
		}
	}
	// RFC 9110 §12.5.1: "A value of 'q=0' means 'not acceptable'." So
	// `Accept: text/markdown;q=0` must not cause us to serve markdown,
	// even when no HTML media type is listed. Require mdQ > 0 before
	// considering markdown as a candidate.
	if !mdSeen || mdQ <= 0 {
		return false
	}
	// Strict preference: markdown must beat HTML. If HTML is not listed at
	// all (htmlQ == -1) and markdown is, markdown wins.
	return mdQ > htmlQ
}

// WriteMarkdown writes body as a text/markdown response with Vary: Accept
// and an x-markdown-tokens header carrying a coarse whitespace token count.
// It is safe to call once per request; callers must not write additional
// body bytes after this function returns.
func WriteMarkdown(w http.ResponseWriter, body string) {
	h := w.Header()
	h.Set("Content-Type", ContentTypeMarkdown)
	appendVary(h, "Accept")
	h.Set("X-Markdown-Tokens", strconv.Itoa(tokenCount(body)))
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, body); err != nil {
		slog.Warn("short write on markdown response", "error", err)
	}
}

// AddVaryAccept adds Accept to the Vary header on any response, idempotently.
// HTML handlers call this so CDNs/reverse proxies keep the two
// representations cached under distinct keys.
func AddVaryAccept(w http.ResponseWriter) {
	appendVary(w.Header(), "Accept")
}

// PageToMarkdown renders a single page's HTML body as a Markdown document
// with an H1 title, optional excerpt blockquote, optional "Published" line,
// the converted body, and a trailing canonical Source link.
//
// Returns an error when the body exceeds MaxHTMLBytes or the underlying
// HTML parser fails. Callers should fall back to the HTML representation on
// error to avoid blanking the page.
func PageToMarkdown(title, excerpt, bodyHTML, canonical string, publishedAt *time.Time, labels Labels) (string, error) {
	if len(bodyHTML) > MaxHTMLBytes {
		return "", fmt.Errorf("page body exceeds %d bytes (got %d)", MaxHTMLBytes, len(bodyHTML))
	}

	mdBody, err := htmltomarkdown.ConvertString(bodyHTML)
	if err != nil {
		return "", fmt.Errorf("convert html to markdown: %w", err)
	}

	var b strings.Builder
	if title != "" {
		b.WriteString("# ")
		b.WriteString(sanitizeHeading(title))
		b.WriteString("\n\n")
	}
	if publishedAt != nil {
		label := labels.PublishedOn
		if label == "" {
			label = "Published"
		}
		fmt.Fprintf(&b, "*%s: %s*\n\n", label, publishedAt.Format("2006-01-02"))
	}
	if excerpt != "" {
		for _, line := range strings.Split(strings.TrimSpace(excerpt), "\n") {
			b.WriteString("> ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString(strings.TrimSpace(mdBody))
	b.WriteByte('\n')
	if canonical != "" {
		label := labels.Source
		if label == "" {
			label = "Source"
		}
		fmt.Fprintf(&b, "\n---\n\n[%s](%s)\n", label, canonical)
	}
	return b.String(), nil
}

// HomeToMarkdown renders the site homepage as Markdown: site title and
// description, then a bulleted list of recent posts with their dates.
func HomeToMarkdown(siteName, siteDescription, canonical string, recent []RecentPost, labels Labels) string {
	var b strings.Builder
	if siteName != "" {
		b.WriteString("# ")
		b.WriteString(sanitizeHeading(siteName))
		b.WriteString("\n\n")
	}
	if siteDescription != "" {
		b.WriteString(siteDescription)
		b.WriteString("\n\n")
	}

	heading := labels.RecentPosts
	if heading == "" {
		heading = "Recent Posts"
	}
	if len(recent) > 0 {
		b.WriteString("## ")
		b.WriteString(heading)
		b.WriteString("\n\n")
		for _, p := range recent {
			fmt.Fprintf(&b, "- [%s](%s)", p.Title, p.URL)
			if p.PublishedAt != nil {
				fmt.Fprintf(&b, " — %s", p.PublishedAt.Format("2006-01-02"))
			}
			b.WriteByte('\n')
			if p.Excerpt != "" {
				fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(strings.TrimSpace(p.Excerpt), "\n", " "))
			}
		}
		b.WriteByte('\n')
	}

	if canonical != "" {
		label := labels.Source
		if label == "" {
			label = "Source"
		}
		fmt.Fprintf(&b, "---\n\n[%s](%s)\n", label, canonical)
	}
	return b.String()
}

// appendVary adds value to Vary if not already present. Case-insensitive
// token comparison per RFC 9110 §12.5.5.
func appendVary(h http.Header, value string) {
	existing := h.Get("Vary")
	if existing == "" {
		h.Set("Vary", value)
		return
	}
	for _, tok := range strings.Split(existing, ",") {
		if strings.EqualFold(strings.TrimSpace(tok), value) {
			return
		}
	}
	h.Set("Vary", existing+", "+value)
}

// tokenCount returns the number of whitespace-separated tokens in s. Used
// for the x-markdown-tokens hint — a coarse analogue of "word count" /
// approximate LLM token budget without introducing a real tokenizer.
func tokenCount(s string) int {
	return len(strings.Fields(s))
}

// sanitizeHeading collapses newline and carriage-return characters to spaces
// so a stored title cannot break out of its heading context and inject
// additional Markdown structures (e.g., a title of "Foo\n\n## Injected"
// producing a real H2 further down). Admin-authored content is trusted, but
// the guard is cheap and closes a defense-in-depth gap.
func sanitizeHeading(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")
	return r.Replace(s)
}
