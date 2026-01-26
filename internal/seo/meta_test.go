// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"strings"
	"testing"
	"time"
)

func TestBuildMetaHomepage(t *testing.T) {
	site := &SiteConfig{
		SiteName:        "My Site",
		SiteURL:         "https://example.com",
		SiteDescription: "A great website",
		DefaultOGImage:  "/images/og-default.jpg",
		TwitterHandle:   "@mysite",
	}

	meta := BuildMeta(nil, site)

	if meta.Title != "My Site" {
		t.Errorf("Title = %q, want %q", meta.Title, "My Site")
	}
	if meta.OGTitle != "My Site" {
		t.Errorf("OGTitle = %q, want %q", meta.OGTitle, "My Site")
	}
	if meta.Description != "A great website" {
		t.Errorf("Description = %q, want %q", meta.Description, "A great website")
	}
	if meta.OGDescription != "A great website" {
		t.Errorf("OGDescription = %q, want %q", meta.OGDescription, "A great website")
	}
	if meta.OGType != "website" {
		t.Errorf("OGType = %q, want %q", meta.OGType, "website")
	}
	if meta.OGSiteName != "My Site" {
		t.Errorf("OGSiteName = %q, want %q", meta.OGSiteName, "My Site")
	}
	if meta.Canonical != "https://example.com" {
		t.Errorf("Canonical = %q, want %q", meta.Canonical, "https://example.com")
	}
	if meta.OGURL != "https://example.com" {
		t.Errorf("OGURL = %q, want %q", meta.OGURL, "https://example.com")
	}
	if meta.TwitterCard != "summary_large_image" {
		t.Errorf("TwitterCard = %q, want %q", meta.TwitterCard, "summary_large_image")
	}
	if meta.TwitterSite != "@mysite" {
		t.Errorf("TwitterSite = %q, want %q", meta.TwitterSite, "@mysite")
	}
	if meta.Robots != "index,follow" {
		t.Errorf("Robots = %q, want %q", meta.Robots, "index,follow")
	}
	if meta.OGImage != "https://example.com/images/og-default.jpg" {
		t.Errorf("OGImage = %q, want %q", meta.OGImage, "https://example.com/images/og-default.jpg")
	}
}

func TestBuildMetaPage(t *testing.T) {
	site := &SiteConfig{
		SiteName:        "My Site",
		SiteURL:         "https://example.com",
		SiteDescription: "A great website",
		DefaultOGImage:  "/images/og-default.jpg",
	}

	page := &PageData{
		Title:           "About Us",
		Slug:            "about",
		MetaTitle:       "About Our Company",
		MetaDescription: "Learn about our company",
		MetaKeywords:    "about, company, team",
		OGImageURL:      "/images/about-og.jpg",
	}

	meta := BuildMeta(page, site)

	if meta.Title != "About Our Company" {
		t.Errorf("Title = %q, want %q", meta.Title, "About Our Company")
	}
	if meta.OGTitle != "About Our Company" {
		t.Errorf("OGTitle = %q, want %q", meta.OGTitle, "About Our Company")
	}
	if meta.Description != "Learn about our company" {
		t.Errorf("Description = %q, want %q", meta.Description, "Learn about our company")
	}
	if meta.Keywords != "about, company, team" {
		t.Errorf("Keywords = %q, want %q", meta.Keywords, "about, company, team")
	}
	if meta.OGType != "article" {
		t.Errorf("OGType = %q, want %q", meta.OGType, "article")
	}
	if meta.Canonical != "https://example.com/about" {
		t.Errorf("Canonical = %q, want %q", meta.Canonical, "https://example.com/about")
	}
	if meta.OGImage != "https://example.com/images/about-og.jpg" {
		t.Errorf("OGImage = %q, want %q", meta.OGImage, "https://example.com/images/about-og.jpg")
	}
}

func TestBuildMetaPageFallbackToTitle(t *testing.T) {
	site := &SiteConfig{
		SiteName: "My Site",
		SiteURL:  "https://example.com",
	}

	page := &PageData{
		Title: "Page Title",
		Slug:  "page",
	}

	meta := BuildMeta(page, site)

	if meta.Title != "Page Title" {
		t.Errorf("Title = %q, want %q", meta.Title, "Page Title")
	}
	if meta.OGTitle != "Page Title" {
		t.Errorf("OGTitle = %q, want %q", meta.OGTitle, "Page Title")
	}
}

