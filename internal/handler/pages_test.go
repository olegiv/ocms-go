// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewPagesHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewPagesHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewPagesHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
	if h.sessionManager != sm {
		t.Error("sessionManager not set correctly")
	}
}

func TestPagesHandlerSetDispatcher(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewPagesHandler(db, nil, sm)
	if h.dispatcher != nil {
		t.Error("dispatcher should be nil initially")
	}

	// SetDispatcher with nil (just testing it doesn't panic)
	h.SetDispatcher(nil)
	if h.dispatcher != nil {
		t.Error("dispatcher should still be nil")
	}
}

func TestPagesListData(t *testing.T) {
	data := PagesListData{
		Pages:          []store.Page{{ID: 1, Title: "Test"}},
		PageTags:       make(map[int64][]store.Tag),
		PageCategories: make(map[int64][]store.Category),
	}

	if len(data.Pages) != 1 {
		t.Errorf("Pages length = %d, want 1", len(data.Pages))
	}
	if data.PageTags == nil {
		t.Error("PageTags should be initialized")
	}
	if data.PageCategories == nil {
		t.Error("PageCategories should be initialized")
	}
}

// TestPagesValidateSlug tests slug validation for pages
func TestPagesValidateSlug(t *testing.T) {
	db, _ := testHandlerSetup(t)

	// Create test user
	user := createTestAdminUser(t, db)

	// Create an existing page
	queries := store.New(db)
	_, err := queries.CreatePage(context.Background(), store.CreatePageParams{
		Title:    "Existing Page",
		Slug:     "existing-page",
		Body:     "Content",
		Status:   "published",
		AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("failed to create test page: %v", err)
	}

	t.Run("slug validation for creation", func(t *testing.T) {
		// Test valid slug
		checkExists := func() (int64, error) {
			return queries.SlugExists(context.Background(), "new-page")
		}
		result := ValidateSlugWithChecker("new-page", checkExists)
		if result != "" {
			t.Errorf("valid slug should return empty string, got %q", result)
		}

		// Test existing slug
		checkExistsExisting := func() (int64, error) {
			return queries.SlugExists(context.Background(), "existing-page")
		}
		result = ValidateSlugWithChecker("existing-page", checkExistsExisting)
		if result != "Slug already exists" {
			t.Errorf("existing slug should return error, got %q", result)
		}
	})
}

// TestParsePageFormData tests parsing form data for pages
func TestParsePageForm(t *testing.T) {
	tests := []struct {
		name      string
		formData  url.Values
		wantTitle string
		wantSlug  string
	}{
		{
			name: "basic form",
			formData: url.Values{
				"title":  {"My Page"},
				"slug":   {"my-page"},
				"body":   {"<p>Hello</p>"},
				"status": {"draft"},
			},
			wantTitle: "My Page",
			wantSlug:  "my-page",
		},
		{
			name: "form with meta fields",
			formData: url.Values{
				"title":            {"SEO Page"},
				"slug":             {"seo-page"},
				"body":             {"Content"},
				"status":           {"published"},
				"meta_title":       {"Custom Title"},
				"meta_description": {"Description"},
			},
			wantTitle: "SEO Page",
			wantSlug:  "seo-page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(tt.formData.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			if err := req.ParseForm(); err != nil {
				t.Fatalf("failed to parse form: %v", err)
			}

			if req.FormValue("title") != tt.wantTitle {
				t.Errorf("title = %q, want %q", req.FormValue("title"), tt.wantTitle)
			}
			if req.FormValue("slug") != tt.wantSlug {
				t.Errorf("slug = %q, want %q", req.FormValue("slug"), tt.wantSlug)
			}
		})
	}
}

// TestPageStatusValidation tests page status validation
func TestPageStatusValidation(t *testing.T) {
	validStatuses := map[string]bool{
		"draft":     true,
		"published": true,
	}

	tests := []struct {
		status string
		valid  bool
	}{
		{"draft", true},
		{"published", true},
		{"pending", false},
		{"archived", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			isValid := validStatuses[tt.status]
			if isValid != tt.valid {
				t.Errorf("status %q valid = %v, want %v", tt.status, isValid, tt.valid)
			}
		})
	}
}

// TestPageScheduling tests page scheduling logic
func TestPageScheduling(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		scheduledAt  *time.Time
		status       string
		shouldUpdate bool
	}{
		{
			name:         "no schedule",
			scheduledAt:  nil,
			status:       "draft",
			shouldUpdate: false,
		},
		{
			name:         "future schedule",
			scheduledAt:  func() *time.Time { t := now.Add(time.Hour); return &t }(),
			status:       "draft",
			shouldUpdate: false,
		},
		{
			name:         "past schedule should publish",
			scheduledAt:  func() *time.Time { t := now.Add(-time.Hour); return &t }(),
			status:       "draft",
			shouldUpdate: true,
		},
		{
			name:         "already published",
			scheduledAt:  nil,
			status:       "published",
			shouldUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsUpdate := tt.scheduledAt != nil && tt.scheduledAt.Before(now) && tt.status != "published"
			if needsUpdate != tt.shouldUpdate {
				t.Errorf("shouldUpdate = %v, want %v", needsUpdate, tt.shouldUpdate)
			}
		})
	}
}

