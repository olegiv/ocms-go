// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"fmt"
	"strings"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/seo"
)

// validateImportedMenuURLs refuses menu items whose URL uses a scheme menus do
// not allow, such as javascript: or data:. It applies the same rule the
// frontend applies before rendering a menu link, so relative links pass and a
// site's own export always imports again.
func validateImportedMenuURLs(data *ExportData, opts ImportOptions) []ImportError {
	if !opts.ImportMenus {
		return nil
	}
	var failures []ImportError
	var check func(menuSlug string, items []ExportMenuItem)
	check = func(menuSlug string, items []ExportMenuItem) {
		for _, item := range items {
			if item.URL != "" && !model.IsRenderableMenuURL(item.URL) {
				failures = append(failures, ImportError{Entity: "menu", ID: menuSlug,
					Message: fmt.Sprintf("item %q URL must be relative or use http, https, mailto or tel", item.Title)})
			}
			check(menuSlug, item.Children)
		}
	}
	for _, menu := range data.Menus {
		check(menu.Slug, menu.Items)
	}
	return failures
}

// normalizeImportedConfigValue applies the admin form's checks to the config
// values that hold URLs and returns the value to store. A value that fails
// them is skipped with a notice rather than failing the whole import.
func normalizeImportedConfigValue(key, value string) (string, error) {
	switch key {
	case model.ConfigKeySiteURL:
		if strings.TrimSpace(value) == "" {
			return "", nil
		}
		return seo.NormalizeFeedOrigin(value)
	case model.SiteLogoConfigKey:
		return model.NormalizeSiteLogoURL(value)
	}
	return value, nil
}
