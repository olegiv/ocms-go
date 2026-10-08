// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"html/template"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/seo"
)

// FrontendBreadcrumb is shared by the visible navigation and its JSON-LD.
// The active item's URL is metadata only; templates render its label as text.
type FrontendBreadcrumb struct {
	Label  string
	URL    string
	Active bool
}

func breadcrumbTranslation(lang, key, fallback string) string {
	if translated := i18n.T(lang, key); translated != key {
		return translated
	}
	return fallback
}

func (data *BaseTemplateData) setBreadcrumbs(name, path string, post bool) {
	data.BreadcrumbLabel = breadcrumbTranslation(data.LangCode, "breadcrumb.aria_label", "Breadcrumb")
	data.Breadcrumbs = []FrontendBreadcrumb{{
		Label: breadcrumbTranslation(data.LangCode, "frontend.home", "Home"), URL: data.HomeURL,
	}}
	if post {
		data.Breadcrumbs = append(data.Breadcrumbs, FrontendBreadcrumb{
			Label: breadcrumbTranslation(data.LangCode, "frontend.blog", "Blog"), URL: data.LangPrefix + "/blog",
		})
	}
	if data.Canonical != "" {
		path = data.Canonical
	}
	data.Breadcrumbs = append(data.Breadcrumbs, FrontendBreadcrumb{Label: name, URL: path, Active: true})
}

func (data *BaseTemplateData) breadcrumbSchema() template.JS {
	crumbs := make([]seo.Breadcrumb, 0, len(data.Breadcrumbs))
	for _, crumb := range data.Breadcrumbs {
		crumbs = append(crumbs, seo.Breadcrumb{Name: crumb.Label, URL: crumb.URL})
	}
	return seo.BuildBreadcrumbSchema(crumbs, data.SiteURL)
}
