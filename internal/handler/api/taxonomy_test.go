// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestValidateNameSlugRequired(t *testing.T) {
	tests := []struct {
		name       string
		inputName  string
		inputSlug  string
		wantErrors map[string]string
	}{
		{
			name:       "both valid",
			inputName:  "Test Name",
			inputSlug:  "test-slug",
			wantErrors: map[string]string{},
		},
		{
			name:      "missing name",
			inputName: "",
			inputSlug: "test-slug",
			wantErrors: map[string]string{
				"name": "Name is required",
			},
		},
		{
			name:      "missing slug",
			inputName: "Test Name",
			inputSlug: "",
			wantErrors: map[string]string{
				"slug": "Slug is required",
			},
		},
		{
			name:      "both missing",
			inputName: "",
			inputSlug: "",
			wantErrors: map[string]string{
				"name": "Name is required",
				"slug": "Slug is required",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateNameSlugRequired(tt.inputName, tt.inputSlug)

			if len(got) != len(tt.wantErrors) {
				t.Errorf("validateNameSlugRequired() returned %d errors, want %d", len(got), len(tt.wantErrors))
				return
			}

			for key, wantMsg := range tt.wantErrors {
				if gotMsg, ok := got[key]; !ok {
					t.Errorf("validateNameSlugRequired() missing error for key %q", key)
				} else if gotMsg != wantMsg {
					t.Errorf("validateNameSlugRequired()[%q] = %q, want %q", key, gotMsg, wantMsg)
				}
			}
		})
	}
}

func TestApplyOptionalNameUpdate(t *testing.T) {
	tests := []struct {
		name        string
		reqName     *string
		currentName string
		wantName    string
	}{
		{
			name:        "nil request name",
			reqName:     nil,
			currentName: "Original",
			wantName:    "Original",
		},
		{
			name:        "empty request name",
			reqName:     strPtr(""),
			currentName: "Original",
			wantName:    "Original",
		},
		{
			name:        "valid request name",
			reqName:     strPtr("New Name"),
			currentName: "Original",
			wantName:    "New Name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := tt.currentName
			applyOptionalNameUpdate(tt.reqName, &current)

			if current != tt.wantName {
				t.Errorf("applyOptionalNameUpdate() currentName = %q, want %q", current, tt.wantName)
			}
		})
	}
}

func TestApplyOptionalSlugUpdate(t *testing.T) {
	tests := []struct {
		name        string
		reqSlug     *string
		currentSlug string
		checkResult bool
		wantOK      bool
		wantSlug    string
	}{
		{
			name:        "nil request slug",
			reqSlug:     nil,
			currentSlug: "original-slug",
			checkResult: true,
			wantOK:      true,
			wantSlug:    "original-slug",
		},
		{
			name:        "empty request slug",
			reqSlug:     strPtr(""),
			currentSlug: "original-slug",
			checkResult: true,
			wantOK:      true,
			wantSlug:    "original-slug",
		},
		{
			name:        "valid slug with successful check",
			reqSlug:     strPtr("new-slug"),
			currentSlug: "original-slug",
			checkResult: true,
			wantOK:      true,
			wantSlug:    "new-slug",
		},
		{
			name:        "valid slug with failed check",
			reqSlug:     strPtr("duplicate-slug"),
			currentSlug: "original-slug",
			checkResult: false,
			wantOK:      false,
			wantSlug:    "original-slug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := tt.currentSlug
			checkCalled := false

			checkSlug := func() bool {
				checkCalled = true
				return tt.checkResult
			}

			got := applyOptionalSlugUpdate(tt.reqSlug, &current, checkSlug)

			if got != tt.wantOK {
				t.Errorf("applyOptionalSlugUpdate() = %v, want %v", got, tt.wantOK)
			}
			if current != tt.wantSlug {
				t.Errorf("applyOptionalSlugUpdate() currentSlug = %q, want %q", current, tt.wantSlug)
			}

			// Check function should only be called when slug is provided and non-empty
			shouldCallCheck := tt.reqSlug != nil && *tt.reqSlug != ""
			if checkCalled != shouldCallCheck {
				t.Errorf("applyOptionalSlugUpdate() checkSlug called = %v, want %v", checkCalled, shouldCallCheck)
			}
		})
	}
}

