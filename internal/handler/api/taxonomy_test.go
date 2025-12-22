package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ocms-go/internal/store"
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

			if got.ID != tt.want.ID {
				t.Errorf("categoryRowToResponse().ID = %v, want %v", got.ID, tt.want.ID)
			}
			if got.Name != tt.want.Name {
				t.Errorf("categoryRowToResponse().Name = %v, want %v", got.Name, tt.want.Name)
			}
			if got.Slug != tt.want.Slug {
				t.Errorf("categoryRowToResponse().Slug = %v, want %v", got.Slug, tt.want.Slug)
			}
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
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tags", nil)
		w := httptest.NewRecorder()

		h.ListTags(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp Response
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Meta == nil || resp.Meta.Total != 0 {
			t.Errorf("expected total 0, got %v", resp.Meta)
		}
	})

	t.Run("with tags", func(t *testing.T) {
		createTestTag(t, db, "Tag One", "tag-one")
		createTestTag(t, db, "Tag Two", "tag-two")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tags", nil)
		w := httptest.NewRecorder()

		h.ListTags(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data []TagAPIResponse `json:"data"`
			Meta *Meta            `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if len(resp.Data) != 2 {
			t.Errorf("expected 2 tags, got %d", len(resp.Data))
		}
		if resp.Meta.Total != 2 {
			t.Errorf("expected total 2, got %d", resp.Meta.Total)
		}
	})

	t.Run("with pagination", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tags?page=1&per_page=1", nil)
		w := httptest.NewRecorder()

		h.ListTags(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data []TagAPIResponse `json:"data"`
			Meta *Meta            `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if len(resp.Data) != 1 {
			t.Errorf("expected 1 tag per page, got %d", len(resp.Data))
		}
		if resp.Meta.PerPage != 1 {
			t.Errorf("expected per_page 1, got %d", resp.Meta.PerPage)
		}
	})
}

func TestGetTag(t *testing.T) {
	db, h := testSetup(t)

	tag := createTestTag(t, db, "Test Tag", "test-tag")

	t.Run("existing tag", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tags/1", nil)
		req = requestWithURLParams(req, map[string]string{"id": "1"})
		w := httptest.NewRecorder()

		h.GetTag(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data TagAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.ID != tag.ID {
			t.Errorf("expected ID %d, got %d", tag.ID, resp.Data.ID)
		}
		if resp.Data.Name != "Test Tag" {
			t.Errorf("expected name 'Test Tag', got %q", resp.Data.Name)
		}
	})

	t.Run("non-existent tag", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tags/999", nil)
		req = requestWithURLParams(req, map[string]string{"id": "999"})
		w := httptest.NewRecorder()

		h.GetTag(w, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})

	t.Run("invalid tag ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tags/invalid", nil)
		req = requestWithURLParams(req, map[string]string{"id": "invalid"})
		w := httptest.NewRecorder()

		h.GetTag(w, req)

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
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tags/1", nil)
		req = requestWithURLParams(req, map[string]string{"id": "1"})
		w := httptest.NewRecorder()

		h.GetTag(w, req)

		assertStatusCode(t, w, http.StatusInternalServerError)

		var resp ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Error.Code != "internal_error" {
			t.Errorf("expected error code 'internal_error', got %q", resp.Error.Code)
		}
		if resp.Error.Message != "Failed to retrieve tag" {
			t.Errorf("expected message 'Failed to retrieve tag', got %q", resp.Error.Message)
		}
	})

	t.Run("database error on category returns internal error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories/1", nil)
		req = requestWithURLParams(req, map[string]string{"id": "1"})
		w := httptest.NewRecorder()

		h.GetCategory(w, req)

		assertStatusCode(t, w, http.StatusInternalServerError)

		var resp ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Error.Code != "internal_error" {
			t.Errorf("expected error code 'internal_error', got %q", resp.Error.Code)
		}
		if resp.Error.Message != "Failed to retrieve category" {
			t.Errorf("expected message 'Failed to retrieve category', got %q", resp.Error.Message)
		}
	})
}

