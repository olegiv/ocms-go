// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"strings"
	"testing"
	"time"
)

func TestNewSitemapBuilder(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")
	if builder == nil {
		t.Fatal("NewSitemapBuilder() returned nil")
	}
	if builder.siteURL != "https://example.com" {
		t.Errorf("siteURL = %q, want %q", builder.siteURL, "https://example.com")
	}
	if len(builder.urls) != 0 {
		t.Errorf("urls length = %d, want 0", len(builder.urls))
	}
}

func TestSitemapBuilderAddHomepage(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")
	builder.AddHomepage()

	if len(builder.urls) != 1 {
		t.Fatalf("urls length = %d, want 1", len(builder.urls))
	}

	url := builder.urls[0]
	if url.Loc != "https://example.com" {
		t.Errorf("Loc = %q, want %q", url.Loc, "https://example.com")
	}
	if url.Priority != "1.0" {
		t.Errorf("Priority = %q, want %q", url.Priority, "1.0")
	}
	if url.ChangeFreq != ChangeFreqDaily {
		t.Errorf("ChangeFreq = %q, want %q", url.ChangeFreq, ChangeFreqDaily)
	}
}

func TestSitemapBuilderAddLanguageHomepages(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com/")
	builder.AddLanguageHomepage("en", true)
	builder.AddLanguageHomepage("fr", false)
	builder.AddLanguageHomepage("blog", false)
	builder.AddLanguageHomepage("x", false)

	want := []string{
		"https://example.com",
		"https://example.com/fr",
	}
	if len(builder.urls) != len(want) {
		t.Fatalf("urls length = %d, want %d: %+v", len(builder.urls), len(want), builder.urls)
	}
	for i, loc := range want {
		if builder.urls[i].Loc != loc {
			t.Errorf("urls[%d].Loc = %q, want %q", i, builder.urls[i].Loc, loc)
		}
	}
}

func TestSitemapBuilderAddPage(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")
	updatedAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)

	builder.AddPage(SitemapPage{
		Slug: "about-us", LanguageCode: "en", IsDefault: true, UpdatedAt: updatedAt,
	})

	if len(builder.urls) != 1 {
		t.Fatalf("urls length = %d, want 1", len(builder.urls))
	}

	url := builder.urls[0]
	if url.Loc != "https://example.com/about-us" {
		t.Errorf("Loc = %q, want %q", url.Loc, "https://example.com/about-us")
	}
	if url.Priority != "0.8" {
		t.Errorf("Priority = %q, want %q", url.Priority, "0.8")
	}
	if url.ChangeFreq != ChangeFreqWeekly {
		t.Errorf("ChangeFreq = %q, want %q", url.ChangeFreq, ChangeFreqWeekly)
	}
	if !strings.Contains(url.LastMod, "2025-01-15") {
		t.Errorf("LastMod = %q, should contain 2025-01-15", url.LastMod)
	}
}

func TestSitemapBuilderAddPages(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")

	pages := []SitemapPage{
		{Slug: "page-1", LanguageCode: "en", IsDefault: true},
		{Slug: "page-2", LanguageCode: "en", IsDefault: true},
		{Slug: "page-3", LanguageCode: "en", IsDefault: true},
	}
	builder.AddPages(pages)

	if len(builder.urls) != 3 {
		t.Fatalf("urls length = %d, want 3", len(builder.urls))
	}

	for i, page := range pages {
		expected := "https://example.com/" + page.Slug
		if builder.urls[i].Loc != expected {
			t.Errorf("urls[%d].Loc = %q, want %q", i, builder.urls[i].Loc, expected)
		}
	}
}

// testSitemapTaxonomyAdd tests adding a single taxonomy item (category or tag).
func testSitemapTaxonomyAdd(t *testing.T, addFn func(*SitemapBuilder), expectedLoc, expectedPriority string) {
	t.Helper()
	builder := NewSitemapBuilder("https://example.com")
	addFn(builder)

	if len(builder.urls) != 1 {
		t.Fatalf("urls length = %d, want 1", len(builder.urls))
	}

	url := builder.urls[0]
	if url.Loc != expectedLoc {
		t.Errorf("Loc = %q, want %q", url.Loc, expectedLoc)
	}
	if url.Priority != expectedPriority {
		t.Errorf("Priority = %q, want %q", url.Priority, expectedPriority)
	}
}

func TestSitemapBuilderAddCategory(t *testing.T) {
	testSitemapTaxonomyAdd(t, func(b *SitemapBuilder) {
		b.AddCategory(SitemapCategory{Slug: "technology", LanguageCode: "en", IsDefault: true})
	}, "https://example.com/category/technology", "0.6")
}

