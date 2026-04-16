// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestStoreCategoryToResponse(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		category store.Category
		want     CategoryResponse
	}{
		{
			name: "category with description",
			category: store.Category{
				ID:          1,
				Name:        "Tech",
				Slug:        "tech",
				Description: sql.NullString{String: "Technology articles", Valid: true},
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			want: CategoryResponse{
				ID:          1,
				Name:        "Tech",
				Slug:        "tech",
				Description: "Technology articles",
			},
		},
		{
			name: "category without description",
			category: store.Category{
				ID:          2,
				Name:        "News",
				Slug:        "news",
				Description: sql.NullString{Valid: false},
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			want: CategoryResponse{
				ID:          2,
				Name:        "News",
				Slug:        "news",
				Description: "",
			},
		},
		{
			name: "category with empty description",
			category: store.Category{
				ID:          3,
				Name:        "Blog",
				Slug:        "blog",
				Description: sql.NullString{String: "", Valid: true},
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			want: CategoryResponse{
				ID:          3,
				Name:        "Blog",
				Slug:        "blog",
				Description: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storeCategoryToResponse(tt.category)

			assertIDNameSlug(t, got.ID, tt.want.ID, got.Name, tt.want.Name, got.Slug, tt.want.Slug)
			if got.Description != tt.want.Description {
				t.Errorf("Description = %q, want %q", got.Description, tt.want.Description)
			}
		})
	}
}

func TestStoreTagToResponse(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		tag  store.Tag
		want TagResponse
	}{
		{
			name: "basic tag",
			tag: store.Tag{
				ID:        1,
				Name:      "golang",
				Slug:      "golang",
				CreatedAt: now,
				UpdatedAt: now,
			},
			want: TagResponse{
				ID:   1,
				Name: "golang",
				Slug: "golang",
			},
		},
		{
			name: "tag with special characters in name",
			tag: store.Tag{
				ID:        2,
				Name:      "C++",
				Slug:      "cpp",
				CreatedAt: now,
				UpdatedAt: now,
			},
			want: TagResponse{
				ID:   2,
				Name: "C++",
				Slug: "cpp",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storeTagToResponse(tt.tag)

			assertIDNameSlug(t, got.ID, tt.want.ID, got.Name, tt.want.Name, got.Slug, tt.want.Slug)
		})
	}
}

func TestValidatePageBodyMarkupPolicy(t *testing.T) {
	t.Run("disabled policy allows suspicious body", func(t *testing.T) {
		errMsg := validatePageBodyMarkupPolicy(`<script>alert(1)</script>`, false)
		if errMsg != "" {
			t.Fatalf("unexpected error: %s", errMsg)
		}
	})

	t.Run("enabled policy blocks suspicious body", func(t *testing.T) {
		errMsg := validatePageBodyMarkupPolicy(`<img src=x onerror="alert(1)">`, true)
		if errMsg == "" {
			t.Fatal("expected validation error")
		}
	})

	t.Run("enabled policy allows clean body", func(t *testing.T) {
		errMsg := validatePageBodyMarkupPolicy(`<p>Hello</p>`, true)
		if errMsg != "" {
			t.Fatalf("unexpected error: %s", errMsg)
		}
	})

	t.Run("enabled policy blocks javascript URI with entity-prefixed whitespace", func(t *testing.T) {
		errMsg := validatePageBodyMarkupPolicy(`<a href="&#x0A;javascript:alert(1)">x</a>`, true)
		if errMsg == "" {
			t.Fatal("expected validation error")
		}
	})

	t.Run("enabled policy allows plain text javascript mention", func(t *testing.T) {
		errMsg := validatePageBodyMarkupPolicy(`<strong>JavaScript:</strong> language`, true)
		if errMsg != "" {
			t.Fatalf("unexpected error: %s", errMsg)
		}
	})
}

func TestSanitizePageBodyForStorage(t *testing.T) {
	raw := `<p>Hello</p><script>alert(1)</script>`

	t.Run("returns raw body when disabled", func(t *testing.T) {
		if got := sanitizePageBodyForStorage(raw, false); got != raw {
			t.Fatalf("sanitizePageBodyForStorage() = %q, want %q", got, raw)
		}
	})

	t.Run("sanitizes body when enabled", func(t *testing.T) {
		got := sanitizePageBodyForStorage(raw, true)
		if strings.Contains(got, "<script") {
			t.Fatalf("sanitizePageBodyForStorage() should strip script tags, got %q", got)
		}
		if !strings.Contains(got, "<p>Hello</p>") {
			t.Fatalf("sanitizePageBodyForStorage() should keep safe markup, got %q", got)
		}
	})
}

func TestResolveTagNames(t *testing.T) {
	db, _ := testSetup(t)
	ctx := context.Background()
	q := store.New(db)

	// Pre-create an existing tag
	createTestTag(t, db, "Existing Tag", "existing-tag")

	t.Run("creates new tag", func(t *testing.T) {
		ids, err := resolveTagNames(ctx, q, []string{"Brand New"}, "en", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 ID, got %d", len(ids))
		}

		// Verify tag was created in DB
		tag, err := q.GetTagBySlug(ctx, "brand-new")
		if err != nil {
			t.Fatalf("tag not found in database: %v", err)
		}
		if tag.Name != "Brand New" {
			t.Errorf("expected name 'Brand New', got %q", tag.Name)
		}
		if ids[0] != tag.ID {
			t.Errorf("returned ID %d doesn't match DB ID %d", ids[0], tag.ID)
		}
	})

	t.Run("finds existing tag by slug", func(t *testing.T) {
		ids, err := resolveTagNames(ctx, q, []string{"Existing Tag"}, "en", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 ID, got %d", len(ids))
		}

		existing, _ := q.GetTagBySlug(ctx, "existing-tag")
		if ids[0] != existing.ID {
			t.Errorf("expected existing tag ID %d, got %d", existing.ID, ids[0])
		}
	})

	t.Run("mixed existing and new", func(t *testing.T) {
		ids, err := resolveTagNames(ctx, q, []string{"Existing Tag", "Another New Tag"}, "en", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("expected 2 IDs, got %d", len(ids))
		}
	})

	t.Run("skips empty and whitespace-only names", func(t *testing.T) {
		ids, err := resolveTagNames(ctx, q, []string{"", "  ", "Valid Tag"}, "en", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 ID (empty names skipped), got %d", len(ids))
		}
	})

	t.Run("empty input returns empty slice", func(t *testing.T) {
		ids, err := resolveTagNames(ctx, q, []string{}, "en", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 0 {
			t.Fatalf("expected 0 IDs, got %d", len(ids))
		}
	})

	t.Run("rejects too many tags", func(t *testing.T) {
		names := make([]string, maxTagsPerRequest+1)
		for i := range names {
			names[i] = "tag"
		}
		_, err := resolveTagNames(ctx, q, names, "en", true)
		if err == nil {
			t.Fatal("expected error for too many tags, got nil")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "too many tags") {
			t.Errorf("expected 'too many tags' error, got: %v", err)
		}
	})

	t.Run("rejects tag name too long", func(t *testing.T) {
		longName := strings.Repeat("a", maxTagNameLength+1)
		_, err := resolveTagNames(ctx, q, []string{longName}, "en", true)
		if err == nil {
			t.Fatal("expected error for long tag name, got nil")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "tag name too long") {
			t.Errorf("expected 'tag name too long' error, got: %v", err)
		}
	})

	t.Run("database error propagates", func(t *testing.T) {
		closedDB, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatalf("failed to open db: %v", err)
		}
		_, _ = closedDB.Exec(`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT, slug TEXT UNIQUE, language_code TEXT NOT NULL DEFAULT 'en', created_at DATETIME, updated_at DATETIME)`)
		closedQ := store.New(closedDB)
		_ = closedDB.Close()

		_, err = resolveTagNames(ctx, closedQ, []string{"Will Fail"}, "en", true)
		if err == nil {
			t.Fatal("expected error from closed database, got nil")
		}
	})

	t.Run("disallows creating new tag without taxonomy permission", func(t *testing.T) {
		_, err := resolveTagNames(ctx, q, []string{"Needs Create"}, "en", false)
		if err == nil {
			t.Fatal("expected permission error, got nil")
		}
		var pe *tagPermissionError
		if !errors.As(err, &pe) {
			t.Fatalf("expected tagPermissionError, got %T (%v)", err, err)
		}
	})

	t.Run("allows resolving existing tag without taxonomy permission", func(t *testing.T) {
		ids, err := resolveTagNames(ctx, q, []string{"Existing Tag"}, "en", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 ID, got %d", len(ids))
		}
	})
}

func TestAPIKeyHasPermission(t *testing.T) {
	t.Run("nil key has no permissions", func(t *testing.T) {
		if apiKeyHasPermission(nil, model.PermissionPagesRead) {
			t.Fatal("expected false for nil key")
		}
	})

	t.Run("matching permission returns true", func(t *testing.T) {
		apiKey := &store.ApiKey{Permissions: `["media:read","pages:read"]`}
		if !apiKeyHasPermission(apiKey, model.PermissionPagesRead) {
			t.Fatal("expected pages:read permission to be detected")
		}
	})

	t.Run("non-matching permission returns false", func(t *testing.T) {
		apiKey := &store.ApiKey{Permissions: `["media:read"]`}
		if apiKeyHasPermission(apiKey, model.PermissionPagesRead) {
			t.Fatal("expected false for missing pages:read permission")
		}
	})
}

func TestStorePageToResponseVideoURL(t *testing.T) {
	page := store.Page{
		ID:         1,
		Title:      "Test Page",
		Slug:       "test-page",
		VideoUrl:   "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		VideoTitle: "My YouTube Video",
	}
	resp := storePageToResponse(page)
	if resp.VideoURL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("VideoURL = %q, want YouTube URL", resp.VideoURL)
	}
	if resp.VideoTitle != "My YouTube Video" {
		t.Errorf("VideoTitle = %q, want %q", resp.VideoTitle, "My YouTube Video")
	}

	// Empty fields should be omitted (empty string)
	page.VideoUrl = ""
	page.VideoTitle = ""
	resp = storePageToResponse(page)
	if resp.VideoURL != "" {
		t.Errorf("VideoURL = %q, want empty for omitempty", resp.VideoURL)
	}
	if resp.VideoTitle != "" {
		t.Errorf("VideoTitle = %q, want empty for omitempty", resp.VideoTitle)
	}
}

