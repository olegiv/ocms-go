// Package api provides REST API handlers for the CMS.
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"ocms-go/internal/handler"
	"ocms-go/internal/store"
)

// TagAPIResponse represents a tag in API responses.
type TagAPIResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	PageCount int64     `json:"page_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CategoryAPIResponse represents a category in API responses.
type CategoryAPIResponse struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Slug        string                 `json:"slug"`
	Description string                 `json:"description,omitempty"`
	ParentID    *int64                 `json:"parent_id,omitempty"`
	Position    int64                  `json:"position"`
	PageCount   int64                  `json:"page_count"`
	Children    []*CategoryAPIResponse `json:"children,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// CreateTagRequest represents the request body for creating a tag.
type CreateTagRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// UpdateTagRequest represents the request body for updating a tag.
type UpdateTagRequest struct {
	Name *string `json:"name,omitempty"`
	Slug *string `json:"slug,omitempty"`
}

// CreateCategoryRequest represents the request body for creating a category.
type CreateCategoryRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
	ParentID    *int64 `json:"parent_id,omitempty"`
	Position    *int64 `json:"position,omitempty"`
}

// UpdateCategoryRequest represents the request body for updating a category.
type UpdateCategoryRequest struct {
	Name        *string `json:"name,omitempty"`
	Slug        *string `json:"slug,omitempty"`
	Description *string `json:"description,omitempty"`
	ParentID    *int64  `json:"parent_id,omitempty"`
	Position    *int64  `json:"position,omitempty"`
}

// ============================================================================
// Tag Endpoints
// ============================================================================

// ListTags handles GET /api/v1/tags
// Public: returns all tags with page counts
func (h *Handler) ListTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse pagination
	page := handler.ParsePageParam(r)
	perPage := handler.ParsePerPageParam(r, 50, 100)
	offset := (page - 1) * perPage

	// Get tags with usage counts
	tags, err := h.queries.GetTagUsageCounts(ctx, store.GetTagUsageCountsParams{
		Limit:  int64(perPage),
		Offset: int64(offset),
	})
	if err != nil {
		WriteInternalError(w, "Failed to list tags")
		return
	}

	// Get total count
	total, err := h.queries.CountTags(ctx)
	if err != nil {
		WriteInternalError(w, "Failed to count tags")
		return
	}

	// Convert to response
	responses := make([]TagAPIResponse, 0, len(tags))
	for _, t := range tags {
		responses = append(responses, TagAPIResponse{
			ID:        t.ID,
			Name:      t.Name,
			Slug:      t.Slug,
			PageCount: t.UsageCount,
			CreatedAt: t.CreatedAt,
			UpdatedAt: t.UpdatedAt,
		})
	}

	// Calculate total pages
	totalPages := int(total) / perPage
	if int(total)%perPage != 0 {
		totalPages++
	}

	WriteSuccess(w, responses, &Meta{
		Total:   total,
		Page:    page,
		PerPage: perPage,
		Pages:   totalPages,
	})
}

// GetTag handles GET /api/v1/tags/{id}
// Public: returns a single tag
func (h *Handler) GetTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tag, ok := h.requireTagForAPI(w, r)
	if !ok {
		return
	}

	// Get page count for this tag
	pageCount, err := h.queries.CountPagesForTag(ctx, tag.ID)
	if err != nil {
		pageCount = 0
	}

	resp := TagAPIResponse{
		ID:        tag.ID,
		Name:      tag.Name,
		Slug:      tag.Slug,
		PageCount: pageCount,
		CreatedAt: tag.CreatedAt,
		UpdatedAt: tag.UpdatedAt,
	}

	WriteSuccess(w, resp, nil)
}

// CreateTag handles POST /api/v1/tags
// Requires taxonomy:write permission
func (h *Handler) CreateTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req CreateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteBadRequest(w, "Invalid JSON body", nil)
		return
	}

	// Validate required fields
	validationErrors := make(map[string]string)
	if req.Name == "" {
		validationErrors["name"] = "Name is required"
	}
	if req.Slug == "" {
		validationErrors["slug"] = "Slug is required"
	}
	if len(validationErrors) > 0 {
		WriteValidationError(w, validationErrors)
		return
	}

	// Check slug uniqueness
	exists, err := h.queries.TagSlugExists(ctx, req.Slug)
	if err != nil {
		WriteInternalError(w, "Failed to check slug")
		return
	}
	if exists != 0 {
		WriteValidationError(w, map[string]string{"slug": "Slug already exists"})
		return
	}

	now := time.Now()
	tag, err := h.queries.CreateTag(ctx, store.CreateTagParams{
		Name:      req.Name,
		Slug:      req.Slug,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		WriteInternalError(w, "Failed to create tag")
		return
	}

	resp := TagAPIResponse{
		ID:        tag.ID,
		Name:      tag.Name,
		Slug:      tag.Slug,
		PageCount: 0,
		CreatedAt: tag.CreatedAt,
		UpdatedAt: tag.UpdatedAt,
	}

	WriteCreated(w, resp)
}