// TestPageCreateQuery tests creating a page through store
func TestPageCreateQuery(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	t.Run("create basic page", func(t *testing.T) {
		page, err := queries.CreatePage(context.Background(), store.CreatePageParams{
			Title:    "New Page",
			Slug:     "new-page",
			Body:     "<p>Hello World</p>",
			Status:   "draft",
			AuthorID: user.ID,
		})
		if err != nil {
			t.Fatalf("CreatePage failed: %v", err)
		}

		if page.Title != "New Page" {
			t.Errorf("Title = %q, want %q", page.Title, "New Page")
		}
		if page.Slug != "new-page" {
			t.Errorf("Slug = %q, want %q", page.Slug, "new-page")
		}
		if page.Status != "draft" {
			t.Errorf("Status = %q, want %q", page.Status, "draft")
		}
	})

	t.Run("create page with meta", func(t *testing.T) {
		page, err := queries.CreatePage(context.Background(), store.CreatePageParams{
			Title:           "SEO Page",
			Slug:            "seo-page",
			Body:            "Content",
			Status:          "published",
			AuthorID:        user.ID,
			MetaTitle:       "Custom Title",
			MetaDescription: "Custom Description",
		})
		if err != nil {
			t.Fatalf("CreatePage failed: %v", err)
		}

		if page.MetaTitle != "Custom Title" {
			t.Errorf("MetaTitle = %q, want %q", page.MetaTitle, "Custom Title")
		}
		if page.MetaDescription != "Custom Description" {
			t.Errorf("MetaDescription = %q, want %q", page.MetaDescription, "Custom Description")
		}
	})
}

// TestPageListQuery tests listing pages
func TestPageListQuery(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	// Create test pages
	for i := 1; i <= 15; i++ {
		_, err := queries.CreatePage(context.Background(), store.CreatePageParams{
			Title:    "Page " + string(rune('0'+i%10)),
			Slug:     "page-" + string(rune('a'+i-1)),
			Body:     "Content",
			Status:   "published",
			AuthorID: user.ID,
		})
		if err != nil {
			t.Fatalf("failed to create page %d: %v", i, err)
		}
	}

	t.Run("list all pages", func(t *testing.T) {
		pages, err := queries.ListPages(context.Background(), store.ListPagesParams{
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListPages failed: %v", err)
		}
		if len(pages) != 15 {
			t.Errorf("got %d pages, want 15", len(pages))
		}
	})

	t.Run("list with pagination", func(t *testing.T) {
		pages, err := queries.ListPages(context.Background(), store.ListPagesParams{
			Limit:  10,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListPages failed: %v", err)
		}
		if len(pages) != 10 {
			t.Errorf("got %d pages, want 10", len(pages))
		}
	})

	t.Run("count pages", func(t *testing.T) {
		count, err := queries.CountPages(context.Background())
		if err != nil {
			t.Fatalf("CountPages failed: %v", err)
		}
		if count != 15 {
			t.Errorf("count = %d, want 15", count)
		}
	})
}

// TestPageUpdateQuery tests updating a page
func TestPageUpdateQuery(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	// Create a page to update
	page, err := queries.CreatePage(context.Background(), store.CreatePageParams{
		Title:    "Original Title",
		Slug:     "original-slug",
		Body:     "Original content",
		Status:   "draft",
		AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}

	t.Run("update title and status", func(t *testing.T) {
		_, err := queries.UpdatePage(context.Background(), store.UpdatePageParams{
			ID:     page.ID,
			Title:  "Updated Title",
			Slug:   "original-slug",
			Body:   "Original content",
			Status: "published",
		})
		if err != nil {
			t.Fatalf("UpdatePage failed: %v", err)
		}

		updated, err := queries.GetPageByID(context.Background(), page.ID)
		if err != nil {
			t.Fatalf("GetPageByID failed: %v", err)
		}

		if updated.Title != "Updated Title" {
			t.Errorf("Title = %q, want %q", updated.Title, "Updated Title")
		}
		if updated.Status != "published" {
			t.Errorf("Status = %q, want %q", updated.Status, "published")
		}
	})
}

// TestPageDeleteQuery tests deleting a page
func TestPageDeleteQuery(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	// Create a page to delete
	page, err := queries.CreatePage(context.Background(), store.CreatePageParams{
		Title:    "To Delete",
		Slug:     "to-delete",
		Body:     "Content",
		Status:   "draft",
		AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}

	t.Run("delete page", func(t *testing.T) {
		err := queries.DeletePage(context.Background(), page.ID)
		if err != nil {
			t.Fatalf("DeletePage failed: %v", err)
		}

		_, err = queries.GetPageByID(context.Background(), page.ID)
		if err == nil {
			t.Error("expected error when getting deleted page")
		}
	})
}

// TestPageSlugExists tests slug existence check
func TestPageSlugExists(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	// Create a page
	_, err := queries.CreatePage(context.Background(), store.CreatePageParams{
		Title:    "Existing",
		Slug:     "existing-slug",
		Body:     "Content",
		Status:   "published",
		AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}

	t.Run("slug exists", func(t *testing.T) {
		count, err := queries.SlugExists(context.Background(), "existing-slug")
		if err != nil {
			t.Fatalf("SlugExists failed: %v", err)
		}
		if count == 0 {
			t.Error("expected slug to exist")
		}
	})

	t.Run("slug does not exist", func(t *testing.T) {
		count, err := queries.SlugExists(context.Background(), "nonexistent-slug")
		if err != nil {
			t.Fatalf("SlugExists failed: %v", err)
		}
		if count != 0 {
			t.Error("expected slug to not exist")
		}
	})
}

// TestPageDispatchEvent tests the webhook dispatch helper
func TestPageDispatchEventNilDispatcher(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewPagesHandler(db, nil, sm)

	// Should not panic when dispatcher is nil
	h.dispatchPageEvent(context.Background(), "page.created", store.Page{
		ID:    1,
		Title: "Test",
		Slug:  "test",
	}, "test@example.com")
}