func TestStorePageToResponseSummary(t *testing.T) {
	page := store.Page{
		ID:      1,
		Title:   "Test Page",
		Slug:    "test-page",
		Summary: "Brief description of the page",
	}
	resp := storePageToResponse(page)
	if resp.Summary != "Brief description of the page" {
		t.Errorf("Summary = %q, want %q", resp.Summary, "Brief description of the page")
	}

	// Empty summary should be omitted (empty string)
	page.Summary = ""
	resp = storePageToResponse(page)
	if resp.Summary != "" {
		t.Errorf("Summary = %q, want empty for omitempty", resp.Summary)
	}
}

func TestCreatePage_RejectsTooLongSummary(t *testing.T) {
	_, h := testSetup(t)
	longSummary := strings.Repeat("a", maxSummaryLength+1)
	body := `{"title":"Test","slug":"test-long-summary","body":"<p>ok</p>","summary":"` + longSummary + `"}`
	req := newJSONRequest(t, http.MethodPost, "/api/v1/pages", body, nil)
	w := executeHandler(t, h.CreatePage, req)

	assertStatusCode(t, w, http.StatusUnprocessableEntity)
	resp := assertErrorResponse(t, w, "validation_error")
	if got := resp.Error.Details["summary"]; got == "" {
		t.Fatal("expected validation error for summary field, got none")
	}
}

