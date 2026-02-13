// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestConvertLanguageOption(t *testing.T) {
	lang := store.Language{
		ID: 1, Code: "en", Name: "English", NativeName: "English",
		IsDefault: true, IsActive: true, Direction: "ltr",
	}
	got := convertLanguageOption(lang)
	if got.Code != "en" || got.Name != "English" || got.NativeName != "English" {
		t.Errorf("convertLanguageOption() = {%q, %q, %q}; want {\"en\", \"English\", \"English\"}", got.Code, got.Name, got.NativeName)
	}
}

func TestConvertLanguageOptions(t *testing.T) {
	langs := []store.Language{
		{Code: "en", Name: "English", NativeName: "English"},
		{Code: "ru", Name: "Russian", NativeName: "Русский"},
	}
	got := convertLanguageOptions(langs)
	if len(got) != 2 {
		t.Fatalf("convertLanguageOptions() returned %d items; want 2", len(got))
	}
	if got[0].Code != "en" {
		t.Errorf("got[0].Code = %q; want \"en\"", got[0].Code)
	}
	if got[1].Code != "ru" {
		t.Errorf("got[1].Code = %q; want \"ru\"", got[1].Code)
	}
}

func TestConvertLanguageOptions_Empty(t *testing.T) {
	got := convertLanguageOptions(nil)
	if got != nil {
		t.Errorf("convertLanguageOptions(nil) = %v; want nil", got)
	}
}

func TestConvertLanguageOptionPtr(t *testing.T) {
	lang := &store.Language{Code: "de", Name: "German", NativeName: "Deutsch"}
	got := convertLanguageOptionPtr(lang)
	if got == nil {
		t.Fatal("convertLanguageOptionPtr() returned nil")
	}
	if got.Code != "de" {
		t.Errorf("got.Code = %q; want \"de\"", got.Code)
	}
}

func TestConvertLanguageOptionPtr_Nil(t *testing.T) {
	got := convertLanguageOptionPtr(nil)
	if got != nil {
		t.Errorf("convertLanguageOptionPtr(nil) = %v; want nil", got)
	}
}

func TestConvertTagItem(t *testing.T) {
	tag := store.Tag{
		ID: 42, Name: "Go", Slug: "go", LanguageCode: "en",
	}
	got := convertTagItem(tag)
	if got.ID != 42 || got.Name != "Go" || got.Slug != "go" || got.LanguageCode != "en" {
		t.Errorf("convertTagItem() = %+v; want ID=42, Name=\"Go\", Slug=\"go\"", got)
	}
}

func TestConvertTagTranslations(t *testing.T) {
	translations := []TagTranslationInfo{
		{
			Tag:      store.Tag{ID: 1, Name: "Go", Slug: "go", LanguageCode: "en"},
			Language: store.Language{Code: "en", Name: "English", NativeName: "English"},
		},
		{
			Tag:      store.Tag{ID: 2, Name: "Го", Slug: "go", LanguageCode: "ru"},
			Language: store.Language{Code: "ru", Name: "Russian", NativeName: "Русский"},
		},
	}
	got := convertTagTranslations(translations)
	if len(got) != 2 {
		t.Fatalf("convertTagTranslations() returned %d items; want 2", len(got))
	}
	if got[0].Tag.Name != "Go" {
		t.Errorf("got[0].Tag.Name = %q; want \"Go\"", got[0].Tag.Name)
	}
	if got[1].Language.Code != "ru" {
		t.Errorf("got[1].Language.Code = %q; want \"ru\"", got[1].Language.Code)
	}
}

func TestConvertTagTranslations_Empty(t *testing.T) {
	got := convertTagTranslations(nil)
	if got != nil {
		t.Errorf("convertTagTranslations(nil) = %v; want nil", got)
	}
}

func TestConvertCategoryItem(t *testing.T) {
	cat := store.Category{
		ID:           10,
		Name:         "Tech",
		Slug:         "tech",
		Description:  sql.NullString{String: "Technology", Valid: true},
		ParentID:     sql.NullInt64{Int64: 5, Valid: true},
		Position:     3,
		LanguageCode: "en",
	}
	got := convertCategoryItem(cat)
	if got.ID != 10 || got.Name != "Tech" || got.Slug != "tech" {
		t.Errorf("convertCategoryItem() basic fields = %+v", got)
	}
	if got.Description != "Technology" {
		t.Errorf("got.Description = %q; want \"Technology\"", got.Description)
	}
	if got.ParentID != 5 || !got.HasParent {
		t.Errorf("got.ParentID = %d, got.HasParent = %v; want 5, true", got.ParentID, got.HasParent)
	}
	if got.Position != 3 {
		t.Errorf("got.Position = %d; want 3", got.Position)
	}
}