func TestSitemapBuilderAddCategories(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")

	categories := []SitemapCategory{
		{Slug: "tech", LanguageCode: "en", IsDefault: true},
		{Slug: "news", LanguageCode: "en", IsDefault: true},
	}
	builder.AddCategories(categories)

	if len(builder.urls) != 2 {
		t.Fatalf("urls length = %d, want 2", len(builder.urls))
	}
}

func TestSitemapBuilderAddTag(t *testing.T) {
	testSitemapTaxonomyAdd(t, func(b *SitemapBuilder) {
		b.AddTag(SitemapTag{Slug: "golang", LanguageCode: "en", IsDefault: true})
	}, "https://example.com/tag/golang", "0.5")
}

func TestSitemapBuilderAddTags(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")

	tags := []SitemapTag{
		{Slug: "go", LanguageCode: "en", IsDefault: true},
		{Slug: "rust", LanguageCode: "en", IsDefault: true},
		{Slug: "python", LanguageCode: "en", IsDefault: true},
	}
	builder.AddTags(tags)

	if len(builder.urls) != 3 {
		t.Fatalf("urls length = %d, want 3", len(builder.urls))
	}
}

func TestSitemapBuilderBuild(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")
	builder.AddHomepage()
	builder.AddPage(SitemapPage{Slug: "about", LanguageCode: "en", IsDefault: true})

	xml, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content := string(xml)

	// Check XML header
	if !strings.HasPrefix(content, "<?xml") {
		t.Error("Build() output should start with XML header")
	}

	// Check namespace
	if !strings.Contains(content, XMLNamespace) {
		t.Errorf("Build() output should contain namespace %q", XMLNamespace)
	}

	// Check URLs are present
	if !strings.Contains(content, "https://example.com") {
		t.Error("Build() output should contain homepage URL")
	}
	if !strings.Contains(content, "https://example.com/about") {
		t.Error("Build() output should contain about page URL")
	}

	// Check structure elements
	if !strings.Contains(content, "<urlset") {
		t.Error("Build() output should contain <urlset> element")
	}
	if !strings.Contains(content, "<url>") {
		t.Error("Build() output should contain <url> element")
	}
	if !strings.Contains(content, "<loc>") {
		t.Error("Build() output should contain <loc> element")
	}
}

func TestSitemapBuilderBuildEmpty(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")

	xml, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	content := string(xml)
	if !strings.Contains(content, "<urlset") {
		t.Error("Build() empty sitemap should still have urlset element")
	}
}

func TestSitemapBuilderLastModWithZeroTime(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com")

	// Add page with zero time (should not include lastmod)
	builder.AddPage(SitemapPage{
		Slug: "no-date", LanguageCode: "en", IsDefault: true, UpdatedAt: time.Time{},
	})

	if len(builder.urls) != 1 {
		t.Fatalf("urls length = %d, want 1", len(builder.urls))
	}

	// LastMod should be empty for zero time
	if builder.urls[0].LastMod != "" {
		t.Errorf("LastMod = %q, want empty string for zero time", builder.urls[0].LastMod)
	}
}

func TestSitemapBuilderLanguageCanonicalURLs(t *testing.T) {
	builder := NewSitemapBuilder("https://example.com/")

	builder.AddPages([]SitemapPage{
		{Slug: "welcome", LanguageCode: "en", IsDefault: true},
		{Slug: "bienvenue", LanguageCode: "fr"},
		{Slug: "reserved-page", LanguageCode: "blog"},
		{Slug: "invalid-page", LanguageCode: "x"},
		{Slug: "reserved-default-page", LanguageCode: "blog", IsDefault: true},
	})
	builder.AddCategories([]SitemapCategory{
		{Slug: "news", LanguageCode: "en", IsDefault: true},
		{Slug: "actualites", LanguageCode: "fr"},
		{Slug: "reserved-category", LanguageCode: "category"},
	})
	builder.AddTags([]SitemapTag{
		{Slug: "go", LanguageCode: "en", IsDefault: true},
		{Slug: "aller", LanguageCode: "fr"},
		{Slug: "invalid-tag", LanguageCode: "fr--ch"},
	})

	want := []string{
		"https://example.com/welcome",
		"https://example.com/fr/bienvenue",
		"https://example.com/category/news",
		"https://example.com/fr/category/actualites",
		"https://example.com/tag/go",
		"https://example.com/fr/tag/aller",
	}
	if len(builder.urls) != len(want) {
		t.Fatalf("urls length = %d, want %d: %+v", len(builder.urls), len(want), builder.urls)
	}
	for i, loc := range want {
		if builder.urls[i].Loc != loc {
			t.Errorf("urls[%d].Loc = %q, want %q", i, builder.urls[i].Loc, loc)
		}
	}
}