func TestBuildMetaPageFallbackToBody(t *testing.T) {
	site := &SiteConfig{
		SiteName: "My Site",
		SiteURL:  "https://example.com",
	}

	page := &PageData{
		Title: "Test",
		Slug:  "test",
		Body:  "<p>This is the page content with <strong>HTML</strong> tags.</p>",
	}

	meta := BuildMeta(page, site)

	// Description should be stripped of HTML
	if strings.Contains(meta.Description, "<p>") || strings.Contains(meta.Description, "<strong>") {
		t.Errorf("Description should not contain HTML tags: %q", meta.Description)
	}
	if !strings.Contains(meta.Description, "This is the page content") {
		t.Errorf("Description should contain body text: %q", meta.Description)
	}
}

func TestBuildMetaPageFallbackToFeaturedImage(t *testing.T) {
	site := &SiteConfig{
		SiteName: "My Site",
		SiteURL:  "https://example.com",
	}

	page := &PageData{
		Title:         "Test",
		Slug:          "test",
		FeaturedImage: "/uploads/featured.jpg",
	}

	meta := BuildMeta(page, site)

	if meta.OGImage != "https://example.com/uploads/featured.jpg" {
		t.Errorf("OGImage = %q, want %q", meta.OGImage, "https://example.com/uploads/featured.jpg")
	}
}

func TestBuildMetaPageFallbackToDefaultOGImage(t *testing.T) {
	site := &SiteConfig{
		SiteName:       "My Site",
		SiteURL:        "https://example.com",
		DefaultOGImage: "/images/default.jpg",
	}

	page := &PageData{
		Title: "Test",
		Slug:  "test",
	}

	meta := BuildMeta(page, site)

	if meta.OGImage != "https://example.com/images/default.jpg" {
		t.Errorf("OGImage = %q, want %q", meta.OGImage, "https://example.com/images/default.jpg")
	}
}

func TestBuildMetaPageWithCanonicalURL(t *testing.T) {
	site := &SiteConfig{
		SiteName: "My Site",
		SiteURL:  "https://example.com",
	}

	page := &PageData{
		Title:        "Test",
		Slug:         "test",
		CanonicalURL: "https://other.com/canonical",
	}

	meta := BuildMeta(page, site)

	if meta.Canonical != "https://other.com/canonical" {
		t.Errorf("Canonical = %q, want %q", meta.Canonical, "https://other.com/canonical")
	}
}

func TestBuildMetaPageRobotsDirective(t *testing.T) {
	site := &SiteConfig{
		SiteName: "My Site",
		SiteURL:  "https://example.com",
	}

	tests := []struct {
		name     string
		noIndex  bool
		noFollow bool
		want     string
	}{
		{"index,follow", false, false, "index,follow"},
		{"noindex,follow", true, false, "noindex,follow"},
		{"index,nofollow", false, true, "index,nofollow"},
		{"noindex,nofollow", true, true, "noindex,nofollow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := &PageData{
				Title:    "Test",
				Slug:     "test",
				NoIndex:  tt.noIndex,
				NoFollow: tt.noFollow,
			}

			meta := BuildMeta(page, site)

			if meta.Robots != tt.want {
				t.Errorf("Robots = %q, want %q", meta.Robots, tt.want)
			}
		})
	}
}

func TestBuildMetaAbsoluteURLHandling(t *testing.T) {
	site := &SiteConfig{
		SiteName: "My Site",
		SiteURL:  "https://example.com",
	}

	// Test with already absolute URL
	page := &PageData{
		Title:      "Test",
		Slug:       "test",
		OGImageURL: "https://cdn.example.com/image.jpg",
	}

	meta := BuildMeta(page, site)

	// Should preserve absolute URL
	if meta.OGImage != "https://cdn.example.com/image.jpg" {
		t.Errorf("OGImage = %q, want %q", meta.OGImage, "https://cdn.example.com/image.jpg")
	}
}

