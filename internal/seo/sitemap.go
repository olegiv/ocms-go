// Package seo provides SEO utilities for building meta tags, structured data, and sitemaps.
package seo

import (
	"encoding/xml"
	"time"
)

// XMLNamespace is the sitemap XML namespace.
const XMLNamespace = "http://www.sitemaps.org/schemas/sitemap/0.9"

// ChangeFreq represents the change frequency of a URL.
type ChangeFreq string

// Valid change frequency values.
const (
	ChangeFreqAlways  ChangeFreq = "always"
	ChangeFreqHourly  ChangeFreq = "hourly"
	ChangeFreqDaily   ChangeFreq = "daily"
	ChangeFreqWeekly  ChangeFreq = "weekly"
	ChangeFreqMonthly ChangeFreq = "monthly"
	ChangeFreqYearly  ChangeFreq = "yearly"
	ChangeFreqNever   ChangeFreq = "never"
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
	Slug      string
	UpdatedAt time.Time
}

// SitemapCategory contains data needed to add a category to the sitemap.
type SitemapCategory struct {
	Slug      string
	UpdatedAt time.Time
}

// SitemapTag contains data needed to add a tag to the sitemap.
type SitemapTag struct {
	Slug      string
	UpdatedAt time.Time
}

// SitemapBuilder builds sitemap XML from various content types.
type SitemapBuilder struct {
	siteURL string
	urls    []SitemapURL
}

// NewSitemapBuilder creates a new sitemap builder.
func NewSitemapBuilder(siteURL string) *SitemapBuilder {
	return &SitemapBuilder{
		siteURL: siteURL,
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

// AddPage adds a page to the sitemap.
func (b *SitemapBuilder) AddPage(page SitemapPage) {
	url := SitemapURL{
		Loc:        b.siteURL + "/" + page.Slug,
		ChangeFreq: ChangeFreqWeekly,
		Priority:   "0.8",
	}
	if !page.UpdatedAt.IsZero() {
		url.LastMod = page.UpdatedAt.Format(time.RFC3339)
	}
	b.urls = append(b.urls, url)
}

// AddPages adds multiple pages to the sitemap.
func (b *SitemapBuilder) AddPages(pages []SitemapPage) {
	for _, p := range pages {
		b.AddPage(p)
	}
}

// AddCategory adds a category archive page to the sitemap.
func (b *SitemapBuilder) AddCategory(cat SitemapCategory) {
	url := SitemapURL{
		Loc:        b.siteURL + "/category/" + cat.Slug,
		ChangeFreq: ChangeFreqWeekly,
		Priority:   "0.6",
	}
	if !cat.UpdatedAt.IsZero() {
		url.LastMod = cat.UpdatedAt.Format(time.RFC3339)
	}
	b.urls = append(b.urls, url)
}

// AddCategories adds multiple categories to the sitemap.
func (b *SitemapBuilder) AddCategories(categories []SitemapCategory) {
	for _, c := range categories {
		b.AddCategory(c)
	}
}

// AddTag adds a tag archive page to the sitemap.
func (b *SitemapBuilder) AddTag(tag SitemapTag) {
	url := SitemapURL{
		Loc:        b.siteURL + "/tag/" + tag.Slug,
		ChangeFreq: ChangeFreqWeekly,
		Priority:   "0.5",
	}
	if !tag.UpdatedAt.IsZero() {
		url.LastMod = tag.UpdatedAt.Format(time.RFC3339)
	}
	b.urls = append(b.urls, url)
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

// GenerateSitemap is a convenience function to generate a sitemap from content.
func GenerateSitemap(siteURL string, pages []SitemapPage, categories []SitemapCategory, tags []SitemapTag) ([]byte, error) {
	builder := NewSitemapBuilder(siteURL)
	builder.AddHomepage()
	builder.AddPages(pages)
	builder.AddCategories(categories)
	builder.AddTags(tags)
	return builder.Build()
}
