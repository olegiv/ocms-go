// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"regexp"
	"strings"

	"github.com/a-h/templ"
)

// siteLogoImageDataURI matches a base64 raster image data URI. Unlike SVG or
// HTML data URIs, a browser never runs script from one of these.
var siteLogoImageDataURI = regexp.MustCompile(`^data:image/(?:png|jpeg|gif|webp|avif);base64,[A-Za-z0-9+/]+={0,2}$`)

// siteLogoURL returns the configured site logo as a URL the frontend layout
// may render, or "" when the value would not display as an image. templ
// blocks every scheme except http(s), mailto, tel and ftp, so a raster data
// URI, which a logo may use, is marked trusted here after a strict match.
func siteLogoURL(raw string) templ.SafeURL {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if siteLogoImageDataURI.MatchString(raw) {
		return templ.SafeURL(raw)
	}
	if logo := templ.URL(raw); logo != templ.FailedSanitizationURL {
		return logo
	}
	return ""
}
