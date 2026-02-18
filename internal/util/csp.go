// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"html"
	"regexp"
	"strings"
)

var scriptTagRE = regexp.MustCompile(`(?i)<script\b[^>]*>`)

// CSPNonceAttr returns a nonce attribute string for use in HTML tags.
// Returns an empty string when nonce is empty.
func CSPNonceAttr(nonce string) string {
	trimmed := strings.TrimSpace(nonce)
	if trimmed == "" {
		return ""
	}
	return ` nonce="` + html.EscapeString(trimmed) + `"`
}

// AddNonceToScriptTags injects a CSP nonce attribute into all <script> tags
// that don't already have one.
func AddNonceToScriptTags(content, nonce string) string {
	attr := CSPNonceAttr(nonce)
	if attr == "" || content == "" {
		return content
	}
	return scriptTagRE.ReplaceAllStringFunc(content, func(tag string) string {
		lower := strings.ToLower(tag)
		if strings.Contains(lower, " nonce=") {
			return tag
		}
		idx := strings.Index(lower, "<script")
		if idx == -1 {
			return tag
		}
		return tag[:idx] + "<script" + attr + tag[idx+len("<script"):]
	})
}