func TestConvertCategoryItem_NoParent(t *testing.T) {
	cat := store.Category{
		ID:       1,
		Name:     "Root",
		Slug:     "root",
		ParentID: sql.NullInt64{Valid: false},
	}
	got := convertCategoryItem(cat)
	if got.HasParent {
		t.Error("got.HasParent should be false for category without parent")
	}
	if got.ParentID != 0 {
		t.Errorf("got.ParentID = %d; want 0", got.ParentID)
	}
}

func TestConvertCategoryListItems(t *testing.T) {
	nodes := []CategoryTreeNode{
		{
			Category: store.Category{
				ID: 1, Name: "Parent", Slug: "parent",
				Description:  sql.NullString{String: "A parent", Valid: true},
				LanguageCode: "en",
			},
			UsageCount: 5,
			Depth:      0,
			Children:   []CategoryTreeNode{{Category: store.Category{ID: 2}}},
		},
		{
			Category: store.Category{
				ID: 3, Name: "Orphan", Slug: "orphan",
				Description: sql.NullString{Valid: false},
			},
			UsageCount: 0,
			Depth:      1,
		},
	}
	got := convertCategoryListItems(nodes)
	if len(got) != 2 {
		t.Fatalf("convertCategoryListItems() returned %d items; want 2", len(got))
	}
	if got[0].Children != true {
		t.Error("got[0].Children should be true (has children)")
	}
	if got[0].UsageCount != 5 {
		t.Errorf("got[0].UsageCount = %d; want 5", got[0].UsageCount)
	}
	if got[1].Description != "" {
		t.Errorf("got[1].Description = %q; want empty string", got[1].Description)
	}
	if got[1].Children != false {
		t.Error("got[1].Children should be false (no children)")
	}
}

func TestConvertCategoryTranslations(t *testing.T) {
	translations := []CategoryTranslationInfo{
		{
			Category: store.Category{ID: 1, Name: "Tech", Slug: "tech", LanguageCode: "en"},
			Language: store.Language{Code: "en", Name: "English", NativeName: "English"},
		},
	}
	got := convertCategoryTranslations(translations)
	if len(got) != 1 {
		t.Fatalf("convertCategoryTranslations() returned %d items; want 1", len(got))
	}
	if got[0].Category.Name != "Tech" || got[0].Language.Code != "en" {
		t.Errorf("convertCategoryTranslations()[0] = %+v", got[0])
	}
}

func TestConvertRedirectListItems(t *testing.T) {
	now := time.Now()
	redirects := []store.Redirect{
		{
			ID: 1, SourcePath: "/old", TargetUrl: "/new",
			StatusCode: 301, IsWildcard: false, TargetType: "internal",
			Enabled: true, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: 2, SourcePath: "/api/*", TargetUrl: "https://api.example.com",
			StatusCode: 302, IsWildcard: true, TargetType: "external",
			Enabled: false,
		},
	}
	got := convertRedirectListItems(redirects)
	if len(got) != 2 {
		t.Fatalf("convertRedirectListItems() returned %d items; want 2", len(got))
	}
	if got[0].SourcePath != "/old" || got[0].StatusCode != 301 || !got[0].Enabled {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].SourcePath != "/api/*" || !got[1].IsWildcard || got[1].Enabled {
		t.Errorf("got[1] = %+v", got[1])
	}
}