func TestCreateTag(t *testing.T) {
	db, h := testSetup(t)

	t.Run("valid tag", func(t *testing.T) {
		body := `{"name": "New Tag", "slug": "new-tag"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tags", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateTag(w, req)

		assertStatusCode(t, w, http.StatusCreated)

		var resp struct {
			Data TagAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.Name != "New Tag" {
			t.Errorf("expected name 'New Tag', got %q", resp.Data.Name)
		}
		if resp.Data.Slug != "new-tag" {
			t.Errorf("expected slug 'new-tag', got %q", resp.Data.Slug)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		body := `{"slug": "missing-name"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tags", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateTag(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("missing slug", func(t *testing.T) {
		body := `{"name": "Missing Slug"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tags", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateTag(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("duplicate slug", func(t *testing.T) {
		createTestTag(t, db, "Existing", "existing-slug")

		body := `{"name": "Another", "slug": "existing-slug"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tags", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateTag(w, req)

		// Duplicate slug returns 422 Unprocessable Entity with validation error
		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		body := `not valid json`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tags", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateTag(w, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestUpdateTag(t *testing.T) {
	db, h := testSetup(t)

	tag := createTestTag(t, db, "Original", "original-slug")

	t.Run("update name", func(t *testing.T) {
		body := `{"name": "Updated Name"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tags/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", tag.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateTag(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data TagAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.Name != "Updated Name" {
			t.Errorf("expected name 'Updated Name', got %q", resp.Data.Name)
		}
		if resp.Data.Slug != "original-slug" {
			t.Errorf("expected slug to remain 'original-slug', got %q", resp.Data.Slug)
		}
	})

	t.Run("update slug", func(t *testing.T) {
		body := `{"slug": "updated-slug"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tags/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", tag.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateTag(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data TagAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.Slug != "updated-slug" {
			t.Errorf("expected slug 'updated-slug', got %q", resp.Data.Slug)
		}
	})

	t.Run("non-existent tag", func(t *testing.T) {
		body := `{"name": "Update"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tags/999", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": "999"})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateTag(w, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		body := `invalid`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/tags/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", tag.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateTag(w, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestDeleteTag(t *testing.T) {
	db, h := testSetup(t)

	t.Run("delete existing tag", func(t *testing.T) {
		tag := createTestTag(t, db, "To Delete", "to-delete")

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/tags/1", nil)
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", tag.ID)})
		w := httptest.NewRecorder()

		h.DeleteTag(w, req)

		assertStatusCode(t, w, http.StatusNoContent)

		// Verify tag is deleted
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM tags WHERE id = ?", tag.ID).Scan(&count)
		if count != 0 {
			t.Error("tag should be deleted")
		}
	})

	t.Run("delete non-existent tag", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/tags/999", nil)
		req = requestWithURLParams(req, map[string]string{"id": "999"})
		w := httptest.NewRecorder()

		h.DeleteTag(w, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})
}

// ============================================================================
// Integration Tests for Category Handlers
// ============================================================================

func TestListCategories(t *testing.T) {
	db, h := testSetup(t)

	t.Run("empty list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
		w := httptest.NewRecorder()

		h.ListCategories(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp Response
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Meta == nil || resp.Meta.Total != 0 {
			t.Errorf("expected total 0, got %v", resp.Meta)
		}
	})

	t.Run("with categories as tree", func(t *testing.T) {
		parent := createTestCategory(t, db, "Parent", "parent", nil)
		parentID := parent.ID
		createTestCategory(t, db, "Child", "child", &parentID)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
		w := httptest.NewRecorder()

		h.ListCategories(w, req)

		assertStatusCode(t, w, http.StatusOK)

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
		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories?flat=true", nil)
		w := httptest.NewRecorder()

		h.ListCategories(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data []CategoryAPIResponse `json:"data"`
			Meta *Meta                 `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		// Flat list should return all categories without nesting
		if len(resp.Data) != 2 {
			t.Errorf("expected 2 categories in flat list, got %d", len(resp.Data))
		}
	})
}

func TestGetCategory(t *testing.T) {
	db, h := testSetup(t)

	cat := createTestCategory(t, db, "Test Category", "test-category", nil)

	t.Run("existing category", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories/1", nil)
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := httptest.NewRecorder()

		h.GetCategory(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.ID != cat.ID {
			t.Errorf("expected ID %d, got %d", cat.ID, resp.Data.ID)
		}
		if resp.Data.Name != "Test Category" {
			t.Errorf("expected name 'Test Category', got %q", resp.Data.Name)
		}
	})

	t.Run("category with children", func(t *testing.T) {
		catID := cat.ID
		createTestCategory(t, db, "Child Cat", "child-cat", &catID)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories/1", nil)
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := httptest.NewRecorder()

		h.GetCategory(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if len(resp.Data.Children) != 1 {
			t.Errorf("expected 1 child, got %d", len(resp.Data.Children))
		}
	})

	t.Run("non-existent category", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories/999", nil)
		req = requestWithURLParams(req, map[string]string{"id": "999"})
		w := httptest.NewRecorder()

		h.GetCategory(w, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})

	t.Run("invalid category ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories/invalid", nil)
		req = requestWithURLParams(req, map[string]string{"id": "invalid"})
		w := httptest.NewRecorder()

		h.GetCategory(w, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestCreateCategory(t *testing.T) {
	db, h := testSetup(t)

	t.Run("valid category", func(t *testing.T) {
		body := `{"name": "New Category", "slug": "new-category"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/categories", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateCategory(w, req)

		assertStatusCode(t, w, http.StatusCreated)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.Name != "New Category" {
			t.Errorf("expected name 'New Category', got %q", resp.Data.Name)
		}
	})

	t.Run("with parent", func(t *testing.T) {
		parent := createTestCategory(t, db, "Parent Cat", "parent-cat", nil)

		body := fmt.Sprintf(`{"name": "Child Category", "slug": "child-category", "parent_id": %d}`, parent.ID)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/categories", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateCategory(w, req)

		assertStatusCode(t, w, http.StatusCreated)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.ParentID == nil || *resp.Data.ParentID != parent.ID {
			t.Errorf("expected parent_id %d, got %v", parent.ID, resp.Data.ParentID)
		}
	})

	t.Run("with invalid parent", func(t *testing.T) {
		body := `{"name": "Orphan", "slug": "orphan", "parent_id": 9999}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/categories", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateCategory(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("missing name", func(t *testing.T) {
		body := `{"slug": "missing-name"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/categories", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateCategory(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("duplicate slug", func(t *testing.T) {
		createTestCategory(t, db, "Existing", "existing-cat-slug", nil)

		body := `{"name": "Another", "slug": "existing-cat-slug"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/categories", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.CreateCategory(w, req)

		// Duplicate slug returns 422 Unprocessable Entity with validation error
		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})
}

func TestUpdateCategory(t *testing.T) {
	db, h := testSetup(t)

	cat := createTestCategory(t, db, "Original Cat", "original-cat-slug", nil)

	t.Run("update name", func(t *testing.T) {
		body := `{"name": "Updated Category"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.Name != "Updated Category" {
			t.Errorf("expected name 'Updated Category', got %q", resp.Data.Name)
		}
	})

	t.Run("update description", func(t *testing.T) {
		body := `{"description": "A new description"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.Description != "A new description" {
			t.Errorf("expected description 'A new description', got %q", resp.Data.Description)
		}
	})

	t.Run("self as parent", func(t *testing.T) {
		body := fmt.Sprintf(`{"parent_id": %d}`, cat.ID)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("non-existent category", func(t *testing.T) {
		body := `{"name": "Update"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/999", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": "999"})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

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
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", grandparent.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)

		var resp ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
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
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", root.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("clear parent", func(t *testing.T) {
		// Create a category with a parent
		parentCat := createTestCategory(t, db, "Parent To Clear", "parent-to-clear", nil)
		parentID := parentCat.ID
		childCat := createTestCategory(t, db, "Has Parent", "has-parent", &parentID)

		// Clear the parent by setting parent_id to 0
		body := `{"parent_id": 0}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", childCat.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.ParentID != nil {
			t.Errorf("expected parent_id to be nil after clearing, got %v", resp.Data.ParentID)
		}
	})

	t.Run("set valid parent", func(t *testing.T) {
		// Create two unrelated categories
		newParent := createTestCategory(t, db, "New Parent", "new-parent", nil)
		orphan := createTestCategory(t, db, "Orphan Cat", "orphan-cat", nil)

		// Set parent for orphan
		body := fmt.Sprintf(`{"parent_id": %d}`, newParent.ID)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", orphan.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.ParentID == nil || *resp.Data.ParentID != newParent.ID {
			t.Errorf("expected parent_id %d, got %v", newParent.ID, resp.Data.ParentID)
		}
	})

	t.Run("non-existent parent", func(t *testing.T) {
		orphan := createTestCategory(t, db, "Orphan2", "orphan2", nil)

		body := `{"parent_id": 99999}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", orphan.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)

		var resp ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Error.Details["parent_id"] != "Parent category not found" {
			t.Errorf("expected 'Parent category not found', got %q", resp.Error.Details["parent_id"])
		}
	})

	t.Run("update position", func(t *testing.T) {
		posCat := createTestCategory(t, db, "Position Cat", "position-cat", nil)

		body := `{"position": 5}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", posCat.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusOK)

		var resp struct {
			Data CategoryAPIResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.Position != 5 {
			t.Errorf("expected position 5, got %d", resp.Data.Position)
		}
	})

	t.Run("update slug with uniqueness check", func(t *testing.T) {
		cat1 := createTestCategory(t, db, "Cat One", "cat-one-slug", nil)
		createTestCategory(t, db, "Cat Two", "cat-two-slug", nil)

		// Try to update cat1's slug to cat2's slug
		body := `{"slug": "cat-two-slug"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", cat1.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusUnprocessableEntity)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		body := `invalid json`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/categories/1", strings.NewReader(body))
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.UpdateCategory(w, req)

		assertStatusCode(t, w, http.StatusBadRequest)
	})
}

func TestDeleteCategory(t *testing.T) {
	db, h := testSetup(t)

	t.Run("delete existing category", func(t *testing.T) {
		cat := createTestCategory(t, db, "To Delete", "to-delete-cat", nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/categories/1", nil)
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", cat.ID)})
		w := httptest.NewRecorder()

		h.DeleteCategory(w, req)

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

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/categories/1", nil)
		req = requestWithURLParams(req, map[string]string{"id": fmt.Sprintf("%d", parent.ID)})
		w := httptest.NewRecorder()

		h.DeleteCategory(w, req)

		assertStatusCode(t, w, http.StatusConflict)
	})

	t.Run("delete non-existent category", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/categories/999", nil)
		req = requestWithURLParams(req, map[string]string{"id": "999"})
		w := httptest.NewRecorder()

		h.DeleteCategory(w, req)

		assertStatusCode(t, w, http.StatusNotFound)
	})
}
