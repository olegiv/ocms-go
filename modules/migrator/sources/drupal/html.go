// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// drupalMediaPattern matches the <drupal-media> and <drupal-entity> custom
// elements the CKEditor media embed button produces. These render as nothing
// outside Drupal, so they are rewritten to plain HTML before storage.
var drupalMediaPattern = regexp.MustCompile(`(?is)<drupal-(?:media|entity)\b[^>]*?/?>(?:\s*</drupal-(?:media|entity)>)?`)

// attrPattern extracts an HTML attribute value from a tag.
var attrPattern = regexp.MustCompile(`(?is)\b([a-z0-9-]+)\s*=\s*"([^"]*)"`)

// nodePathPattern matches Drupal's internal node path form.
var nodePathPattern = regexp.MustCompile(`^/?node/(\d+)/?$`)

// parseNodePath extracts a node ID from "/node/12", "node/12" or "/node/12/".
func parseNodePath(path string) (int64, bool) {
	m := nodePathPattern.FindStringSubmatch(strings.TrimSpace(path))
	if m == nil {
		return 0, false
	}
	nid, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return nid, true
}

// MediaRefs carries the lookups needed to rewrite a Drupal body: the old public
// file path to the new oCMS URL, and the Drupal file/media UUID to the same.
type MediaRefs struct {
	ByPath map[string]string // "/sites/default/files/a.jpg" -> "/uploads/originals/<uuid>/a.jpg"
	ByUUID map[string]string // Drupal file or media UUID    -> new URL
	AltMap map[string]string // new URL -> alt text
	IsImg  map[string]bool   // new URL -> true when the file is an image
}

// NewMediaRefs returns an empty, ready-to-use MediaRefs.
func NewMediaRefs() *MediaRefs {
	return &MediaRefs{
		ByPath: make(map[string]string),
		ByUUID: make(map[string]string),
		AltMap: make(map[string]string),
		IsImg:  make(map[string]bool),
	}
}

// RewriteBody converts a Drupal body into HTML that renders correctly in oCMS.
//
// The order matters: <drupal-media> elements are resolved first and file URLs
// second, because both steps must happen before sanitizing — the sanitizer
// strips unknown custom elements, so resolving after it would silently drop
// every embedded image. Sanitizing is the caller's final step.
func RewriteBody(body string, refs *MediaRefs) string {
	if body == "" {
		return ""
	}
	if refs != nil {
		body = replaceDrupalMedia(body, refs)
		body = rewriteFileURLs(body, refs)
	}
	return body
}

// replaceDrupalMedia turns <drupal-media data-entity-uuid="…"> into a plain
// <img> when the referenced file is an image, or an <a> download link when it
// is not. Unresolvable references are dropped rather than left as dead custom
// elements.
func replaceDrupalMedia(body string, refs *MediaRefs) string {
	return drupalMediaPattern.ReplaceAllStringFunc(body, func(tag string) string {
		attrs := parseAttrs(tag)
		uuid := attrs["data-entity-uuid"]
		if uuid == "" {
			return ""
		}
		url, ok := refs.ByUUID[uuid]
		if !ok {
			return ""
		}

		alt := attrs["alt"]
		if alt == "" {
			alt = refs.AltMap[url]
		}

		if refs.IsImg[url] {
			return fmt.Sprintf(`<img src="%s" alt="%s">`, html.EscapeString(url), html.EscapeString(alt))
		}
		label := alt
		if label == "" {
			label = url
		}
		return fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(url), html.EscapeString(label))
	})
}

// parseAttrs extracts double-quoted attributes from a tag.
func parseAttrs(tag string) map[string]string {
	attrs := make(map[string]string)
	for _, m := range attrPattern.FindAllStringSubmatch(tag, -1) {
		attrs[strings.ToLower(m[1])] = html.UnescapeString(m[2])
	}
	return attrs
}

// drupalFilePrefixes are the public URL forms Drupal serves managed files under.
var drupalFilePrefixes = []string{
	"/sites/default/files/",
	"/system/files/",
}

// rewriteFileURLs replaces Drupal public-file URLs with their new oCMS URLs.
//
// Lookups go through the relative path rather than a blind string replace of
// every map key, so a file named "a.jpg" cannot corrupt a longer path that
// happens to contain it.
func rewriteFileURLs(body string, refs *MediaRefs) string {
	if len(refs.ByPath) == 0 {
		return body
	}
	for _, prefix := range drupalFilePrefixes {
		body = rewritePrefixedURLs(body, prefix, refs)
	}
	return body
}

// rewritePrefixedURLs rewrites every occurrence of prefix+relpath in body.
func rewritePrefixedURLs(body, prefix string, refs *MediaRefs) string {
	var out strings.Builder
	for {
		idx := strings.Index(body, prefix)
		if idx < 0 {
			out.WriteString(body)
			break
		}
		out.WriteString(body[:idx])
		rest := body[idx:]
		end := urlEnd(rest)
		candidate := rest[:end]
		relPath := strings.TrimPrefix(candidate, prefix)
		if newURL, ok := refs.ByPath[relPath]; ok {
			out.WriteString(newURL)
		} else {
			out.WriteString(candidate)
		}
		body = rest[end:]
	}
	return out.String()
}

// urlEnd returns the index just past the end of a URL starting at s[0].
func urlEnd(s string) int {
	for i, r := range s {
		switch r {
		case '"', '\'', '<', '>', ' ', '\t', '\n', '\r', ')', ']':
			return i
		}
	}
	return len(s)
}

// ResolveLinkURI maps a Drupal menu link URI onto an oCMS menu item target.
//
// Drupal stores menu targets as URIs: "entity:node/12" and "internal:/node/12"
// point at content, "internal:/some/path" at an arbitrary local path, and
// "http(s)://…" at an external site. "route:<name>" refers to a Drupal routing
// entry that has no oCMS equivalent, so it is reported rather than guessed at.
func ResolveLinkURI(uri string) (nodeID int64, url string, err error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return 0, "", fmt.Errorf("empty link URI")
	}

	switch {
	case strings.HasPrefix(uri, "entity:"):
		path := strings.TrimPrefix(uri, "entity:")
		if nid, ok := parseNodePath(path); ok {
			return nid, "", nil
		}
		return 0, "", fmt.Errorf("unsupported entity link %q", uri)

	case strings.HasPrefix(uri, "internal:"):
		path := strings.TrimPrefix(uri, "internal:")
		if nid, ok := parseNodePath(path); ok {
			return nid, "", nil
		}
		if path == "" || path == "/" {
			return 0, "/", nil
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return 0, path, nil

	case strings.HasPrefix(uri, "http://"), strings.HasPrefix(uri, "https://"):
		return 0, uri, nil

	case strings.HasPrefix(uri, "route:"):
		return 0, "", fmt.Errorf("route link %q has no oCMS equivalent", uri)

	case strings.HasPrefix(uri, "base:"):
		path := strings.TrimPrefix(uri, "base:")
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return 0, path, nil
	}

	return 0, "", fmt.Errorf("unsupported link URI %q", uri)
}