// UpdateTag handles PUT /api/v1/tags/{id}
// Requires taxonomy:write permission
func (h *Handler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	existing, ok := h.requireTagForAPI(w, r)
	if !ok {
		return
	}

	var req UpdateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteBadRequest(w, "Invalid JSON body", nil)
		return
	}

	// Build update params
	params := store.UpdateTagParams{
		ID:        existing.ID,
		Name:      existing.Name,
		Slug:      existing.Slug,
		UpdatedAt: time.Now(),
	}

	// Apply updates
	if req.Name != nil && *req.Name != "" {
		params.Name = *req.Name
	}
	if req.Slug != nil && *req.Slug != "" {
		// Check slug uniqueness (excluding current tag)
		exists, err := h.queries.TagSlugExistsExcluding(ctx, store.TagSlugExistsExcludingParams{
			Slug: *req.Slug,
			ID:   existing.ID,
		})
		if err != nil {
			WriteInternalError(w, "Failed to check slug")
			return
		}
		if exists != 0 {
			WriteValidationError(w, map[string]string{"slug": "Slug already exists"})
			return
		}
		params.Slug = *req.Slug
	}

	tag, err := h.queries.UpdateTag(ctx, params)
	if err != nil {
		WriteInternalError(w, "Failed to update tag")
		return
	}

	// Get page count
	pageCount, err := h.queries.CountPagesForTag(ctx, existing.ID)
	if err != nil {
		pageCount = 0
	}

	resp := TagAPIResponse{
		ID:        tag.ID,
		Name:      tag.Name,
		Slug:      tag.Slug,
		PageCount: pageCount,
		CreatedAt: tag.CreatedAt,
		UpdatedAt: tag.UpdatedAt,
	}

	WriteSuccess(w, resp, nil)
}

// DeleteTag handles DELETE /api/v1/tags/{id}
// Requires taxonomy:write permission
func (h *Handler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tag, ok := h.requireTagForAPI(w, r)
	if !ok {
		return
	}

	// Delete tag (page_tags associations are handled by CASCADE or manually)
	if err := h.queries.DeleteTag(ctx, tag.ID); err != nil {
		WriteInternalError(w, "Failed to delete tag")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================================
// Category Endpoints
// ============================================================================

// ListCategories handles GET /api/v1/categories
// Public: returns all categories as a nested tree structure
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if flat list is requested
	flat := r.URL.Query().Get("flat") == "true"

	// Get all categories with usage counts
	categories, err := h.queries.GetCategoryUsageCounts(ctx)
	if err != nil {
		WriteInternalError(w, "Failed to list categories")
		return
	}

	if flat {
		// Return flat list
		responses := make([]CategoryAPIResponse, 0, len(categories))
		for _, c := range categories {
			resp := categoryRowToResponse(c)
			responses = append(responses, resp)
		}

		WriteSuccess(w, responses, &Meta{
			Total: int64(len(responses)),
		})
		return
	}

	// Build nested tree structure
	tree := buildCategoryTree(categories)

	WriteSuccess(w, tree, &Meta{
		Total: int64(len(categories)),
	})
}

// GetCategory handles GET /api/v1/categories/{id}
// Public: returns a single category with its children
func (h *Handler) GetCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	category, ok := h.requireCategoryForAPI(w, r)
	if !ok {
		return
	}

	// Get page count for this category
	pageCount, err := h.queries.CountPagesByCategory(ctx, category.ID)
	if err != nil {
		pageCount = 0
	}

	// Get children
	children, err := h.queries.ListChildCategories(ctx, sql.NullInt64{Int64: category.ID, Valid: true})
	if err != nil {
		children = nil
	}

	resp := CategoryAPIResponse{
		ID:        category.ID,
		Name:      category.Name,
		Slug:      category.Slug,
		Position:  category.Position,
		PageCount: pageCount,
		CreatedAt: category.CreatedAt,
		UpdatedAt: category.UpdatedAt,
	}

	if category.Description.Valid {
		resp.Description = category.Description.String
	}
	if category.ParentID.Valid {
		resp.ParentID = &category.ParentID.Int64
	}

	// Add children
	if len(children) > 0 {
		resp.Children = make([]*CategoryAPIResponse, 0, len(children))
		for _, child := range children {
			childPageCount, _ := h.queries.CountPagesByCategory(ctx, child.ID)
			childResp := &CategoryAPIResponse{
				ID:        child.ID,
				Name:      child.Name,
				Slug:      child.Slug,
				Position:  child.Position,
				PageCount: childPageCount,
				CreatedAt: child.CreatedAt,
				UpdatedAt: child.UpdatedAt,
			}
			if child.Description.Valid {
				childResp.Description = child.Description.String
			}
			if child.ParentID.Valid {
				childResp.ParentID = &child.ParentID.Int64
			}
			resp.Children = append(resp.Children, childResp)
		}
	}

	WriteSuccess(w, resp, nil)
}

