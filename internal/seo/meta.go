// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package seo provides SEO utilities for building meta tags and structured data.
package seo

import (
	"encoding/json"
	"html/template"
	"strings"
	"time"
)

// Meta holds all SEO meta tag data for a page.
type Meta struct {
	Title         string // Page title (for <title> tag)
	Description   string // Meta description
	Keywords      string // Meta keywords
	Canonical     string // Canonical URL
	OGTitle       string // Open Graph title
	OGDescription string // Open Graph description
	OGImage       string // Open Graph image URL (absolute)
	OGType        string // Open Graph type (website, article)
	OGSiteName    string // Open Graph site name
	OGURL         string // Open Graph URL
	Robots        string // Robots directive (index,follow / noindex,nofollow)
	TwitterCard   string // Twitter card type
	TwitterSite   string // Twitter @username
}

// PageData contains page information for building meta tags.
type PageData struct {
	Title           string
	Body            string
	Slug            string
	MetaTitle       string
	MetaDescription string
	MetaKeywords    string
	OGImageURL      string
	FeaturedImage   string
	NoIndex         bool
	NoFollow        bool
	CanonicalURL    string
	PublishedAt     *time.Time
	AuthorName      string
}

// SiteConfig contains site-wide settings for SEO.
type SiteConfig struct {
	SiteName        string
	SiteURL         string
	SiteDescription string
	DefaultOGImage  string
	TwitterHandle   string
}

// BuildMeta creates a Meta struct from page and site data with proper fallbacks.
func BuildMeta(page *PageData, site *SiteConfig) *Meta {
	meta := &Meta{
		OGType:      "website",
		TwitterCard: "summary_large_image",
		OGSiteName:  site.SiteName,
		TwitterSite: site.TwitterHandle,
	}

	if page != nil {
		meta.OGType = "article"

		// Title: meta_title → page title + site name
		if page.MetaTitle != "" {
			meta.Title = page.MetaTitle
			meta.OGTitle = page.MetaTitle
		} else if page.Title != "" {
			meta.Title = page.Title
			meta.OGTitle = page.Title
		}

		// Description: meta_description → truncated body
		if page.MetaDescription != "" {
			meta.Description = page.MetaDescription
			meta.OGDescription = page.MetaDescription
		} else if page.Body != "" {
			meta.Description = truncateText(stripHTML(page.Body), 160)
			meta.OGDescription = meta.Description
		}

		// Keywords
		meta.Keywords = page.MetaKeywords

		// OG Image: og_image → featured_image → site default
		if page.OGImageURL != "" {
			meta.OGImage = makeAbsoluteURL(page.OGImageURL, site.SiteURL)
		} else if page.FeaturedImage != "" {
			meta.OGImage = makeAbsoluteURL(page.FeaturedImage, site.SiteURL)
		} else if site.DefaultOGImage != "" {
			meta.OGImage = makeAbsoluteURL(site.DefaultOGImage, site.SiteURL)
		}

		// Canonical URL
		if page.CanonicalURL != "" {
			meta.Canonical = page.CanonicalURL
		} else if page.Slug != "" {
			meta.Canonical = site.SiteURL + "/" + page.Slug
		}
		meta.OGURL = meta.Canonical

		// Robots directive
		meta.Robots = buildRobotsDirective(page.NoIndex, page.NoFollow)
	} else {
		// Homepage defaults
		meta.Title = site.SiteName
		meta.OGTitle = site.SiteName
		meta.Description = site.SiteDescription
		meta.OGDescription = site.SiteDescription
		meta.Canonical = site.SiteURL
		meta.OGURL = site.SiteURL
		meta.Robots = "index,follow"

		if site.DefaultOGImage != "" {
			meta.OGImage = makeAbsoluteURL(site.DefaultOGImage, site.SiteURL)
		}
	}

	return meta
}

// buildRobotsDirective creates the robots meta content from noindex/nofollow flags.
func buildRobotsDirective(noIndex, noFollow bool) string {
	var parts []string

	if noIndex {
		parts = append(parts, "noindex")
	} else {
		parts = append(parts, "index")
	}

	if noFollow {
		parts = append(parts, "nofollow")
	} else {
		parts = append(parts, "follow")
	}

	return strings.Join(parts, ",")
}

