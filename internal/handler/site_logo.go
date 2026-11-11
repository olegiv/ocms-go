// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"github.com/a-h/templ"

	"github.com/olegiv/ocms-go/internal/model"
)

// siteLogoURL returns the configured site logo for the header's <img src>, or
// "" when no logo is set or model.NormalizeSiteLogoURL rejects the value, so
// the header shows the site name instead. The value is marked trusted only
// after that check, because templ's URL filter rejects the data: URIs a logo
// may use.
func siteLogoURL(raw string) templ.SafeURL {
	logo, err := model.NormalizeSiteLogoURL(raw)
	if err != nil {
		return ""
	}
	return templ.SafeURL(logo)
}