func TestBuildArticleSchema(t *testing.T) {
	site := &SiteConfig{
		SiteName:       "My Site",
		SiteURL:        "https://example.com",
		DefaultOGImage: "/images/logo.png",
	}

	publishedAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	modifiedAt := time.Date(2025, 1, 20, 14, 30, 0, 0, time.UTC)

	page := &PageData{
		Title:           "Test Article",
		Slug:            "test-article",
		MetaDescription: "Article description",
		OGImageURL:      "/images/article.jpg",
		PublishedAt:     &publishedAt,
		AuthorName:      "John Doe",
	}

	schema := BuildArticleSchema(page, site, modifiedAt)

	if schema == "" {
		t.Fatal("BuildArticleSchema() returned empty")
	}

	schemaStr := string(schema)

	// Check required fields
	if !strings.Contains(schemaStr, `"@context": "https://schema.org"`) {
		t.Error("Schema should contain @context")
	}
	if !strings.Contains(schemaStr, `"@type": "Article"`) {
		t.Error("Schema should contain @type Article")
	}
	if !strings.Contains(schemaStr, `"headline": "Test Article"`) {
		t.Error("Schema should contain headline")
	}
	if !strings.Contains(schemaStr, `"description": "Article description"`) {
		t.Error("Schema should contain description")
	}
	if !strings.Contains(schemaStr, "2025-01-15") {
		t.Error("Schema should contain published date")
	}
	if !strings.Contains(schemaStr, "2025-01-20") {
		t.Error("Schema should contain modified date")
	}
	if !strings.Contains(schemaStr, `"name": "John Doe"`) {
		t.Error("Schema should contain author name")
	}
}

func TestBuildArticleSchemaNilPage(t *testing.T) {
	site := &SiteConfig{
		SiteName: "My Site",
		SiteURL:  "https://example.com",
	}

	schema := BuildArticleSchema(nil, site, time.Now())

	if schema != "" {
		t.Errorf("BuildArticleSchema(nil, ...) = %q, want empty", schema)
	}
}

func TestBuildArticleSchemaNoAuthor(t *testing.T) {
	site := &SiteConfig{
		SiteName: "My Site",
		SiteURL:  "https://example.com",
	}

	page := &PageData{
		Title: "Test",
		Slug:  "test",
	}

	schema := BuildArticleSchema(page, site, time.Now())
	schemaStr := string(schema)

	// Should not have author field with empty name
	if strings.Contains(schemaStr, `"author":`) && !strings.Contains(schemaStr, `"author": null`) {
		// Check if there's an author object with a name
		if strings.Contains(schemaStr, `"name": ""`) {
			t.Error("Schema should not include author with empty name")
		}
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple paragraph",
			input: "<p>Hello World</p>",
			want:  "Hello World",
		},
		{
			name:  "nested tags",
			input: "<div><p>Hello <strong>World</strong></p></div>",
			want:  "Hello World",
		},
		{
			name:  "multiple spaces",
			input: "<p>Hello</p>  <p>World</p>",
			want:  "Hello World",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "no tags",
			input: "Plain text",
			want:  "Plain text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		maxLen int
		want   string
	}{
		{
			name:   "short text",
			text:   "Hello",
			maxLen: 100,
			want:   "Hello",
		},
		{
			name:   "exact length",
			text:   "Hello",
			maxLen: 5,
			want:   "Hello",
		},
		{
			name:   "truncate at word boundary",
			text:   "Hello World and more text here",
			maxLen: 15,
			want:   "Hello World...",
		},
		{
			name:   "empty text",
			text:   "",
			maxLen: 100,
			want:   "",
		},
		{
			name:   "whitespace trimmed",
			text:   "  Hello World  ",
			maxLen: 100,
			want:   "Hello World",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateText(tt.text, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateText(%q, %d) = %q, want %q", tt.text, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestMakeAbsoluteURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		siteURL string
		want    string
	}{
		{
			name:    "relative with slash",
			url:     "/images/test.jpg",
			siteURL: "https://example.com",
			want:    "https://example.com/images/test.jpg",
		},
		{
			name:    "relative without slash",
			url:     "images/test.jpg",
			siteURL: "https://example.com",
			want:    "https://example.com/images/test.jpg",
		},
		{
			name:    "already absolute http",
			url:     "http://other.com/image.jpg",
			siteURL: "https://example.com",
			want:    "http://other.com/image.jpg",
		},
		{
			name:    "already absolute https",
			url:     "https://cdn.com/image.jpg",
			siteURL: "https://example.com",
			want:    "https://cdn.com/image.jpg",
		},
		{
			name:    "empty url",
			url:     "",
			siteURL: "https://example.com",
			want:    "",
		},
		{
			name:    "site url with trailing slash",
			url:     "/image.jpg",
			siteURL: "https://example.com/",
			want:    "https://example.com/image.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeAbsoluteURL(tt.url, tt.siteURL)
			if got != tt.want {
				t.Errorf("makeAbsoluteURL(%q, %q) = %q, want %q", tt.url, tt.siteURL, got, tt.want)
			}
		})
	}
}