func TestCategoryRowToResponse(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		row  store.GetCategoryUsageCountsRow
		want CategoryAPIResponse
	}{
		{
			name: "basic category without optional fields",
			row: store.GetCategoryUsageCountsRow{
				ID:          1,
				Name:        "Test Category",
				Slug:        "test-category",
				Position:    0,
				UsageCount:  5,
				CreatedAt:   now,
				UpdatedAt:   now,
				Description: sql.NullString{Valid: false},
				ParentID:    sql.NullInt64{Valid: false},
			},
			want: CategoryAPIResponse{
				ID:        1,
				Name:      "Test Category",
				Slug:      "test-category",
				Position:  0,
				PageCount: 5,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		{
			name: "category with description",
			row: store.GetCategoryUsageCountsRow{
				ID:          2,
				Name:        "With Description",
				Slug:        "with-description",
				Position:    1,
				UsageCount:  10,
				CreatedAt:   now,
				UpdatedAt:   now,
				Description: sql.NullString{String: "A description", Valid: true},
				ParentID:    sql.NullInt64{Valid: false},
			},
			want: CategoryAPIResponse{
				ID:          2,
				Name:        "With Description",
				Slug:        "with-description",
				Description: "A description",
				Position:    1,
				PageCount:   10,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
		{
			name: "category with parent",
			row: store.GetCategoryUsageCountsRow{
				ID:          3,
				Name:        "Child Category",
				Slug:        "child-category",
				Position:    2,
				UsageCount:  3,
				CreatedAt:   now,
				UpdatedAt:   now,
				Description: sql.NullString{Valid: false},
				ParentID:    sql.NullInt64{Int64: 1, Valid: true},
			},
			want: CategoryAPIResponse{
				ID:        3,
				Name:      "Child Category",
				Slug:      "child-category",
				ParentID:  int64Ptr(1),
				Position:  2,
				PageCount: 3,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := categoryRowToResponse(tt.row)

			assertIDNameSlug(t, got.ID, tt.want.ID, got.Name, tt.want.Name, got.Slug, tt.want.Slug)
			if got.Description != tt.want.Description {
				t.Errorf("categoryRowToResponse().Description = %v, want %v", got.Description, tt.want.Description)
			}
			if got.Position != tt.want.Position {
				t.Errorf("categoryRowToResponse().Position = %v, want %v", got.Position, tt.want.Position)
			}
			if got.PageCount != tt.want.PageCount {
				t.Errorf("categoryRowToResponse().PageCount = %v, want %v", got.PageCount, tt.want.PageCount)
			}

			// Check ParentID pointer
			if (got.ParentID == nil) != (tt.want.ParentID == nil) {
				t.Errorf("categoryRowToResponse().ParentID nil = %v, want nil = %v", got.ParentID == nil, tt.want.ParentID == nil)
			} else if got.ParentID != nil && *got.ParentID != *tt.want.ParentID {
				t.Errorf("categoryRowToResponse().ParentID = %v, want %v", *got.ParentID, *tt.want.ParentID)
			}
		})
	}
}

func TestBuildCategoryTree(t *testing.T) {
	now := time.Now()

	t.Run("empty list", func(t *testing.T) {
		result := buildCategoryTree([]store.GetCategoryUsageCountsRow{})
		if len(result) != 0 {
			t.Errorf("buildCategoryTree() with empty input should return empty slice, got %v", result)
		}
	})

	t.Run("single root category", func(t *testing.T) {
		categories := []store.GetCategoryUsageCountsRow{
			{ID: 1, Name: "Root", Slug: "root", ParentID: sql.NullInt64{Valid: false}, CreatedAt: now, UpdatedAt: now},
		}

		result := buildCategoryTree(categories)

		if len(result) != 1 {
			t.Fatalf("buildCategoryTree() returned %d roots, want 1", len(result))
		}
		if result[0].ID != 1 {
			t.Errorf("buildCategoryTree()[0].ID = %v, want 1", result[0].ID)
		}
		if len(result[0].Children) != 0 {
			t.Errorf("buildCategoryTree()[0].Children should be empty, got %d", len(result[0].Children))
		}
	})

	t.Run("parent with children", func(t *testing.T) {
		categories := []store.GetCategoryUsageCountsRow{
			{ID: 1, Name: "Parent", Slug: "parent", ParentID: sql.NullInt64{Valid: false}, CreatedAt: now, UpdatedAt: now},
			{ID: 2, Name: "Child 1", Slug: "child-1", ParentID: sql.NullInt64{Int64: 1, Valid: true}, CreatedAt: now, UpdatedAt: now},
			{ID: 3, Name: "Child 2", Slug: "child-2", ParentID: sql.NullInt64{Int64: 1, Valid: true}, CreatedAt: now, UpdatedAt: now},
		}

		result := buildCategoryTree(categories)

		if len(result) != 1 {
			t.Fatalf("buildCategoryTree() returned %d roots, want 1", len(result))
		}
		if result[0].ID != 1 {
			t.Errorf("buildCategoryTree()[0].ID = %v, want 1", result[0].ID)
		}
		if len(result[0].Children) != 2 {
			t.Fatalf("buildCategoryTree()[0].Children has %d items, want 2", len(result[0].Children))
		}

		childIDs := map[int64]bool{}
		for _, child := range result[0].Children {
			childIDs[child.ID] = true
		}
		if !childIDs[2] || !childIDs[3] {
			t.Errorf("buildCategoryTree() children IDs = %v, want {2, 3}", childIDs)
		}
	})

	t.Run("multiple roots with nested children", func(t *testing.T) {
		categories := []store.GetCategoryUsageCountsRow{
			{ID: 1, Name: "Root 1", Slug: "root-1", ParentID: sql.NullInt64{Valid: false}, CreatedAt: now, UpdatedAt: now},
			{ID: 2, Name: "Root 2", Slug: "root-2", ParentID: sql.NullInt64{Valid: false}, CreatedAt: now, UpdatedAt: now},
			{ID: 3, Name: "Child of 1", Slug: "child-of-1", ParentID: sql.NullInt64{Int64: 1, Valid: true}, CreatedAt: now, UpdatedAt: now},
			{ID: 4, Name: "Grandchild", Slug: "grandchild", ParentID: sql.NullInt64{Int64: 3, Valid: true}, CreatedAt: now, UpdatedAt: now},
		}

		result := buildCategoryTree(categories)

		if len(result) != 2 {
			t.Fatalf("buildCategoryTree() returned %d roots, want 2", len(result))
		}

		// Find root 1
		var root1 *CategoryAPIResponse
		for _, r := range result {
			if r.ID == 1 {
				root1 = r
				break
			}
		}
		if root1 == nil {
			t.Fatal("buildCategoryTree() missing root with ID 1")
		}

		if len(root1.Children) != 1 {
			t.Fatalf("Root 1 should have 1 child, got %d", len(root1.Children))
		}
		if root1.Children[0].ID != 3 {
			t.Errorf("Root 1's child ID = %v, want 3", root1.Children[0].ID)
		}
		if len(root1.Children[0].Children) != 1 {
			t.Fatalf("Child 3 should have 1 child, got %d", len(root1.Children[0].Children))
		}
		if root1.Children[0].Children[0].ID != 4 {
			t.Errorf("Grandchild ID = %v, want 4", root1.Children[0].Children[0].ID)
		}
	})

	t.Run("orphan with missing parent treated as root", func(t *testing.T) {
		categories := []store.GetCategoryUsageCountsRow{
			{ID: 1, Name: "Orphan", Slug: "orphan", ParentID: sql.NullInt64{Int64: 999, Valid: true}, CreatedAt: now, UpdatedAt: now},
		}

		result := buildCategoryTree(categories)

		if len(result) != 1 {
			t.Fatalf("buildCategoryTree() returned %d roots, want 1 (orphan as root)", len(result))
		}
		if result[0].ID != 1 {
			t.Errorf("buildCategoryTree()[0].ID = %v, want 1", result[0].ID)
		}
	})
}

// Helper functions for tests
func strPtr(s string) *string {
	return &s
}

func int64Ptr(i int64) *int64 {
	return &i
}

// ============================================================================
// Integration Tests for Tag Handlers
// ============================================================================

func TestListTags(t *testing.T) {
	db, h := testSetup(t)

	t.Run("empty list", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/tags", nil)
		w := executeHandler(t, h.ListTags, req)

		assertStatusCode(t, w, http.StatusOK)

		_, meta := unmarshalList[TagAPIResponse](t, w)
		if meta == nil || meta.Total != 0 {
			t.Errorf("expected total 0, got %v", meta)
		}
	})

	t.Run("with tags", func(t *testing.T) {
		createTestTag(t, db, "Tag One", "tag-one")
		createTestTag(t, db, "Tag Two", "tag-two")

		req := newGetRequest(t, "/api/v1/tags", nil)
		w := executeHandler(t, h.ListTags, req)

		assertStatusCode(t, w, http.StatusOK)

		data, meta := unmarshalList[TagAPIResponse](t, w)
		if len(data) != 2 {
			t.Errorf("expected 2 tags, got %d", len(data))
		}
		if meta.Total != 2 {
			t.Errorf("expected total 2, got %d", meta.Total)
		}
	})

	t.Run("with pagination", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/tags?page=1&per_page=1", nil)
		w := executeHandler(t, h.ListTags, req)

		assertStatusCode(t, w, http.StatusOK)

		data, meta := unmarshalList[TagAPIResponse](t, w)
		if len(data) != 1 {
			t.Errorf("expected 1 tag per page, got %d", len(data))
		}
		if meta.PerPage != 1 {
			t.Errorf("expected per_page 1, got %d", meta.PerPage)
		}
	})
}

func TestGetTag(t *testing.T) {
	db, h := testSetup(t)

	tag := createTestTag(t, db, "Test Tag", "test-tag")

	t.Run("existing tag", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/tags/1", map[string]string{"id": "1"})
		w := executeHandler(t, h.GetTag, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[TagAPIResponse](t, w)
		if data.ID != tag.ID {
			t.Errorf("expected ID %d, got %d", tag.ID, data.ID)
		}
		if data.Name != "Test Tag" {
			t.Errorf("expected name 'Test Tag', got %q", data.Name)
		}
	})

	t.Run("non-existent tag", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/tags/999", map[string]string{"id": "999"})
		w := executeHandler(t, h.GetTag, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})

	t.Run("invalid tag ID", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/tags/invalid", map[string]string{"id": "invalid"})
		w := executeHandler(t, h.GetTag, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestRequireEntityByID_InternalError(t *testing.T) {
	// Create a database and immediately close it to trigger internal error
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create minimal schema
	_, err = db.Exec(`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT, slug TEXT, language_id INTEGER, created_at DATETIME, updated_at DATETIME)`)
	if err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	h := NewHandler(db)

	// Close the database to cause internal errors on queries
	_ = db.Close()

	t.Run("database error returns internal error", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/tags/1", map[string]string{"id": "1"})
		w := executeHandler(t, h.GetTag, req)

		assertStatusCode(t, w, http.StatusInternalServerError)

		resp := assertErrorResponse(t, w, "internal_error")
		if resp.Error.Message != "Failed to retrieve tag" {
			t.Errorf("expected message 'Failed to retrieve tag', got %q", resp.Error.Message)
		}
	})

	t.Run("database error on category returns internal error", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/categories/1", map[string]string{"id": "1"})
		w := executeHandler(t, h.GetCategory, req)

		assertStatusCode(t, w, http.StatusInternalServerError)

		resp := assertErrorResponse(t, w, "internal_error")
		if resp.Error.Message != "Failed to retrieve category" {
			t.Errorf("expected message 'Failed to retrieve category', got %q", resp.Error.Message)
		}
	})
}

func TestCreateTag(t *testing.T) {
	db, h := testSetup(t)

	t.Run("valid tag", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPost, "/api/v1/tags", `{"name": "New Tag", "slug": "new-tag"}`, nil)
		w := executeHandler(t, h.CreateTag, req)

		assertStatusCode(t, w, http.StatusCreated)

		data := unmarshalData[TagAPIResponse](t, w)
		if data.Name != "New Tag" {
			t.Errorf("expected name 'New Tag', got %q", data.Name)
		}
		if data.Slug != "new-tag" {
			t.Errorf("expected slug 'new-tag', got %q", data.Slug)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPost, "/api/v1/tags", `{"slug": "missing-name"}`, nil)
		w := executeHandler(t, h.CreateTag, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("missing slug", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPost, "/api/v1/tags", `{"name": "Missing Slug"}`, nil)
		w := executeHandler(t, h.CreateTag, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("duplicate slug", func(t *testing.T) {
		createTestTag(t, db, "Existing", "existing-slug")

		req := newJSONRequest(t, http.MethodPost, "/api/v1/tags", `{"name": "Another", "slug": "existing-slug"}`, nil)
		w := executeHandler(t, h.CreateTag, req)

		// Duplicate slug returns 422 Unprocessable Entity with validation error
		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPost, "/api/v1/tags", `not valid json`, nil)
		w := executeHandler(t, h.CreateTag, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestUpdateTag(t *testing.T) {
	db, h := testSetup(t)

	tag := createTestTag(t, db, "Original", "original-slug")

	t.Run("update name", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPut, "/api/v1/tags/1", `{"name": "Updated Name"}`, map[string]string{"id": fmt.Sprintf("%d", tag.ID)})
		w := executeHandler(t, h.UpdateTag, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[TagAPIResponse](t, w)
		if data.Name != "Updated Name" {
			t.Errorf("expected name 'Updated Name', got %q", data.Name)
		}
		if data.Slug != "original-slug" {
			t.Errorf("expected slug to remain 'original-slug', got %q", data.Slug)
		}
	})

	t.Run("update slug", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPut, "/api/v1/tags/1", `{"slug": "updated-slug"}`, map[string]string{"id": fmt.Sprintf("%d", tag.ID)})
		w := executeHandler(t, h.UpdateTag, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[TagAPIResponse](t, w)
		if data.Slug != "updated-slug" {
			t.Errorf("expected slug 'updated-slug', got %q", data.Slug)
		}
	})

	t.Run("non-existent tag", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPut, "/api/v1/tags/999", `{"name": "Update"}`, map[string]string{"id": "999"})
		w := executeHandler(t, h.UpdateTag, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPut, "/api/v1/tags/1", `invalid`, map[string]string{"id": fmt.Sprintf("%d", tag.ID)})
		w := executeHandler(t, h.UpdateTag, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestDeleteTag(t *testing.T) {
	db, h := testSetup(t)

	t.Run("delete existing tag", func(t *testing.T) {
		tag := createTestTag(t, db, "To Delete", "to-delete")

		req := newDeleteRequest(t, "/api/v1/tags/1", map[string]string{"id": fmt.Sprintf("%d", tag.ID)})
		w := executeHandler(t, h.DeleteTag, req)

		assertStatusCode(t, w, http.StatusNoContent)

		// Verify tag is deleted
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM tags WHERE id = ?", tag.ID).Scan(&count)
		if count != 0 {
			t.Error("tag should be deleted")
		}
	})

	t.Run("delete non-existent tag", func(t *testing.T) {
		req := newDeleteRequest(t, "/api/v1/tags/999", map[string]string{"id": "999"})
		w := executeHandler(t, h.DeleteTag, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})
}

// ============================================================================
// Integration Tests for Category Handlers
// ============================================================================

func TestListCategories(t *testing.T) {
	db, h := testSetup(t)

	t.Run("empty list", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/categories", nil)
		w := executeHandler(t, h.ListCategories, req)

		assertStatusCode(t, w, http.StatusOK)

		_, meta := unmarshalList[CategoryAPIResponse](t, w)
		if meta == nil || meta.Total != 0 {
			t.Errorf("expected total 0, got %v", meta)
		}
	})

	t.Run("with categories as tree", func(t *testing.T) {
		parent := createTestCategory(t, db, "Parent", "parent", nil)
		parentID := parent.ID
		createTestCategory(t, db, "Child", "child", &parentID)

		req := newGetRequest(t, "/api/v1/categories", nil)
		w := executeHandler(t, h.ListCategories, req)

		assertStatusCode(t, w, http.StatusOK)

		// Tree response uses pointers for nested children
		var resp struct {
			Data []*CategoryAPIResponse `json:"data"`
			Meta *Meta                  `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		// Should return tree structure (1 root with 1 child)
		if len(resp.Data) != 1 {
			t.Errorf("expected 1 root category, got %d", len(resp.Data))
		}
		if resp.Meta.Total != 2 {
			t.Errorf("expected total 2, got %d", resp.Meta.Total)
		}
		if len(resp.Data[0].Children) != 1 {
			t.Errorf("expected 1 child, got %d", len(resp.Data[0].Children))
		}
	})

	t.Run("flat list", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/categories?flat=true", nil)
		w := executeHandler(t, h.ListCategories, req)

		assertStatusCode(t, w, http.StatusOK)

		data, _ := unmarshalList[CategoryAPIResponse](t, w)
		// Flat list should return all categories without nesting
		if len(data) != 2 {
			t.Errorf("expected 2 categories in flat list, got %d", len(data))
		}
	})
}

func TestGetCategory(t *testing.T) {
	db, h := testSetup(t)

	cat := createTestCategory(t, db, "Test Category", "test-category", nil)

	t.Run("existing category", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/categories/1", map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := executeHandler(t, h.GetCategory, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if data.ID != cat.ID {
			t.Errorf("expected ID %d, got %d", cat.ID, data.ID)
		}
		if data.Name != "Test Category" {
			t.Errorf("expected name 'Test Category', got %q", data.Name)
		}
	})

	t.Run("category with children", func(t *testing.T) {
		catID := cat.ID
		createTestCategory(t, db, "Child Cat", "child-cat", &catID)

		req := newGetRequest(t, "/api/v1/categories/1", map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := executeHandler(t, h.GetCategory, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if len(data.Children) != 1 {
			t.Errorf("expected 1 child, got %d", len(data.Children))
		}
	})

	t.Run("non-existent category", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/categories/999", map[string]string{"id": "999"})
		w := executeHandler(t, h.GetCategory, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})

	t.Run("invalid category ID", func(t *testing.T) {
		req := newGetRequest(t, "/api/v1/categories/invalid", map[string]string{"id": "invalid"})
		w := executeHandler(t, h.GetCategory, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestCreateCategory(t *testing.T) {
	db, h := testSetup(t)

	t.Run("valid category", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPost, "/api/v1/categories", `{"name": "New Category", "slug": "new-category"}`, nil)
		w := executeHandler(t, h.CreateCategory, req)

		assertStatusCode(t, w, http.StatusCreated)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if data.Name != "New Category" {
			t.Errorf("expected name 'New Category', got %q", data.Name)
		}
	})

	t.Run("with parent", func(t *testing.T) {
		parent := createTestCategory(t, db, "Parent Cat", "parent-cat", nil)

		body := fmt.Sprintf(`{"name": "Child Category", "slug": "child-category", "parent_id": %d}`, parent.ID)
		req := newJSONRequest(t, http.MethodPost, "/api/v1/categories", body, nil)
		w := executeHandler(t, h.CreateCategory, req)

		assertStatusCode(t, w, http.StatusCreated)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if data.ParentID == nil || *data.ParentID != parent.ID {
			t.Errorf("expected parent_id %d, got %v", parent.ID, data.ParentID)
		}
	})

	t.Run("with invalid parent", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPost, "/api/v1/categories", `{"name": "Orphan", "slug": "orphan", "parent_id": 9999}`, nil)
		w := executeHandler(t, h.CreateCategory, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("missing name", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPost, "/api/v1/categories", `{"slug": "missing-name"}`, nil)
		w := executeHandler(t, h.CreateCategory, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("duplicate slug", func(t *testing.T) {
		createTestCategory(t, db, "Existing", "existing-cat-slug", nil)

		req := newJSONRequest(t, http.MethodPost, "/api/v1/categories", `{"name": "Another", "slug": "existing-cat-slug"}`, nil)
		w := executeHandler(t, h.CreateCategory, req)

		// Duplicate slug returns 422 Unprocessable Entity with validation error
		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})
}

func TestUpdateCategory(t *testing.T) {
	db, h := testSetup(t)

	cat := createTestCategory(t, db, "Original Cat", "original-cat-slug", nil)

	t.Run("update name", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", `{"name": "Updated Category"}`, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if data.Name != "Updated Category" {
			t.Errorf("expected name 'Updated Category', got %q", data.Name)
		}
	})

	t.Run("update description", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", `{"description": "A new description"}`, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if data.Description != "A new description" {
			t.Errorf("expected description 'A new description', got %q", data.Description)
		}
	})

	t.Run("self as parent", func(t *testing.T) {
		body := fmt.Sprintf(`{"parent_id": %d}`, cat.ID)
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", body, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("non-existent category", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/999", `{"name": "Update"}`, map[string]string{"id": "999"})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})

	t.Run("circular reference - child as parent", func(t *testing.T) {
		// Create a hierarchy: grandparent -> parent -> child
		grandparent := createTestCategory(t, db, "Grandparent", "grandparent", nil)
		gpID := grandparent.ID
		parent := createTestCategory(t, db, "Parent Cat", "parent-cat", &gpID)
		pID := parent.ID
		child := createTestCategory(t, db, "Child Cat", "child-cat", &pID)

		// Try to set child as parent of grandparent (circular reference)
		body := fmt.Sprintf(`{"parent_id": %d}`, child.ID)
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", body, map[string]string{"id": fmt.Sprintf("%d", grandparent.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)

		resp := assertErrorResponse(t, w, "validation_error")
		if resp.Error.Details["parent_id"] != "Cannot set a descendant as parent (circular reference)" {
			t.Errorf("expected circular reference error, got %q", resp.Error.Details["parent_id"])
		}
	})

	t.Run("circular reference - grandchild as parent", func(t *testing.T) {
		// Create deeper hierarchy: root -> child -> grandchild
		root := createTestCategory(t, db, "Root Cat", "root-cat", nil)
		rootID := root.ID
		childCat := createTestCategory(t, db, "Child2", "child2", &rootID)
		childID := childCat.ID
		grandchild := createTestCategory(t, db, "Grandchild", "grandchild", &childID)

		// Try to set grandchild as parent of root (circular reference)
		body := fmt.Sprintf(`{"parent_id": %d}`, grandchild.ID)
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", body, map[string]string{"id": fmt.Sprintf("%d", root.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("clear parent", func(t *testing.T) {
		// Create a category with a parent
		parentCat := createTestCategory(t, db, "Parent To Clear", "parent-to-clear", nil)
		parentID := parentCat.ID
		childCat := createTestCategory(t, db, "Has Parent", "has-parent", &parentID)

		// Clear the parent by setting parent_id to 0
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", `{"parent_id": 0}`, map[string]string{"id": fmt.Sprintf("%d", childCat.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if data.ParentID != nil {
			t.Errorf("expected parent_id to be nil after clearing, got %v", data.ParentID)
		}
	})

	t.Run("set valid parent", func(t *testing.T) {
		// Create two unrelated categories
		newParent := createTestCategory(t, db, "New Parent", "new-parent", nil)
		orphan := createTestCategory(t, db, "Orphan Cat", "orphan-cat", nil)

		// Set parent for orphan
		body := fmt.Sprintf(`{"parent_id": %d}`, newParent.ID)
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", body, map[string]string{"id": fmt.Sprintf("%d", orphan.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if data.ParentID == nil || *data.ParentID != newParent.ID {
			t.Errorf("expected parent_id %d, got %v", newParent.ID, data.ParentID)
		}
	})

	t.Run("non-existent parent", func(t *testing.T) {
		orphan := createTestCategory(t, db, "Orphan2", "orphan2", nil)

		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", `{"parent_id": 99999}`, map[string]string{"id": fmt.Sprintf("%d", orphan.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)

		resp := assertErrorResponse(t, w, "validation_error")
		if resp.Error.Details["parent_id"] != "Parent category not found" {
			t.Errorf("expected 'Parent category not found', got %q", resp.Error.Details["parent_id"])
		}
	})

	t.Run("update position", func(t *testing.T) {
		posCat := createTestCategory(t, db, "Position Cat", "position-cat", nil)

		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", `{"position": 5}`, map[string]string{"id": fmt.Sprintf("%d", posCat.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusOK)

		data := unmarshalData[CategoryAPIResponse](t, w)
		if data.Position != 5 {
			t.Errorf("expected position 5, got %d", data.Position)
		}
	})

	t.Run("update slug with uniqueness check", func(t *testing.T) {
		cat1 := createTestCategory(t, db, "Cat One", "cat-one-slug", nil)
		createTestCategory(t, db, "Cat Two", "cat-two-slug", nil)

		// Try to update cat1's slug to cat2's slug
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", `{"slug": "cat-two-slug"}`, map[string]string{"id": fmt.Sprintf("%d", cat1.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodPut, "/api/v1/categories/1", `invalid json`, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := executeHandler(t, h.UpdateCategory, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestDeleteCategory(t *testing.T) {
	db, h := testSetup(t)

	t.Run("delete existing category", func(t *testing.T) {
		cat := createTestCategory(t, db, "To Delete", "to-delete-cat", nil)

		req := newDeleteRequest(t, "/api/v1/categories/1", map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := executeHandler(t, h.DeleteCategory, req)

		assertStatusCode(t, w, http.StatusNoContent)

		// Verify category is deleted
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM categories WHERE id = ?", cat.ID).Scan(&count)
		if count != 0 {
			t.Error("category should be deleted")
		}
	})

	t.Run("delete category with children", func(t *testing.T) {
		parent := createTestCategory(t, db, "Parent to Delete", "parent-to-delete", nil)
		parentID := parent.ID
		createTestCategory(t, db, "Child of Parent", "child-of-parent", &parentID)

		req := newDeleteRequest(t, "/api/v1/categories/1", map[string]string{"id": fmt.Sprintf("%d", parent.ID)})
		w := executeHandler(t, h.DeleteCategory, req)

		assertStatusCode(t, w, http.StatusConflict)
	})

	t.Run("delete non-existent category", func(t *testing.T) {
		req := newDeleteRequest(t, "/api/v1/categories/999", map[string]string{"id": "999"})
		w := executeHandler(t, h.DeleteCategory, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})
}
