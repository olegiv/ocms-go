// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestSitemapCache_GetCachesBySiteURL(t *testing.T) {
	q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	ctx := context.Background()

	xmlA, err := c.Get(ctx, "https://first.example")
	if err != nil {
		t.Fatalf("Get(first) error: %v", err)
	}
	if !strings.Contains(string(xmlA), "https://first.example") {
		t.Fatalf("expected first sitemap to contain first host, got: %s", string(xmlA))
	}

	statsAfterFirst := c.Stats()
	if statsAfterFirst.Misses != 1 {
		t.Fatalf("after first Get misses=%d, want 1", statsAfterFirst.Misses)
	}
	if statsAfterFirst.Hits != 0 {
		t.Fatalf("after first Get hits=%d, want 0", statsAfterFirst.Hits)
	}

	xmlB, err := c.Get(ctx, "https://second.example")
	if err != nil {
		t.Fatalf("Get(second) error: %v", err)
	}
	if !strings.Contains(string(xmlB), "https://second.example") {
		t.Fatalf("expected second sitemap to contain second host, got: %s", string(xmlB))
	}
	if strings.Contains(string(xmlB), "https://first.example") {
		t.Fatalf("second sitemap should not contain first host, got: %s", string(xmlB))
	}

	statsAfterSecond := c.Stats()
	if statsAfterSecond.Misses != 2 {
		t.Fatalf("after second Get misses=%d, want 2", statsAfterSecond.Misses)
	}
	if statsAfterSecond.Hits != 0 {
		t.Fatalf("after second Get hits=%d, want 0", statsAfterSecond.Hits)
	}

	xmlB2, err := c.Get(ctx, "https://second.example")
	if err != nil {
		t.Fatalf("Get(second cached) error: %v", err)
	}
	if !strings.Contains(string(xmlB2), "https://second.example") {
		t.Fatalf("expected cached second sitemap to contain second host, got: %s", string(xmlB2))
	}

	statsAfterThird := c.Stats()
	if statsAfterThird.Misses != 2 {
		t.Fatalf("after third Get misses=%d, want 2", statsAfterThird.Misses)
	}
	if statsAfterThird.Hits != 1 {
		t.Fatalf("after third Get hits=%d, want 1", statsAfterThird.Hits)
	}
}

func TestSitemapCacheLanguageCanonicalURLs(t *testing.T) {
	q := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for _, lang := range []struct {
		code   string
		active bool
	}{
		{code: "fr", active: true},
		{code: "de", active: false},
		{code: "blog", active: true},
		{code: "x", active: true},
	} {
		_, err := q.CreateLanguage(ctx, store.CreateLanguageParams{
			Code:       lang.code,
			Name:       lang.code,
			NativeName: lang.code,
			IsActive:   lang.active,
			Direction:  "ltr",
			CreatedAt:  now,
			UpdatedAt:  now,
		})
		if err != nil {
			t.Fatalf("CreateLanguage(%q): %v", lang.code, err)
		}
	}

	author, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "sitemap@example.com",
		PasswordHash: "hash",
		Role:         "editor",
		Name:         "Sitemap Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for _, code := range []string{"en", "fr", "de", "zz", "blog", "x"} {
		_, err = q.CreatePage(ctx, store.CreatePageParams{
			Title:        "Page " + code,
			Slug:         "page-" + code,
			Status:       "published",
			AuthorID:     author.ID,
			LanguageCode: code,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			t.Fatalf("CreatePage(%q): %v", code, err)
		}
		_, err = q.CreateCategory(ctx, store.CreateCategoryParams{
			Name:         "Category " + code,
			Slug:         "category-" + code,
			LanguageCode: code,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			t.Fatalf("CreateCategory(%q): %v", code, err)
		}
		_, err = q.CreateTag(ctx, store.CreateTagParams{
			Name:         "Tag " + code,
			Slug:         "tag-" + code,
			LanguageCode: code,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			t.Fatalf("CreateTag(%q): %v", code, err)
		}
	}

	c := NewSitemapCache(q, time.Hour)
	content, err := c.Get(ctx, "https://example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var document struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(content, &document); err != nil {
		t.Fatalf("Unmarshal sitemap: %v", err)
	}

	want := map[string]bool{
		"https://example.com":                         false,
		"https://example.com/fr":                      false,
		"https://example.com/page-en":                 false,
		"https://example.com/fr/page-fr":              false,
		"https://example.com/category/category-en":    false,
		"https://example.com/fr/category/category-fr": false,
		"https://example.com/tag/tag-en":              false,
		"https://example.com/fr/tag/tag-fr":           false,
	}
	if len(document.URLs) != len(want) {
		t.Errorf("sitemap URL count = %d, want %d", len(document.URLs), len(want))
	}
	for _, entry := range document.URLs {
		if _, ok := want[entry.Loc]; !ok {
			t.Errorf("unexpected sitemap URL %q", entry.Loc)
			continue
		}
		want[entry.Loc] = true
	}
	for loc, found := range want {
		if !found {
			t.Errorf("missing sitemap URL %q", loc)
		}
	}

	defaultLang, err := q.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("GetDefaultLanguage: %v", err)
	}
	_, err = q.UpdateLanguage(ctx, store.UpdateLanguageParams{
		Code:       defaultLang.Code,
		Name:       defaultLang.Name,
		NativeName: defaultLang.NativeName,
		IsDefault:  true,
		IsActive:   false,
		Direction:  defaultLang.Direction,
		Position:   defaultLang.Position,
		UpdatedAt:  now.Add(time.Minute),
		ID:         defaultLang.ID,
	})
	if err != nil {
		t.Fatalf("deactivate default language: %v", err)
	}
	c.Invalidate()
	content, err = c.Get(ctx, "https://example.com")
	if err != nil {
		t.Fatalf("Get with inactive default: %v", err)
	}
	if strings.Contains(string(content), "<loc>https://example.com</loc>") {
		t.Error("inactive default homepage was emitted in sitemap")
	}
	if strings.Contains(string(content), "page-en") ||
		strings.Contains(string(content), "category-en") ||
		strings.Contains(string(content), "tag-en") {
		t.Error("inactive default-language entity was emitted in sitemap")
	}
	if !strings.Contains(string(content), "https://example.com/fr/page-fr") ||
		!strings.Contains(string(content), "<loc>https://example.com/fr</loc>") ||
		!strings.Contains(string(content), "https://example.com/fr/category/category-fr") ||
		!strings.Contains(string(content), "https://example.com/fr/tag/tag-fr") {
		t.Error("active non-default sitemap URLs disappeared with inactive default")
	}
}

func TestSitemapCacheRejectsAmbiguousDefaultLanguages(t *testing.T) {
	q := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	_, err := q.CreateLanguage(ctx, store.CreateLanguageParams{
		Code:       "fr",
		Name:       "French",
		NativeName: "Français",
		IsDefault:  true,
		IsActive:   true,
		Direction:  "ltr",
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}

	c := NewSitemapCache(q, time.Hour)
	if _, err := c.Get(ctx, "https://example.com"); err == nil {
		t.Fatal("Get succeeded with multiple default languages")
	}
}