// CreateCategory handles POST /api/v1/categories
// Requires taxonomy:write permission
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req CreateCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteBadRequest(w, "Invalid JSON body", nil)
		return
	}

	// Validate required fields
	validationErrors := make(map[string]string)
	if req.Name == "" {
		validationErrors["name"] = "Name is required"
	}
	if req.Slug == "" {
		validationErrors["slug"] = "Slug is required"
	}
	if len(validationErrors) > 0 {
		WriteValidationError(w, validationErrors)
		return
	}

	// Check slug uniqueness
	exists, err := h.queries.CategorySlugExists(ctx, req.Slug)
	if err != nil {
		WriteInternalError(w, "Failed to check slug")
		return
	}
	if exists > 0 {
		WriteValidationError(w, map[string]string{"slug": "Slug already exists"})
		return
	}

	// Validate parent_id if provided
	if req.ParentID != nil {
		_, err := h.queries.GetCategoryByID(ctx, *req.ParentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				WriteValidationError(w, map[string]string{"parent_id": "Parent category not found"})
			} else {
				WriteInternalError(w, "Failed to validate parent category")
			}
			return
		}
	}

	now := time.Now()
	params := store.CreateCategoryParams{
		Name:      req.Name,
		Slug:      req.Slug,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if req.Description != "" {
		params.Description = sql.NullString{String: req.Description, Valid: true}
	}
	if req.ParentID != nil {
		params.ParentID = sql.NullInt64{Int64: *req.ParentID, Valid: true}
	}
	if req.Position != nil {
		params.Position = *req.Position
	}

	category, err := h.queries.CreateCategory(ctx, params)
	if err != nil {
		WriteInternalError(w, "Failed to create category")
		return
	}

	resp := CategoryAPIResponse{
		ID:        category.ID,
		Name:      category.Name,
		Slug:      category.Slug,
		Position:  category.Position,
		PageCount: 0,
		CreatedAt: category.CreatedAt,
		UpdatedAt: category.UpdatedAt,
	}

	if category.Description.Valid {
		resp.Description = category.Description.String
	}
	if category.ParentID.Valid {
		resp.ParentID = &category.ParentID.Int64
	}

	WriteCreated(w, resp)
}

// UpdateCategory handles PUT /api/v1/categories/{id}
// Requires taxonomy:write permission
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	existing, ok := h.requireCategoryForAPI(w, r)
	if !ok {
		return
	}

	var req UpdateCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteBadRequest(w, "Invalid JSON body", nil)
		return
	}

	// Build update params
	params := store.UpdateCategoryParams{
		ID:          existing.ID,
		Name:        existing.Name,
		Slug:        existing.Slug,
		Description: existing.Description,
		ParentID:    existing.ParentID,
		Position:    existing.Position,
		UpdatedAt:   time.Now(),
	}

	// Apply updates
	if req.Name != nil && *req.Name != "" {
		params.Name = *req.Name
	}
	if req.Slug != nil && *req.Slug != "" {
		// Check slug uniqueness (excluding current category)
		exists, err := h.queries.CategorySlugExistsExcluding(ctx, store.CategorySlugExistsExcludingParams{
			Slug: *req.Slug,
			ID:   existing.ID,
		})
		if err != nil {
			WriteInternalError(w, "Failed to check slug")
			return
		}
		if exists > 0 {
			WriteValidationError(w, map[string]string{"slug": "Slug already exists"})
			return
		}
		params.Slug = *req.Slug
	}
	if req.Description != nil {
		params.Description = sql.NullString{String: *req.Description, Valid: *req.Description != ""}
	}
	if req.ParentID != nil {
		// Check for circular reference
		if *req.ParentID == existing.ID {
			WriteValidationError(w, map[string]string{"parent_id": "Category cannot be its own parent"})
			return
		}
		if *req.ParentID == 0 {
			// Clear parent
			params.ParentID = sql.NullInt64{Valid: false}
		} else {
			// Validate new parent exists and is not a descendant
			_, err := h.queries.GetCategoryByID(ctx, *req.ParentID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					WriteValidationError(w, map[string]string{"parent_id": "Parent category not found"})
				} else {
					WriteInternalError(w, "Failed to validate parent category")
				}
				return
			}

			// Check for circular reference (new parent is a descendant)
			descendants, _ := h.queries.GetDescendantIDs(ctx, sql.NullInt64{Int64: existing.ID, Valid: true})
			for _, descID := range descendants {
				if descID == *req.ParentID {
					WriteValidationError(w, map[string]string{"parent_id": "Cannot set a descendant as parent (circular reference)"})
					return
				}
			}

			params.ParentID = sql.NullInt64{Int64: *req.ParentID, Valid: true}
		}
	}
	if req.Position != nil {
		params.Position = *req.Position
	}

	category, err := h.queries.UpdateCategory(ctx, params)
	if err != nil {
		WriteInternalError(w, "Failed to update category")
		return
	}

	// Get page count
	pageCount, err := h.queries.CountPagesByCategory(ctx, existing.ID)
	if err != nil {
		pageCount = 0
	}

	resp := CategoryAPIResponse{
		ID:        category.ID,
		Name:      category.Name,
		Slug:      category.Slug,
		Position:  category.Position,
		PageCount: pageCount,
		CreatedAt: category.CreatedAt,
		UpdatedAt: category.UpdatedAt,
	}

	if category.Description.Valid {
		resp.Description = category.Description.String
	}
	if category.ParentID.Valid {
		resp.ParentID = &category.ParentID.Int64
	}

	WriteSuccess(w, resp, nil)
}