// TestValidateAndTrimSummary_CountsCharactersNotBytes verifies that the
// summary length check uses Unicode character count (runes), not byte count,
// so multilingual summaries are accepted at up to maxSummaryLength characters.
func TestValidateAndTrimSummary_CountsCharactersNotBytes(t *testing.T) {
	// "Привет" (Cyrillic) = 6 runes but 12 bytes in UTF-8.
	// Build a summary with maxSummaryLength runes using multi-byte characters.
	// Each rune here is 2 bytes, so byte length is 2*maxSummaryLength,
	// which would fail a naive len() check but must pass the rune-based check.
	validMultilingual := strings.Repeat("Я", maxSummaryLength)
	w := httptest.NewRecorder()
	trimmed, ok := validateAndTrimSummary(w, validMultilingual)
	if !ok {
		t.Fatalf("multilingual summary with %d runes (%d bytes) was rejected; should be accepted",
			maxSummaryLength, len(validMultilingual))
	}
	if trimmed != validMultilingual {
		t.Errorf("trimmed = %q, want %q", trimmed, validMultilingual)
	}

	// One more rune than allowed must be rejected.
	tooLong := strings.Repeat("Я", maxSummaryLength+1)
	w2 := httptest.NewRecorder()
	if _, ok := validateAndTrimSummary(w2, tooLong); ok {
		t.Fatalf("summary with %d runes should be rejected but was accepted", maxSummaryLength+1)
	}
}

func TestDeduplicateInt64(t *testing.T) {
	tests := []struct {
		name  string
		input []int64
		want  []int64
	}{
		{"no duplicates", []int64{1, 2, 3}, []int64{1, 2, 3}},
		{"with duplicates", []int64{1, 2, 1, 3, 2}, []int64{1, 2, 3}},
		{"all same", []int64{5, 5, 5}, []int64{5}},
		{"empty", []int64{}, []int64{}},
		{"single", []int64{42}, []int64{42}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deduplicateInt64(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCreatePage_RejectsInvalidSlugFormat(t *testing.T) {
	_, h := testSetup(t)
	req := newJSONRequest(t, http.MethodPost, "/api/v1/pages", `{"title":"Test","slug":"/t.co","body":"<p>ok</p>"}`, nil)
	w := executeHandler(t, h.CreatePage, req)

	assertStatusCode(t, w, http.StatusUnprocessableEntity)
	resp := assertErrorResponse(t, w, "validation_error")
	if got := resp.Error.Details["slug"]; got != "Invalid slug format" {
		t.Fatalf("slug error = %q, want %q", got, "Invalid slug format")
	}
}
