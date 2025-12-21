// Package api provides REST API handlers for the CMS.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/handler"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/store"
)

// PageResponse represents a page in API responses.
type PageResponse struct {
	ID              int64              `json:"id"`
	Title           string             `json:"title"`
	Slug            string             `json:"slug"`
	Body            string             `json:"body"`
	Status          string             `json:"status"`
	AuthorID        int64              `json:"author_id"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	PublishedAt     *time.Time         `json:"published_at,omitempty"`
	FeaturedImageID *int64             `json:"featured_image_id,omitempty"`
	MetaTitle       string             `json:"meta_title,omitempty"`
	MetaDescription string             `json:"meta_description,omitempty"`
	MetaKeywords    string             `json:"meta_keywords,omitempty"`
	OGImageID       *int64             `json:"og_image_id,omitempty"`
	NoIndex         bool               `json:"no_index"`
	NoFollow        bool               `json:"no_follow"`
	CanonicalURL    string             `json:"canonical_url,omitempty"`
	ScheduledAt     *time.Time         `json:"scheduled_at,omitempty"`
	Author          *AuthorResponse    `json:"author,omitempty"`
	Categories      []CategoryResponse `json:"categories,omitempty"`
	Tags            []TagResponse      `json:"tags,omitempty"`
}

// AuthorResponse represents an author in API responses.
type AuthorResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// CategoryResponse represents a category in API responses.
type CategoryResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
}

// TagResponse represents a tag in API responses.
type TagResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// CreatePageRequest represents the request body for creating a page.
type CreatePageRequest struct {
	Title           string  `json:"title"`
	Slug            string  `json:"slug"`
	Body            string  `json:"body"`
	Status          string  `json:"status"`
	FeaturedImageID *int64  `json:"featured_image_id,omitempty"`
	MetaTitle       string  `json:"meta_title,omitempty"`
	MetaDescription string  `json:"meta_description,omitempty"`
	MetaKeywords    string  `json:"meta_keywords,omitempty"`
	OGImageID       *int64  `json:"og_image_id,omitempty"`
	NoIndex         bool    `json:"no_index"`
	NoFollow        bool    `json:"no_follow"`
	CanonicalURL    string  `json:"canonical_url,omitempty"`
	ScheduledAt     *string `json:"scheduled_at,omitempty"`
	CategoryIDs     []int64 `json:"category_ids,omitempty"`
	TagIDs          []int64 `json:"tag_ids,omitempty"`
}

// UpdatePageRequest represents the request body for updating a page.
type UpdatePageRequest struct {
	Title           *string  `json:"title,omitempty"`
	Slug            *string  `json:"slug,omitempty"`
	Body            *string  `json:"body,omitempty"`
	Status          *string  `json:"status,omitempty"`
	FeaturedImageID *int64   `json:"featured_image_id,omitempty"`
	MetaTitle       *string  `json:"meta_title,omitempty"`
	MetaDescription *string  `json:"meta_description,omitempty"`
	MetaKeywords    *string  `json:"meta_keywords,omitempty"`
	OGImageID       *int64   `json:"og_image_id,omitempty"`
	NoIndex         *bool    `json:"no_index,omitempty"`
	NoFollow        *bool    `json:"no_follow,omitempty"`
	CanonicalURL    *string  `json:"canonical_url,omitempty"`
	ScheduledAt     *string  `json:"scheduled_at,omitempty"`
	CategoryIDs     *[]int64 `json:"category_ids,omitempty"`
	TagIDs          *[]int64 `json:"tag_ids,omitempty"`
}

// storePageToResponse converts a store.Page to PageResponse.
func storePageToResponse(p store.Page) PageResponse {
	resp := PageResponse{
		ID:              p.ID,
		Title:           p.Title,
		Slug:            p.Slug,
		Body:            p.Body,
		Status:          p.Status,
		AuthorID:        p.AuthorID,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
		MetaTitle:       p.MetaTitle,
		MetaDescription: p.MetaDescription,
		MetaKeywords:    p.MetaKeywords,
		NoIndex:         p.NoIndex != 0,
		NoFollow:        p.NoFollow != 0,
		CanonicalURL:    p.CanonicalUrl,
	}

	if p.PublishedAt.Valid {
		resp.PublishedAt = &p.PublishedAt.Time
	}
	if p.FeaturedImageID.Valid {
		resp.FeaturedImageID = &p.FeaturedImageID.Int64
	}
	if p.OgImageID.Valid {
		resp.OGImageID = &p.OgImageID.Int64
	}
	if p.ScheduledAt.Valid {
		resp.ScheduledAt = &p.ScheduledAt.Time
	}

	return resp
}

// ListPages handles GET /api/v1/pages
// Public: returns only published pages
// With API key: can filter by status
func (h *Handler) ListPages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse query parameters
	status := r.URL.Query().Get("status")
	categoryIDStr := r.URL.Query().Get("category")
	tagIDStr := r.URL.Query().Get("tag")
	include := r.URL.Query().Get("include")

	// Parse pagination
	page := handler.ParsePageParam(r)
	perPage := handler.ParsePerPageParam(r, 20, 100)
	offset := (page - 1) * perPage

	// Check authentication for non-published access
	apiKey := middleware.GetAPIKey(r)
	isAuthenticated := apiKey != nil

	// Non-authenticated users can only see published pages
	if !isAuthenticated && status != "" && status != model.PageStatusPublished {
		WriteForbidden(w, "Authentication required to view non-published pages")
		return
	}

	// Default to published for unauthenticated requests
	if !isAuthenticated {
		status = model.PageStatusPublished
	}

	var pages []store.Page
	var total int64
	var err error

	// Helper to execute list and count queries
	publishedOnly := status == model.PageStatusPublished
	limit := int64(perPage)
	off := int64(offset)

	// Filter by category
	if categoryIDStr != "" {
		categoryID, parseErr := strconv.ParseInt(categoryIDStr, 10, 64)
		if parseErr != nil {
			WriteBadRequest(w, "Invalid category ID", nil)
			return
		}

		if publishedOnly {
			pages, err = h.queries.ListPublishedPagesByCategory(ctx, store.ListPublishedPagesByCategoryParams{
				CategoryID: categoryID, Limit: limit, Offset: off,
			})
			if err == nil {
				total, err = h.queries.CountPublishedPagesByCategory(ctx, categoryID)
			}
		} else {
			pages, err = h.queries.ListPagesByCategory(ctx, store.ListPagesByCategoryParams{
				CategoryID: categoryID, Limit: limit, Offset: off,
			})
			if err == nil {
				total, err = h.queries.CountPagesByCategory(ctx, categoryID)
			}
		}
	} else if tagIDStr != "" {
		// Filter by tag
		tagID, parseErr := strconv.ParseInt(tagIDStr, 10, 64)
		if parseErr != nil {
			WriteBadRequest(w, "Invalid tag ID", nil)
			return
		}

		if publishedOnly {
			pages, err = h.queries.ListPublishedPagesForTag(ctx, store.ListPublishedPagesForTagParams{
				TagID: tagID, Limit: limit, Offset: off,
			})
			if err == nil {
				total, err = h.queries.CountPublishedPagesForTag(ctx, tagID)
			}
		} else {
			pages, err = h.queries.GetPagesForTag(ctx, store.GetPagesForTagParams{
				TagID: tagID, Limit: limit, Offset: off,
			})
			if err == nil {
				total, err = h.queries.CountPagesForTag(ctx, tagID)
			}
		}
	} else if status != "" {
		// Filter by status
		pages, err = h.queries.ListPagesByStatus(ctx, store.ListPagesByStatusParams{
			Status: status, Limit: limit, Offset: off,
		})
		if err == nil {
			total, err = h.queries.CountPagesByStatus(ctx, status)
		}
	} else {
		// All pages (authenticated only - unauthenticated requests have status set to "published")
		pages, err = h.queries.ListPages(ctx, store.ListPagesParams{
			Limit: limit, Offset: off,
		})
		if err == nil {
			total, err = h.queries.CountPages(ctx)
		}
	}

	if err != nil {
		WriteInternalError(w, "Failed to list pages")
		return
	}

	// Parse includes
	includeAuthor := false
	includeCategories := false
	includeTags := false
	if include != "" {
		includes := strings.Split(include, ",")
		for _, inc := range includes {
			switch strings.TrimSpace(inc) {
			case "author":
				includeAuthor = true
			case "categories":
				includeCategories = true
			case "tags":
				includeTags = true
			}
		}
	}

	// Convert to response
	responses := make([]PageResponse, 0, len(pages))
	for _, p := range pages {
		resp := storePageToResponse(p)

		if includeAuthor {
			author, authorErr := h.queries.GetPageAuthor(ctx, p.ID)
			if authorErr == nil {
				resp.Author = &AuthorResponse{
					ID:    author.ID,
					Name:  author.Name,
					Email: author.Email,
				}
			}
		}

		if includeCategories {
			categories, catErr := h.queries.GetCategoriesForPage(ctx, p.ID)
			if catErr == nil {
				resp.Categories = make([]CategoryResponse, 0, len(categories))
				for _, c := range categories {
					catResp := CategoryResponse{
						ID:   c.ID,
						Name: c.Name,
						Slug: c.Slug,
					}
					if c.Description.Valid {
						catResp.Description = c.Description.String
					}
					resp.Categories = append(resp.Categories, catResp)
				}
			}
		}

		if includeTags {
			tags, tagErr := h.queries.GetTagsForPage(ctx, p.ID)
			if tagErr == nil {
				resp.Tags = make([]TagResponse, 0, len(tags))
				for _, t := range tags {
					resp.Tags = append(resp.Tags, TagResponse{
						ID:   t.ID,
						Name: t.Name,
						Slug: t.Slug,
					})
				}
			}
		}

		responses = append(responses, resp)
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

// GetPage handles GET /api/v1/pages/{id}
// Public: returns only if published
// With API key: returns any page
func (h *Handler) GetPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	include := r.URL.Query().Get("include")

	page, ok := h.requirePageForAPI(w, r)
	if !ok {
		return
	}

	// Check access for non-published pages
	apiKey := middleware.GetAPIKey(r)
	if page.Status != model.PageStatusPublished && apiKey == nil {
		WriteNotFound(w, "Page not found")
		return
	}

	resp := storePageToResponse(page)
	h.populatePageIncludes(ctx, &resp, page.ID, include)

	WriteSuccess(w, resp, nil)
}

// GetPageBySlug handles GET /api/v1/pages/slug/{slug}
// Public: returns only published pages
func (h *Handler) GetPageBySlug(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")
	include := r.URL.Query().Get("include")

	if slug == "" {
		WriteBadRequest(w, "Slug is required", nil)
		return
	}

	// Check authentication
	apiKey := middleware.GetAPIKey(r)

	var page store.Page
	var err error

	if apiKey != nil {
		// Authenticated: can get any page by slug
		page, err = h.queries.GetPageBySlug(ctx, slug)
	} else {
		// Public: only published pages
		page, err = h.queries.GetPublishedPageBySlug(ctx, slug)
	}

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteNotFound(w, "Page not found")
		} else {
			WriteInternalError(w, "Failed to retrieve page")
		}
		return
	}

	resp := storePageToResponse(page)
	h.populatePageIncludes(ctx, &resp, page.ID, include)

	WriteSuccess(w, resp, nil)
}

// CreatePage handles POST /api/v1/pages
// Requires pages:write permission
func (h *Handler) CreatePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req CreatePageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteBadRequest(w, "Invalid JSON body", nil)
		return
	}

	// Validate required fields
	validationErrors := make(map[string]string)
	if req.Title == "" {
		validationErrors["title"] = "Title is required"
	}
	if req.Slug == "" {
		validationErrors["slug"] = "Slug is required"
	}
	if len(validationErrors) > 0 {
		WriteValidationError(w, validationErrors)
		return
	}

	// Validate status
	if req.Status == "" {
		req.Status = model.PageStatusDraft
	}
	if req.Status != model.PageStatusDraft && req.Status != model.PageStatusPublished {
		WriteValidationError(w, map[string]string{"status": "Status must be 'draft' or 'published'"})
		return
	}

	// Check slug uniqueness
	exists, err := h.queries.SlugExists(ctx, req.Slug)
	if err != nil {
		WriteInternalError(w, "Failed to check slug")
		return
	}
	if exists == 1 {
		WriteValidationError(w, map[string]string{"slug": "Slug already exists"})
		return
	}

	// Get author from API key
	apiKey := middleware.GetAPIKey(r)
	if apiKey == nil {
		WriteUnauthorized(w, "API key required")
		return
	}

	now := time.Now()

	// Prepare create params
	params := store.CreatePageParams{
		Title:     req.Title,
		Slug:      req.Slug,
		Body:      req.Body,
		Status:    req.Status,
		AuthorID:  apiKey.CreatedBy, // Use API key creator as author
		CreatedAt: now,
		UpdatedAt: now,
	}

	if req.FeaturedImageID != nil {
		params.FeaturedImageID = sql.NullInt64{Int64: *req.FeaturedImageID, Valid: true}
	}
	if req.OGImageID != nil {
		params.OgImageID = sql.NullInt64{Int64: *req.OGImageID, Valid: true}
	}
	if req.ScheduledAt != nil && *req.ScheduledAt != "" {
		t, parseErr := time.Parse(time.RFC3339, *req.ScheduledAt)
		if parseErr != nil {
			WriteValidationError(w, map[string]string{"scheduled_at": "Invalid date format. Use RFC3339 (e.g., 2024-01-01T00:00:00Z)"})
			return
		}
		params.ScheduledAt = sql.NullTime{Time: t, Valid: true}
	}

	params.MetaTitle = req.MetaTitle
	params.MetaDescription = req.MetaDescription
	params.MetaKeywords = req.MetaKeywords
	params.CanonicalUrl = req.CanonicalURL

	if req.NoIndex {
		params.NoIndex = 1
	}
	if req.NoFollow {
		params.NoFollow = 1
	}

	// Create page
	page, err := h.queries.CreatePage(ctx, params)
	if err != nil {
		WriteInternalError(w, "Failed to create page")
		return
	}

	// Add categories
	if len(req.CategoryIDs) > 0 {
		for _, catID := range req.CategoryIDs {
			_ = h.queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
				PageID:     page.ID,
				CategoryID: catID,
			})
		}
	}

	// Add tags
	if len(req.TagIDs) > 0 {
		for _, tagID := range req.TagIDs {
			_ = h.queries.AddTagToPage(ctx, store.AddTagToPageParams{
				PageID: page.ID,
				TagID:  tagID,
			})
		}
	}

	resp := storePageToResponse(page)

	// Include categories and tags in response
	if len(req.CategoryIDs) > 0 {
		categories, _ := h.queries.GetCategoriesForPage(ctx, page.ID)
		resp.Categories = make([]CategoryResponse, 0, len(categories))
		for _, c := range categories {
			catResp := CategoryResponse{ID: c.ID, Name: c.Name, Slug: c.Slug}
			if c.Description.Valid {
				catResp.Description = c.Description.String
			}
			resp.Categories = append(resp.Categories, catResp)
		}
	}
	if len(req.TagIDs) > 0 {
		tags, _ := h.queries.GetTagsForPage(ctx, page.ID)
		resp.Tags = make([]TagResponse, 0, len(tags))
		for _, t := range tags {
			resp.Tags = append(resp.Tags, TagResponse{ID: t.ID, Name: t.Name, Slug: t.Slug})
		}
	}

	WriteCreated(w, resp)
}

// UpdatePage handles PUT /api/v1/pages/{id}
// Requires pages:write permission
func (h *Handler) UpdatePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	existing, ok := h.requirePageForAPI(w, r)
	if !ok {
		return
	}

	var req UpdatePageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteBadRequest(w, "Invalid JSON body", nil)
		return
	}

	// Build update params, starting with existing values
	params := store.UpdatePageParams{
		ID:              existing.ID,
		Title:           existing.Title,
		Slug:            existing.Slug,
		Body:            existing.Body,
		Status:          existing.Status,
		FeaturedImageID: existing.FeaturedImageID,
		OgImageID:       existing.OgImageID,
		MetaTitle:       existing.MetaTitle,
		MetaDescription: existing.MetaDescription,
		MetaKeywords:    existing.MetaKeywords,
		CanonicalUrl:    existing.CanonicalUrl,
		NoIndex:         existing.NoIndex,
		NoFollow:        existing.NoFollow,
		ScheduledAt:     existing.ScheduledAt,
		LanguageID:      existing.LanguageID,
		UpdatedAt:       time.Now(),
	}

	// Apply updates
	if req.Title != nil {
		params.Title = *req.Title
	}
	if req.Slug != nil {
		// Check slug uniqueness
		exists, err := h.queries.SlugExistsExcluding(ctx, store.SlugExistsExcludingParams{
			Slug: *req.Slug,
			ID:   existing.ID,
		})
		if err != nil {
			WriteInternalError(w, "Failed to check slug")
			return
		}
		if exists == 1 {
			WriteValidationError(w, map[string]string{"slug": "Slug already exists"})
			return
		}
		params.Slug = *req.Slug
	}
	if req.Body != nil {
		params.Body = *req.Body
	}
	if req.Status != nil {
		if *req.Status != model.PageStatusDraft && *req.Status != model.PageStatusPublished {
			WriteValidationError(w, map[string]string{"status": "Status must be 'draft' or 'published'"})
			return
		}
		params.Status = *req.Status
	}
	if req.FeaturedImageID != nil {
		params.FeaturedImageID = sql.NullInt64{Int64: *req.FeaturedImageID, Valid: true}
	}
	if req.OGImageID != nil {
		params.OgImageID = sql.NullInt64{Int64: *req.OGImageID, Valid: true}
	}
	if req.MetaTitle != nil {
		params.MetaTitle = *req.MetaTitle
	}
	if req.MetaDescription != nil {
		params.MetaDescription = *req.MetaDescription
	}
	if req.MetaKeywords != nil {
		params.MetaKeywords = *req.MetaKeywords
	}
	if req.CanonicalURL != nil {
		params.CanonicalUrl = *req.CanonicalURL
	}
	if req.NoIndex != nil {
		if *req.NoIndex {
			params.NoIndex = 1
		} else {
			params.NoIndex = 0
		}
	}
	if req.NoFollow != nil {
		if *req.NoFollow {
			params.NoFollow = 1
		} else {
			params.NoFollow = 0
		}
	}
	if req.ScheduledAt != nil {
		if *req.ScheduledAt == "" {
			params.ScheduledAt = sql.NullTime{Valid: false}
		} else {
			t, parseErr := time.Parse(time.RFC3339, *req.ScheduledAt)
			if parseErr != nil {
				WriteValidationError(w, map[string]string{"scheduled_at": "Invalid date format. Use RFC3339"})
				return
			}
			params.ScheduledAt = sql.NullTime{Time: t, Valid: true}
		}
	}

	// Update page
	page, err := h.queries.UpdatePage(ctx, params)
	if err != nil {
		WriteInternalError(w, "Failed to update page")
		return
	}

	// Update categories if provided
	if req.CategoryIDs != nil {
		_ = h.queries.ClearPageCategories(ctx, existing.ID)
		for _, catID := range *req.CategoryIDs {
			_ = h.queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
				PageID:     existing.ID,
				CategoryID: catID,
			})
		}
	}

	// Update tags if provided
	if req.TagIDs != nil {
		_ = h.queries.ClearPageTags(ctx, existing.ID)
		for _, tagID := range *req.TagIDs {
			_ = h.queries.AddTagToPage(ctx, store.AddTagToPageParams{
				PageID: existing.ID,
				TagID:  tagID,
			})
		}
	}

	resp := storePageToResponse(page)

	// Include categories and tags
	categories, _ := h.queries.GetCategoriesForPage(ctx, page.ID)
	if len(categories) > 0 {
		resp.Categories = make([]CategoryResponse, 0, len(categories))
		for _, c := range categories {
			catResp := CategoryResponse{ID: c.ID, Name: c.Name, Slug: c.Slug}
			if c.Description.Valid {
				catResp.Description = c.Description.String
			}
			resp.Categories = append(resp.Categories, catResp)
		}
	}

	tags, _ := h.queries.GetTagsForPage(ctx, page.ID)
	if len(tags) > 0 {
		resp.Tags = make([]TagResponse, 0, len(tags))
		for _, t := range tags {
			resp.Tags = append(resp.Tags, TagResponse{ID: t.ID, Name: t.Name, Slug: t.Slug})
		}
	}

	WriteSuccess(w, resp, nil)
}

// DeletePage handles DELETE /api/v1/pages/{id}
// Requires pages:write permission
func (h *Handler) DeletePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, ok := h.requirePageForAPI(w, r)
	if !ok {
		return
	}

	// Delete associated data
	_ = h.queries.ClearPageCategories(ctx, page.ID)
	_ = h.queries.ClearPageTags(ctx, page.ID)
	_ = h.queries.DeletePageVersions(ctx, page.ID)

	// Delete page
	if err := h.queries.DeletePage(ctx, page.ID); err != nil {
		WriteInternalError(w, "Failed to delete page")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// populatePageIncludes adds related data to a page response based on include parameter.
func (h *Handler) populatePageIncludes(ctx context.Context, resp *PageResponse, pageID int64, include string) {
	if include == "" {
		return
	}

	includes := strings.Split(include, ",")
	for _, inc := range includes {
		switch strings.TrimSpace(inc) {
		case "author":
			author, err := h.queries.GetPageAuthor(ctx, pageID)
			if err == nil {
				resp.Author = &AuthorResponse{
					ID:    author.ID,
					Name:  author.Name,
					Email: author.Email,
				}
			}
		case "categories":
			categories, err := h.queries.GetCategoriesForPage(ctx, pageID)
			if err == nil {
				resp.Categories = make([]CategoryResponse, 0, len(categories))
				for _, c := range categories {
					catResp := CategoryResponse{
						ID:   c.ID,
						Name: c.Name,
						Slug: c.Slug,
					}
					if c.Description.Valid {
						catResp.Description = c.Description.String
					}
					resp.Categories = append(resp.Categories, catResp)
				}
			}
		case "tags":
			tags, err := h.queries.GetTagsForPage(ctx, pageID)
			if err == nil {
				resp.Tags = make([]TagResponse, 0, len(tags))
				for _, t := range tags {
					resp.Tags = append(resp.Tags, TagResponse{
						ID:   t.ID,
						Name: t.Name,
						Slug: t.Slug,
					})
				}
			}
		}
	}
}

// requirePageForAPI parses page ID from URL and fetches the page.
// Returns the page and true if successful, or zero value and false if an error occurred (response already written).
func (h *Handler) requirePageForAPI(w http.ResponseWriter, r *http.Request) (store.Page, bool) {
	id, err := handler.ParseIDParam(r)
	if err != nil {
		WriteBadRequest(w, "Invalid page ID", nil)
		return store.Page{}, false
	}

	page, err := h.queries.GetPageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteNotFound(w, "Page not found")
		} else {
			WriteInternalError(w, "Failed to retrieve page")
		}
		return store.Page{}, false
	}
	return page, true
}