// DeleteCategory handles DELETE /api/v1/categories/{id}
// Requires taxonomy:write permission
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	category, ok := h.requireCategoryForAPI(w, r)
	if !ok {
		return
	}

	// Check for child categories
	children, err := h.queries.ListChildCategories(ctx, sql.NullInt64{Int64: category.ID, Valid: true})
	if err == nil && len(children) > 0 {
		WriteError(w, http.StatusConflict, "has_children", "Cannot delete category with child categories. Delete or reassign children first.", nil)
		return
	}

	// Delete category (page_categories associations are handled by CASCADE or manually)
	if err := h.queries.DeleteCategory(ctx, category.ID); err != nil {
		WriteInternalError(w, "Failed to delete category")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================================
// Helper Functions
// ============================================================================

// categoryRowToResponse converts a GetCategoryUsageCountsRow to CategoryAPIResponse.
func categoryRowToResponse(c store.GetCategoryUsageCountsRow) CategoryAPIResponse {
	resp := CategoryAPIResponse{
		ID:        c.ID,
		Name:      c.Name,
		Slug:      c.Slug,
		Position:  c.Position,
		PageCount: c.UsageCount,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}

	if c.Description.Valid {
		resp.Description = c.Description.String
	}
	if c.ParentID.Valid {
		resp.ParentID = &c.ParentID.Int64
	}

	return resp
}

// buildCategoryTree builds a nested tree structure from flat category list.
func buildCategoryTree(categories []store.GetCategoryUsageCountsRow) []*CategoryAPIResponse {
	// Create a map for quick lookup
	categoryMap := make(map[int64]*CategoryAPIResponse)
	for _, c := range categories {
		resp := categoryRowToResponse(c)
		resp.Children = []*CategoryAPIResponse{} // Initialize empty children slice
		categoryMap[c.ID] = &resp
	}

	// Build tree by assigning children to parents
	var rootCategories []*CategoryAPIResponse
	for _, c := range categories {
		cat := categoryMap[c.ID]
		if c.ParentID.Valid {
			// Has parent - add to parent's children
			if parent, ok := categoryMap[c.ParentID.Int64]; ok {
				parent.Children = append(parent.Children, cat)
			} else {
				// Parent not found, treat as root
				rootCategories = append(rootCategories, cat)
			}
		} else {
			// No parent - this is a root category
			rootCategories = append(rootCategories, cat)
		}
	}

	return rootCategories
}

// requireTagForAPI parses tag ID from URL and fetches the tag.
// Returns the tag and true if successful, or zero value and false if an error occurred (response already written).
func (h *Handler) requireTagForAPI(w http.ResponseWriter, r *http.Request) (store.Tag, bool) {
	id, err := handler.ParseIDParam(r)
	if err != nil {
		WriteBadRequest(w, "Invalid tag ID", nil)
		return store.Tag{}, false
	}

	tag, err := h.queries.GetTagByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteNotFound(w, "Tag not found")
		} else {
			WriteInternalError(w, "Failed to retrieve tag")
		}
		return store.Tag{}, false
	}
	return tag, true
}

// requireCategoryForAPI parses category ID from URL and fetches the category.
// Returns the category and true if successful, or zero value and false if an error occurred (response already written).
func (h *Handler) requireCategoryForAPI(w http.ResponseWriter, r *http.Request) (store.Category, bool) {
	id, err := handler.ParseIDParam(r)
	if err != nil {
		WriteBadRequest(w, "Invalid category ID", nil)
		return store.Category{}, false
	}

	category, err := h.queries.GetCategoryByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteNotFound(w, "Category not found")
		} else {
			WriteInternalError(w, "Failed to retrieve category")
		}
		return store.Category{}, false
	}
	return category, true
}
