// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// SiteLogoConfigKey is the config row that holds the site logo. It is not a
// standard config field: the row exists only where an older install or an
// import created it, and /admin/config edits it when present.
const SiteLogoConfigKey = "site_logo"

// siteLogoDataURI matches a base64 data URI of an image type browsers display
// in <img>. An image loaded that way never runs script, SVG included.
var siteLogoDataURI = regexp.MustCompile(`(?i)^data:image/(?:png|jpe?g|gif|webp|avif|svg\+xml);base64,[A-Za-z0-9+/]+={0,2}$`)

// ErrInvalidSiteLogoURL reports a site logo value the site header cannot show.
var ErrInvalidSiteLogoURL = errors.New("site logo must be an http(s) URL, a site path starting with /, or a base64 image data URI")

// NormalizeSiteLogoURL trims a site logo value and checks that the site header
// can show it as an image: an http(s) URL, a site path starting with "/", or a
// base64 PNG, JPEG, GIF, WebP, AVIF or SVG data URI. An empty value means no
// logo and is valid.
func NormalizeSiteLogoURL(raw string) (string, error) {
	logo := strings.TrimSpace(raw)
	if logo == "" || siteLogoDataURI.MatchString(logo) {
		return logo, nil
	}
	parsed, err := url.Parse(logo)
	if err != nil {
		return "", ErrInvalidSiteLogoURL
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if parsed.Host != "" {
			return logo, nil
		}
	case "":
		if strings.HasPrefix(logo, "/") {
			return logo, nil
		}
	}
	return "", ErrInvalidSiteLogoURL
}