func TestConvertRedirectInfo(t *testing.T) {
	now := time.Now()
	rd := &store.Redirect{
		ID: 5, SourcePath: "/src", TargetUrl: "/dst",
		StatusCode: 301, IsWildcard: true, TargetType: "internal",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	got := convertRedirectInfo(rd)
	if got == nil {
		t.Fatal("convertRedirectInfo() returned nil")
	}
	if got.ID != 5 || got.SourcePath != "/src" || got.TargetURL != "/dst" {
		t.Errorf("convertRedirectInfo() = %+v", got)
	}
	if !got.IsWildcard || !got.Enabled {
		t.Errorf("got.IsWildcard=%v, got.Enabled=%v; want true, true", got.IsWildcard, got.Enabled)
	}
}

func TestConvertRedirectInfo_Nil(t *testing.T) {
	got := convertRedirectInfo(nil)
	if got != nil {
		t.Errorf("convertRedirectInfo(nil) = %v; want nil", got)
	}
}

func TestConvertStatusCodes(t *testing.T) {
	codes := []StatusCodeOption{
		{Code: 301, Label: "Permanent Redirect"},
		{Code: 302, Label: "Temporary Redirect"},
	}
	got := convertStatusCodes(codes)
	if len(got) != 2 {
		t.Fatalf("convertStatusCodes() returned %d items; want 2", len(got))
	}
	if got[0].Code != 301 || got[0].Label != "Permanent Redirect" {
		t.Errorf("got[0] = %+v", got[0])
	}
}

func TestConvertMenuListItems(t *testing.T) {
	now := time.Now()
	menus := []store.Menu{
		{ID: 1, Name: "Main Menu", Slug: "main", LanguageCode: "en", UpdatedAt: now},
		{ID: 2, Name: "Footer Menu", Slug: "footer", LanguageCode: "en", UpdatedAt: now},
		{ID: 3, Name: "Custom Menu", Slug: "custom", LanguageCode: "ru", UpdatedAt: now},
	}
	got := convertMenuListItems(menus)
	if len(got) != 3 {
		t.Fatalf("convertMenuListItems() returned %d items; want 3", len(got))
	}
	// "main" and "footer" slugs should be protected
	if !got[0].IsProtected {
		t.Error("got[0] (main) should be protected")
	}
	if !got[1].IsProtected {
		t.Error("got[1] (footer) should be protected")
	}
	if got[2].IsProtected {
		t.Error("got[2] (custom) should not be protected")
	}
	if got[2].LanguageCode != "ru" {
		t.Errorf("got[2].LanguageCode = %q; want \"ru\"", got[2].LanguageCode)
	}
}

func TestConvertMenuPages(t *testing.T) {
	pages := []store.Page{
		{ID: 1, Title: "Home", Slug: "home"},
		{ID: 2, Title: "About", Slug: "about"},
	}
	got := convertMenuPages(pages)
	if len(got) != 2 {
		t.Fatalf("convertMenuPages() returned %d items; want 2", len(got))
	}
	if got[0].ID != 1 || got[0].Title != "Home" || got[0].Slug != "home" {
		t.Errorf("got[0] = %+v", got[0])
	}
}

// TestBreadcrumbFunctions tests all breadcrumb helper functions.
func TestBreadcrumbFunctions(t *testing.T) {
	lang := "en"

	tests := []struct {
		name       string
		fn         func() []any
		wantLen    int
		lastActive bool
	}{
		{
			name:       "dashboardBreadcrumbs",
			fn:         func() []any { bc := dashboardBreadcrumbs(lang); return toAny(bc) },
			wantLen:    1,
			lastActive: true,
		},
		{
			name:       "tagsBreadcrumbs",
			fn:         func() []any { bc := tagsBreadcrumbs(lang); return toAny(bc) },
			wantLen:    2,
			lastActive: true,
		},
		{
			name:       "tagFormBreadcrumbs_new",
			fn:         func() []any { bc := tagFormBreadcrumbs(lang, false); return toAny(bc) },
			wantLen:    3,
			lastActive: true,
		},
		{
			name:       "tagFormBreadcrumbs_edit",
			fn:         func() []any { bc := tagFormBreadcrumbs(lang, true); return toAny(bc) },
			wantLen:    3,
			lastActive: true,
		},
		{
			name:       "categoriesBreadcrumbs",
			fn:         func() []any { bc := categoriesBreadcrumbs(lang); return toAny(bc) },
			wantLen:    2,
			lastActive: true,
		},
		{
			name:       "categoryFormBreadcrumbs_new",
			fn:         func() []any { bc := categoryFormBreadcrumbs(lang, false); return toAny(bc) },
			wantLen:    3,
			lastActive: true,
		},
		{
			name:       "eventsBreadcrumbs",
			fn:         func() []any { bc := eventsBreadcrumbs(lang); return toAny(bc) },
			wantLen:    2,
			lastActive: true,
		},
		{
			name:       "usersBreadcrumbs",
			fn:         func() []any { bc := usersBreadcrumbs(lang); return toAny(bc) },
			wantLen:    2,
			lastActive: true,
		},
		{
			name:       "userFormBreadcrumbs_new",
			fn:         func() []any { bc := userFormBreadcrumbs(lang, false); return toAny(bc) },
			wantLen:    3,
			lastActive: true,
		},
		{
			name:       "redirectsBreadcrumbs",
			fn:         func() []any { bc := redirectsBreadcrumbs(lang); return toAny(bc) },
			wantLen:    2,
			lastActive: true,
		},
		{
			name:       "menusBreadcrumbs",
			fn:         func() []any { bc := menusBreadcrumbs(lang); return toAny(bc) },
			wantLen:    2,
			lastActive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if len(got) != tt.wantLen {
				t.Errorf("len() = %d; want %d", len(got), tt.wantLen)
			}
		})
	}
}

