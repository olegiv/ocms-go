// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package seo provides SEO utilities for building meta tags, structured data, and sitemaps.
package seo

import (
	"encoding/xml"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/util"
)

// XMLNamespace is the sitemap XML namespace.
const XMLNamespace = "http://www.sitemaps.org/schemas/sitemap/0.9"

// ChangeFreq represents the change frequency of a URL.
type ChangeFreq string

// Valid change frequency values.
const (
	ChangeFreqDaily  ChangeFreq = "daily"
	ChangeFreqWeekly ChangeFreq = "weekly"
)

// SitemapURL represents a single URL entry in the sitemap.
type SitemapURL struct {
	Loc        string     `xml:"loc"`
	LastMod    string     `xml:"lastmod,omitempty"`
	ChangeFreq ChangeFreq `xml:"changefreq,omitempty"`
	Priority   string     `xml:"priority,omitempty"`
}

// Sitemap represents the complete sitemap document.
type Sitemap struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []SitemapURL `xml:"url"`
}

// SitemapPage contains data needed to add a page to the sitemap.
type SitemapPage struct {
	Slug         string
	LanguageCode string
	IsDefault    bool
	UpdatedAt    time.Time
}

// SitemapCategory contains data needed to add a category to the sitemap.
type SitemapCategory struct {
	Slug         string
	LanguageCode string
	IsDefault    bool
	UpdatedAt    time.Time
}

// SitemapTag contains data needed to add a tag to the sitemap.
type SitemapTag struct {
	Slug         string
	LanguageCode string
	IsDefault    bool
	UpdatedAt    time.Time
}

// SitemapBuilder builds sitemap XML from various content types.
type SitemapBuilder struct {
	siteURL string
	urls    []SitemapURL
}

// NewSitemapBuilder creates a new sitemap builder.
func NewSitemapBuilder(siteURL string) *SitemapBuilder {
	return &SitemapBuilder{
		siteURL: strings.TrimRight(siteURL, "/"),
		urls:    make([]SitemapURL, 0),
	}
}

// AddHomepage adds the homepage to the sitemap.
func (b *SitemapBuilder) AddHomepage() {
	b.urls = append(b.urls, SitemapURL{
		Loc:        b.siteURL,
		ChangeFreq: ChangeFreqDaily,
		Priority:   "1.0",
	})
}

// AddLanguageHomepage adds the canonical homepage for one routable language.
// The default remains the bare site URL; non-default homepages use the
// slashless language root served by the frontend router.
func (b *SitemapBuilder) AddLanguageHomepage(languageCode string, isDefault bool) {
	if !util.IsValidLangCode(languageCode) || util.IsReservedLanguageCode(languageCode) {
		return
	}
	if isDefault {
		b.AddHomepage()
		return
	}
	b.urls = append(b.urls, SitemapURL{
		Loc:        b.siteURL + "/" + languageCode,
		ChangeFreq: ChangeFreqDaily,
		Priority:   "1.0",
	})
}

// addURL adds a URL entry to the sitemap with the given parameters.
func (b *SitemapBuilder) addURL(path string, priority string, updatedAt time.Time) {
	url := SitemapURL{
		Loc:        b.siteURL + path,
		ChangeFreq: ChangeFreqWeekly,
		Priority:   priority,
	}
	if !updatedAt.IsZero() {
		url.LastMod = updatedAt.Format(time.RFC3339)
	}
	b.urls = append(b.urls, url)
}

// canonicalLanguagePath returns the public canonical path for content in a
// language that was already verified as active by the sitemap store queries.
// Invalid and reserved codes never receive public URLs, including a legacy
// misconfigured default. A safe default language does not need a prefix.
func canonicalLanguagePath(path, languageCode string, isDefault bool) (string, bool) {
	if !util.IsValidLangCode(languageCode) || util.IsReservedLanguageCode(languageCode) {
		return "", false
	}
	if isDefault {
		return path, true
	}
	return "/" + languageCode + path, true
}

// AddPage adds a page to the sitemap.
func (b *SitemapBuilder) AddPage(page SitemapPage) {
	path, ok := canonicalLanguagePath("/"+page.Slug, page.LanguageCode, page.IsDefault)
	if !ok {
		return
	}
	b.addURL(path, "0.8", page.UpdatedAt)
}

// AddPages adds multiple pages to the sitemap.
func (b *SitemapBuilder) AddPages(pages []SitemapPage) {
	for _, p := range pages {
		b.AddPage(p)
	}
}

// AddCategory adds a category archive page to the sitemap.
func (b *SitemapBuilder) AddCategory(cat SitemapCategory) {
	path, ok := canonicalLanguagePath("/category/"+cat.Slug, cat.LanguageCode, cat.IsDefault)
	if !ok {
		return
	}
	b.addURL(path, "0.6", cat.UpdatedAt)
}

// AddCategories adds multiple categories to the sitemap.
func (b *SitemapBuilder) AddCategories(categories []SitemapCategory) {
	for _, c := range categories {
		b.AddCategory(c)
	}
}

// AddTag adds a tag archive page to the sitemap.
func (b *SitemapBuilder) AddTag(tag SitemapTag) {
	path, ok := canonicalLanguagePath("/tag/"+tag.Slug, tag.LanguageCode, tag.IsDefault)
	if !ok {
		return
	}
	b.addURL(path, "0.5", tag.UpdatedAt)
}

// AddTags adds multiple tags to the sitemap.
func (b *SitemapBuilder) AddTags(tags []SitemapTag) {
	for _, t := range tags {
		b.AddTag(t)
	}
}

// Build generates the sitemap XML.
func (b *SitemapBuilder) Build() ([]byte, error) {
	sitemap := Sitemap{
		XMLNS: XMLNamespace,
		URLs:  b.urls,
	}

	// Add XML header
	output := []byte(xml.Header)
	xmlBytes, err := xml.MarshalIndent(sitemap, "", "  ")
	if err != nil {
		return nil, err
	}

	return append(output, xmlBytes...), nil
}
