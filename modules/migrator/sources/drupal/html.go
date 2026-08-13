// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/olegiv/ocms-go/internal/model"
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

	// Unresolved collects the UUIDs of <drupal-media> embeds that matched no
	// imported file, so the importer can report them. An embed that cannot be
	// resolved is deleted from the body, and deleting content without telling
	// anyone is how these went unnoticed in the first place.
	Unresolved []string
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
		mediaURL, ok := refs.ByUUID[uuid]
		if !ok {
			refs.Unresolved = append(refs.Unresolved, uuid)
			return ""
		}

		alt := attrs["alt"]
		if alt == "" {
			alt = refs.AltMap[mediaURL]
		}

		if refs.IsImg[mediaURL] {
			return fmt.Sprintf(`<img src="%s" alt="%s">`, html.EscapeString(mediaURL), html.EscapeString(alt))
		}
		label := alt
		if label == "" {
			label = mediaURL
		}
		return fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(mediaURL), html.EscapeString(label))
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

// stylePathPattern matches the leading segments of an image-style derivative
// path: "styles/<style>/<scheme>/<relpath>".
var stylePathPattern = regexp.MustCompile(`^styles/[^/]+/[^/]+/`)

// publicMediaURL builds the oCMS URL for an imported file.
//
// The filename is percent-encoded because Drupal filenames routinely carry
// spaces and non-ASCII characters, and bluemonday's URL policy rejects any src
// or href containing a literal space outright. Without the encoding the page
// sanitizer deletes the <img> that RewriteBody just produced, so the image is
// imported and then dropped from the body a moment later.
func publicMediaURL(fileUUID, filename string) string {
	return model.MediaURL(model.VariantOriginal, fileUUID, filename)
}

// normalizeFileRelPath turns a Drupal public-file URL path into the relative
// path stored in file_managed.uri.
//
// Three shapes have to collapse onto the same key. An image-style derivative
// ("styles/large/public/2026-01/a.jpg") is a generated thumbnail of
// "2026-01/a.jpg" and belongs to the same managed file. An "?itok=…" token is a
// derivative signature, not part of the path. And CKEditor percent-encodes
// spaces and non-ASCII while file_managed.uri stores them raw — which matters a
// great deal on a site with Cyrillic filenames.
//
// Unescaping happens last on purpose: doing it first would let a literal %2F
// manufacture path segments and defeat the styles trim above it.
func normalizeFileRelPath(rel string) string {
	if i := strings.IndexAny(rel, "?#"); i >= 0 {
		rel = rel[:i]
	}
	rel = stylePathPattern.ReplaceAllString(rel, "")
	if unescaped, err := url.PathUnescape(rel); err == nil {
		rel = unescaped
	}
	return rel
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
		head := body[:idx]
		rest := body[idx:]
		end := urlEnd(rest)
		candidate := rest[:end]
		relPath := strings.TrimPrefix(candidate, prefix)
		if newURL, ok := lookupByPath(refs, relPath); ok {
			// Drop the old site's origin along with the path. Rewriting only
			// the path turned "https://old.example/sites/default/files/a.jpg"
			// into "https://old.example/uploads/originals/…", so migrated pages
			// kept requesting a URL that does not exist on the old host.
			head = head[:len(head)-absoluteOriginLen(head)]
			out.WriteString(head)
			out.WriteString(newURL)
		} else {
			out.WriteString(head)
			out.WriteString(candidate)
		}
		body = rest[end:]
	}
	return out.String()
}

// absoluteOriginLen returns how many trailing bytes of head form a
// "scheme://authority" origin, or 0 when the matched path is already relative.
//
// The authority may not contain a slash or any character that would have ended
// the URL, which is what keeps this from swallowing unrelated text that merely
// happens to contain "://" earlier in the document.
func absoluteOriginLen(head string) int {
	const authorityStoppers = "/\"'<> \t\n"

	if sep := strings.LastIndex(head, "://"); sep >= 0 {
		authority := head[sep+3:]
		if authority != "" && !strings.ContainsAny(authority, authorityStoppers) {
			start := sep
			for start > 0 && isSchemeByte(head[start-1]) {
				start--
			}
			if start < sep { // there is a scheme in front of "://"
				return len(head) - start
			}
		}
		return 0
	}

	// Protocol-relative ("//old.example/sites/default/files/a.jpg"). Drupal
	// emits these when the source site was served over both schemes.
	if sep := strings.LastIndex(head, "//"); sep >= 0 {
		authority := head[sep+2:]
		if authority != "" && !strings.ContainsAny(authority, authorityStoppers) {
			return len(head) - sep
		}
	}
	return 0
}

// isSchemeByte reports whether b may appear in a URL scheme.
func isSchemeByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '+' || b == '-' || b == '.'
}

// lookupByPath resolves a Drupal relative file path to its new oCMS URL.
//
// ByPath is keyed on the raw file_managed.uri path, so normalization happens
// on the lookup side only. The raw path is tried first so an exact match always
// wins, then the normalized form, which is what lets a style derivative or a
// percent-encoded URL find the original file.
func lookupByPath(refs *MediaRefs, relPath string) (string, bool) {
	if newURL, ok := refs.ByPath[relPath]; ok {
		return newURL, true
	}
	if normalized := normalizeFileRelPath(relPath); normalized != relPath {
		if newURL, ok := refs.ByPath[normalized]; ok {
			return newURL, true
		}
	}
	return "", false
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
func ResolveLinkURI(uri string) (nodeID int64, linkURL string, err error) {
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