// toAny converts a render.Breadcrumb slice to []any for generic length checking.
func toAny[T any](s []T) []any {
	result := make([]any, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}

func TestTagEditBreadcrumbs(t *testing.T) {
	bc := tagEditBreadcrumbs("en", "Go", 42)
	if len(bc) != 3 {
		t.Fatalf("tagEditBreadcrumbs() returned %d items; want 3", len(bc))
	}
	if bc[2].Label != "Go" || !bc[2].Active {
		t.Errorf("last breadcrumb = {%q, Active: %v}; want {\"Go\", true}", bc[2].Label, bc[2].Active)
	}
}

func TestCategoryEditBreadcrumbs(t *testing.T) {
	bc := categoryEditBreadcrumbs("en", "Tech", 10)
	if len(bc) != 3 {
		t.Fatalf("categoryEditBreadcrumbs() returned %d items; want 3", len(bc))
	}
	if bc[2].Label != "Tech" || !bc[2].Active {
		t.Errorf("last breadcrumb = {%q, Active: %v}; want {\"Tech\", true}", bc[2].Label, bc[2].Active)
	}
}

func TestUserEditBreadcrumbs(t *testing.T) {
	bc := userEditBreadcrumbs("en", "John", 5)
	if len(bc) != 3 {
		t.Fatalf("userEditBreadcrumbs() returned %d items; want 3", len(bc))
	}
	if bc[2].Label != "John" || !bc[2].Active {
		t.Errorf("last breadcrumb = {%q, Active: %v}; want {\"John\", true}", bc[2].Label, bc[2].Active)
	}
}

func TestRedirectEditBreadcrumbs(t *testing.T) {
	bc := redirectEditBreadcrumbs("en", "/old-path", 7)
	if len(bc) != 3 {
		t.Fatalf("redirectEditBreadcrumbs() returned %d items; want 3", len(bc))
	}
	if bc[2].Label != "/old-path" || !bc[2].Active {
		t.Errorf("last breadcrumb = {%q, Active: %v}; want {\"/old-path\", true}", bc[2].Label, bc[2].Active)
	}
}

func TestMenuEditBreadcrumbs(t *testing.T) {
	bc := menuEditBreadcrumbs("en", "Main Menu", 1)
	if len(bc) != 3 {
		t.Fatalf("menuEditBreadcrumbs() returned %d items; want 3", len(bc))
	}
	if bc[2].Label != "Main Menu" || !bc[2].Active {
		t.Errorf("last breadcrumb = {%q, Active: %v}; want {\"Main Menu\", true}", bc[2].Label, bc[2].Active)
	}
}

func TestConvertPagination(t *testing.T) {
	p := AdminPagination{
		CurrentPage: 2,
		TotalPages:  5,
		TotalItems:  100,
		HasFirst:    true,
		HasPrev:     true,
		HasNext:     true,
		HasLast:     true,
		BaseURL:     "/admin/pages",
		Pages: []AdminPaginationPage{
			{Number: 1, URL: "/admin/pages?page=1", IsCurrent: false},
			{Number: 2, URL: "/admin/pages?page=2", IsCurrent: true},
			{Number: 3, URL: "/admin/pages?page=3", IsCurrent: false},
		},
	}
	got := convertPagination(p)
	if got.CurrentPage != 2 || got.TotalPages != 5 || got.TotalItems != 100 {
		t.Errorf("basic fields: CurrentPage=%d, TotalPages=%d, TotalItems=%d", got.CurrentPage, got.TotalPages, got.TotalItems)
	}
	if !got.HasFirst || !got.HasPrev || !got.HasNext || !got.HasLast {
		t.Error("navigation flags should all be true")
	}
	if len(got.Pages) != 3 {
		t.Fatalf("len(Pages) = %d; want 3", len(got.Pages))
	}
	if !got.Pages[1].IsCurrent {
		t.Error("Pages[1].IsCurrent should be true")
	}
}