// ArticleSchema represents JSON-LD Article structured data.
type ArticleSchema struct {
	Context          string        `json:"@context"`
	Type             string        `json:"@type"`
	Headline         string        `json:"headline"`
	Description      string        `json:"description,omitempty"`
	Image            string        `json:"image,omitempty"`
	DatePublished    string        `json:"datePublished,omitempty"`
	DateModified     string        `json:"dateModified,omitempty"`
	Author           *PersonSchema `json:"author,omitempty"`
	Publisher        *OrgSchema    `json:"publisher,omitempty"`
	MainEntityOfPage string        `json:"mainEntityOfPage,omitempty"`
}

// PersonSchema represents JSON-LD Person structured data.
type PersonSchema struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

// OrgSchema represents JSON-LD Organization structured data.
type OrgSchema struct {
	Type string       `json:"@type"`
	Name string       `json:"name"`
	Logo *ImageSchema `json:"logo,omitempty"`
}

// ImageSchema represents JSON-LD ImageObject structured data.
type ImageSchema struct {
	Type string `json:"@type"`
	URL  string `json:"url"`
}

// BreadcrumbSchema represents JSON-LD BreadcrumbList structured data.
type BreadcrumbSchema struct {
	Context  string           `json:"@context"`
	Type     string           `json:"@type"`
	ItemList []BreadcrumbItem `json:"itemListElement"`
}

// BreadcrumbItem represents a single breadcrumb item.
type BreadcrumbItem struct {
	Type     string `json:"@type"`
	Position int    `json:"position"`
	Name     string `json:"name"`
	Item     string `json:"item,omitempty"`
}

// WebSiteSchema represents JSON-LD WebSite structured data for homepage.
type WebSiteSchema struct {
	Context      string        `json:"@context"`
	Type         string        `json:"@type"`
	Name         string        `json:"name"`
	URL          string        `json:"url"`
	Description  string        `json:"description,omitempty"`
	Publisher    *OrgSchema    `json:"publisher,omitempty"`
	SearchAction *SearchAction `json:"potentialAction,omitempty"`
}

// SearchAction represents JSON-LD SearchAction for site search.
type SearchAction struct {
	Type       string `json:"@type"`
	Target     string `json:"target"`
	QueryInput string `json:"query-input"`
}

// BuildArticleSchema creates JSON-LD Article structured data for a page.
func BuildArticleSchema(page *PageData, site *SiteConfig, modifiedAt time.Time) template.JS {
	if page == nil {
		return ""
	}

	article := ArticleSchema{
		Context:          "https://schema.org",
		Type:             "Article",
		Headline:         page.Title,
		Description:      page.MetaDescription,
		MainEntityOfPage: site.SiteURL + "/" + page.Slug,
	}

	// Image
	if page.OGImageURL != "" {
		article.Image = makeAbsoluteURL(page.OGImageURL, site.SiteURL)
	} else if page.FeaturedImage != "" {
		article.Image = makeAbsoluteURL(page.FeaturedImage, site.SiteURL)
	}

	// Dates
	if page.PublishedAt != nil {
		article.DatePublished = page.PublishedAt.Format(time.RFC3339)
	}
	if !modifiedAt.IsZero() {
		article.DateModified = modifiedAt.Format(time.RFC3339)
	}

	// Author
	if page.AuthorName != "" {
		article.Author = &PersonSchema{
			Type: "Person",
			Name: page.AuthorName,
		}
	}

	// Publisher
	article.Publisher = &OrgSchema{
		Type: "Organization",
		Name: site.SiteName,
	}
	if site.DefaultOGImage != "" {
		article.Publisher.Logo = &ImageSchema{
			Type: "ImageObject",
			URL:  makeAbsoluteURL(site.DefaultOGImage, site.SiteURL),
		}
	}

	return marshalJSONLD(article)
}

// marshalJSONLD marshals structured data to JSON-LD script tag content.
func marshalJSONLD(v any) template.JS {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return template.JS(data)
}

// Helper functions

// stripHTML removes HTML tags from a string.
func stripHTML(html string) string {
	var result strings.Builder
	inTag := false
	for _, r := range html {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			result.WriteRune(' ') // Replace tags with space
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	// Collapse whitespace
	return strings.Join(strings.Fields(result.String()), " ")
}

// truncateText truncates text to maxLen characters at word boundary.
func truncateText(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}

	truncated := text[:maxLen]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}

	return strings.TrimSpace(truncated) + "..."
}

// makeAbsoluteURL ensures a URL is absolute by prepending site URL if needed.
func makeAbsoluteURL(url, siteURL string) string {
	if url == "" {
		return ""
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	siteURL = strings.TrimSuffix(siteURL, "/")
	if !strings.HasPrefix(url, "/") {
		url = "/" + url
	}
	return siteURL + url
}
