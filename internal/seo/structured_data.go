// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"encoding/json"
	"html/template"
	"net/url"
	"strings"
)

// Breadcrumb is one visible navigation step and its canonical destination.
type Breadcrumb struct {
	Name string
	URL  string
}

// BuildWebSiteSchema describes the site at its configured root origin, including
// when rendered on a language-prefixed homepage. It never advertises SearchAction.
func BuildWebSiteSchema(site *SiteConfig) template.JS {
	if site == nil || strings.TrimSpace(site.SiteName) == "" {
		return ""
	}
	origin, err := NormalizeFeedOrigin(site.SiteURL)
	if err != nil {
		return ""
	}
	return marshalJSONLD(WebSiteSchema{
		Context:     "https://schema.org",
		Type:        "WebSite",
		Name:        site.SiteName,
		URL:         origin + "/",
		Description: site.SiteDescription,
	})
}

// BuildBreadcrumbSchema serializes the visible trail with absolute HTTP(S)
// destinations and consecutive positions. An invalid trail emits no markup.
func BuildBreadcrumbSchema(crumbs []Breadcrumb, siteURL string) template.JS {
	origin, err := NormalizeFeedOrigin(siteURL)
	if err != nil || len(crumbs) < 2 {
		return ""
	}
	schema := BreadcrumbSchema{Context: "https://schema.org", Type: "BreadcrumbList"}
	for i, crumb := range crumbs {
		if strings.TrimSpace(crumb.Name) == "" || crumb.URL == "" {
			return ""
		}
		destination, err := url.Parse(crumb.URL)
		if err != nil || destination.User != nil || destination.Fragment != "" {
			return ""
		}
		if !destination.IsAbs() {
			if destination.Host != "" || !strings.HasPrefix(crumb.URL, "/") {
				return ""
			}
			absolute, _ := url.Parse(origin)
			destination = absolute.ResolveReference(destination)
		}
		if (destination.Scheme != "http" && destination.Scheme != "https") || destination.Hostname() == "" {
			return ""
		}
		schema.ItemList = append(schema.ItemList, BreadcrumbItem{
			Type: "ListItem", Position: i + 1, Name: crumb.Name, Item: destination.String(),
		})
	}
	return marshalJSONLD(schema)
}

// CombineJSONLD keeps one schema as an object and combines multiple schemas in
// a JSON-LD array for the existing theme script block. Inputs must be valid JSON.
func CombineJSONLD(schemas ...template.JS) template.JS {
	var values []json.RawMessage
	for _, schema := range schemas {
		if schema == "" {
			continue
		}
		if !json.Valid([]byte(schema)) {
			return ""
		}
		values = append(values, json.RawMessage(schema))
	}
	switch len(values) {
	case 0:
		return ""
	case 1:
		return marshalJSONLD(values[0])
	default:
		return marshalJSONLD(values)
	}
}
