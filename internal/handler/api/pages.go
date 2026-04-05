// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package api provides REST API handlers for the CMS.
package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// PageResponse represents a page in API responses.
type PageResponse struct {
	ID                int64              `json:"id"`
	Title             string             `json:"title"`
	Slug              string             `json:"slug"`
	Body              string             `json:"body"`
	Status            string             `json:"status"`
	PageType          string             `json:"page_type"`
	AuthorID          int64              `json:"author_id"`
	LanguageCode      string             `json:"language_code"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	PublishedAt       *time.Time         `json:"published_at,omitempty"`
	FeaturedImageID   *int64             `json:"featured_image_id,omitempty"`
	HideFeaturedImage bool               `json:"hide_featured_image"`
	ExcludeFromLists  bool               `json:"exclude_from_lists"`
	MetaTitle         string             `json:"meta_title,omitempty"`
	MetaDescription   string             `json:"meta_description,omitempty"`
	MetaKeywords      string             `json:"meta_keywords,omitempty"`
	OGImageID         *int64             `json:"og_image_id,omitempty"`
	NoIndex           bool               `json:"no_index"`
	NoFollow          bool               `json:"no_follow"`
	CanonicalURL      string             `json:"canonical_url,omitempty"`
	ScheduledAt       *time.Time         `json:"scheduled_at,omitempty"`
	VideoURL          string             `json:"video_url,omitempty"`
	VideoTitle        string             `json:"video_title,omitempty"`
	Author            *AuthorResponse    `json:"author,omitempty"`
	Categories        []CategoryResponse `json:"categories,omitempty"`
	Tags              []TagResponse      `json:"tags,omitempty"`
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
	Title             string   `json:"title"`
	Slug              string   `json:"slug"`
	Body              string   `json:"body"`
	Status            string   `json:"status"`
	PageType          string   `json:"page_type,omitempty"`
	LanguageCode      *string  `json:"language_code,omitempty"`
	FeaturedImageID   *int64   `json:"featured_image_id,omitempty"`
	HideFeaturedImage bool     `json:"hide_featured_image"`
	ExcludeFromLists  bool     `json:"exclude_from_lists"`
	MetaTitle         string   `json:"meta_title,omitempty"`
	MetaDescription   string   `json:"meta_description,omitempty"`
	MetaKeywords      string   `json:"meta_keywords,omitempty"`
	OGImageID         *int64   `json:"og_image_id,omitempty"`
	NoIndex           bool     `json:"no_index"`
	NoFollow          bool     `json:"no_follow"`
	CanonicalURL      string   `json:"canonical_url,omitempty"`
	ScheduledAt       *string  `json:"scheduled_at,omitempty"`
	CategoryIDs       []int64  `json:"category_ids,omitempty"`
	TagIDs            []int64  `json:"tag_ids,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	VideoURL          string   `json:"video_url,omitempty"`
	VideoTitle        string   `json:"video_title,omitempty"`
}

// UpdatePageRequest represents the request body for updating a page.
type UpdatePageRequest struct {
	Title             *string   `json:"title,omitempty"`
	Slug              *string   `json:"slug,omitempty"`
	Body              *string   `json:"body,omitempty"`
	Status            *string   `json:"status,omitempty"`
	PageType          *string   `json:"page_type,omitempty"`
	FeaturedImageID   *int64    `json:"featured_image_id,omitempty"`
	HideFeaturedImage *bool     `json:"hide_featured_image,omitempty"`
	ExcludeFromLists  *bool     `json:"exclude_from_lists,omitempty"`
	MetaTitle         *string   `json:"meta_title,omitempty"`
	MetaDescription   *string   `json:"meta_description,omitempty"`
	MetaKeywords      *string   `json:"meta_keywords,omitempty"`
	OGImageID         *int64    `json:"og_image_id,omitempty"`
	NoIndex           *bool     `json:"no_index,omitempty"`
	NoFollow          *bool     `json:"no_follow,omitempty"`
	CanonicalURL      *string   `json:"canonical_url,omitempty"`
	ScheduledAt       *string   `json:"scheduled_at,omitempty"`
	CategoryIDs       *[]int64  `json:"category_ids,omitempty"`
	TagIDs            *[]int64  `json:"tag_ids,omitempty"`
	Tags              *[]string `json:"tags,omitempty"`
	VideoURL          *string   `json:"video_url,omitempty"`
	VideoTitle        *string   `json:"video_title,omitempty"`
}

// storeCategoryToResponse converts a store.Category to CategoryResponse.
func storeCategoryToResponse(c store.Category) CategoryResponse {
	resp := CategoryResponse{
		ID:   c.ID,
		Name: c.Name,
		Slug: c.Slug,
	}
	if c.Description.Valid {
		resp.Description = c.Description.String
	}
	return resp
}

// storeTagToResponse converts a store.Tag to TagResponse.
func storeTagToResponse(t store.Tag) TagResponse {
	return TagResponse{
		ID:   t.ID,
		Name: t.Name,
		Slug: t.Slug,
	}
}

// pageFilterType defines the filter for listing pages by category or tag.
type pageFilterType int

const (
	filterByCategory pageFilterType = iota
	filterByTag
)

var suspiciousPageMarkupTokens = []string{
	"<script",
	"onerror=",
	"onload=",
	"<iframe",
}

// javascriptURIPattern matches javascript: in attribute contexts only,
// avoiding false positives on plain text like "JavaScript: a language".
var javascriptURIPattern = regexp.MustCompile(`(?i)=\s*["']?\s*javascript:`)

// listPagesByFilter returns pages filtered by category or tag with published-only option.
func (h *Handler) listPagesByFilter(ctx context.Context, publishedOnly bool, filterType pageFilterType, filterID, limit, offset int64) ([]store.Page, int64, error) {
	// Select query functions based on filter type and published status
	var listFn func() ([]store.Page, error)
	var countFn func() (int64, error)

	switch {
	case filterType == filterByCategory && publishedOnly:
		listFn = func() ([]store.Page, error) {
			return h.queries.ListPublishedPagesByCategory(ctx, store.ListPublishedPagesByCategoryParams{
				CategoryID: filterID, Limit: limit, Offset: offset})
		}
		countFn = func() (int64, error) { return h.queries.CountPublishedPagesByCategory(ctx, filterID) }
	case filterType == filterByCategory:
		listFn = func() ([]store.Page, error) {
			return h.queries.ListPagesByCategory(ctx, store.ListPagesByCategoryParams{
				CategoryID: filterID, Limit: limit, Offset: offset})
		}
		countFn = func() (int64, error) { return h.queries.CountPagesByCategory(ctx, filterID) }
	case publishedOnly: // filterByTag
		listFn = func() ([]store.Page, error) {
			return h.queries.ListPublishedPagesForTag(ctx, store.ListPublishedPagesForTagParams{
				TagID: filterID, Limit: limit, Offset: offset})
		}
		countFn = func() (int64, error) { return h.queries.CountPublishedPagesForTag(ctx, filterID) }
	default: // filterByTag, all pages
		listFn = func() ([]store.Page, error) {
			return h.queries.GetPagesForTag(ctx, store.GetPagesForTagParams{
				TagID: filterID, Limit: limit, Offset: offset})
		}
		countFn = func() (int64, error) { return h.queries.CountPagesForTag(ctx, filterID) }
	}

	return handler.ListAndCount(listFn, countFn)
}

// storePageToResponse converts a store.Page to PageResponse.
func storePageToResponse(p store.Page) PageResponse {
	resp := PageResponse{
		ID:                p.ID,
		Title:             p.Title,
		Slug:              p.Slug,
		Body:              p.Body,
		Status:            p.Status,
		PageType:          p.PageType,
		AuthorID:          p.AuthorID,
		LanguageCode:      p.LanguageCode,
		CreatedAt:         p.CreatedAt,
		UpdatedAt:         p.UpdatedAt,
		MetaTitle:         p.MetaTitle,
		MetaDescription:   p.MetaDescription,
		MetaKeywords:      p.MetaKeywords,
		HideFeaturedImage: p.HideFeaturedImage != 0,
		ExcludeFromLists:  p.ExcludeFromLists != 0,
		NoIndex:           p.NoIndex != 0,
		NoFollow:          p.NoFollow != 0,
		CanonicalURL:      p.CanonicalUrl,
		VideoURL:          p.VideoUrl,
		VideoTitle:        p.VideoTitle,
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
	switch {
	case categoryIDStr != "":
		categoryID, parseErr := strconv.ParseInt(categoryIDStr, 10, 64)
		if parseErr != nil {
			WriteBadRequest(w, "Invalid category ID", nil)
			return
		}
		pages, total, err = h.listPagesByFilter(ctx, publishedOnly, filterByCategory, categoryID, limit, off)
	case tagIDStr != "":
		// Filter by tag
		tagID, parseErr := strconv.ParseInt(tagIDStr, 10, 64)
		if parseErr != nil {
			WriteBadRequest(w, "Invalid tag ID", nil)
			return
		}
		pages, total, err = h.listPagesByFilter(ctx, publishedOnly, filterByTag, tagID, limit, off)
	case status != "":
		// Filter by status
		pages, total, err = handler.ListAndCount(
			func() ([]store.Page, error) {
				return h.queries.ListPagesByStatus(ctx, store.ListPagesByStatusParams{
					Status: status, Limit: limit, Offset: off,
				})
			},
			func() (int64, error) { return h.queries.CountPagesByStatus(ctx, status) },
		)
	default:
		// All pages (authenticated only - unauthenticated requests have status set to "published")
		pages, total, err = handler.ListAndCount(
			func() ([]store.Page, error) {
				return h.queries.ListPages(ctx, store.ListPagesParams{
					Limit: limit, Offset: off,
				})
			},
			func() (int64, error) { return h.queries.CountPages(ctx) },
		)
	}

	if err != nil {
		LogAndWriteInternalError(w, "Failed to list pages", "error", err)
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
			h.populatePageAuthor(ctx, &resp, p.ID)
		}

		if includeCategories {
			h.populatePageCategories(ctx, &resp, p.ID)
		}

		if includeTags {
			h.populatePageTags(ctx, &resp, p.ID)
		}

		responses = append(responses, resp)
	}

	WriteSuccess(w, responses, &Meta{
		Total:   total,
		Page:    page,
		PerPage: perPage,
		Pages:   handler.CalculateTotalPages(int(total), perPage),
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
			LogAndWriteInternalError(w, "Failed to retrieve page", "error", err)
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
	if err := decodeJSON(w, r, &req, maxAPIJSONBodyBytes); err != nil {
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
	if bodyErr := validatePageBodyMarkupPolicy(req.Body, h.blockSuspiciousPageMarkup); bodyErr != "" {
		WriteValidationError(w, map[string]string{"body": bodyErr})
		return
	}
	normalizedBody := sanitizePageBodyForStorage(req.Body, h.sanitizePageHTML)

	// Check slug uniqueness
	if !h.checkPageSlugUnique(w, ctx, req.Slug) {
		return
	}

	// Get author from API key
	apiKey := middleware.GetAPIKey(r)
	if apiKey == nil {
		WriteUnauthorized(w, "API key required")
		return
	}

	now := time.Now()

	// Resolve language code (default to system default language if not provided)
	langCode, langErr := h.resolveLanguageCode(ctx, req.LanguageCode)
	if langErr != nil {
		LogAndWriteInternalError(w, "Failed to resolve default language", "error", langErr)
		return
	}

	// Prepare create params
	params := store.CreatePageParams{
		Title:        req.Title,
		Slug:         req.Slug,
		Body:         normalizedBody,
		Status:       req.Status,
		AuthorID:     apiKey.CreatedBy, // Use API key creator as author
		LanguageCode: langCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if req.FeaturedImageID != nil {
		params.FeaturedImageID = util.NullInt64FromPtr(req.FeaturedImageID)
	}
	if req.OGImageID != nil {
		params.OgImageID = util.NullInt64FromPtr(req.OGImageID)
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
	if req.HideFeaturedImage {
		params.HideFeaturedImage = 1
	}
	if req.ExcludeFromLists {
		params.ExcludeFromLists = 1
	}

	// Set page type (default to "post" if not provided)
	params.PageType = req.PageType
	if params.PageType == "" {
		params.PageType = "post"
	}

	params.VideoUrl = req.VideoURL
	params.VideoTitle = req.VideoTitle

	// Set published_at if status is published
	if req.Status == model.PageStatusPublished {
		params.PublishedAt = sql.NullTime{Time: now, Valid: true}
	}

	// Pre-validate category IDs
	for _, catID := range req.CategoryIDs {
		if _, err := h.queries.GetCategoryByID(ctx, catID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				WriteValidationError(w, map[string]string{"category_ids": fmt.Sprintf("Category %d not found", catID)})
			} else {
				LogAndWriteInternalError(w, "Failed to validate category", "error", err, "category_id", catID)
			}
			return
		}
	}

	// All writes (page, categories, tags, new tag creation) in one transaction
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		LogAndWriteInternalError(w, "Failed to start transaction", "error", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck

	txq := h.queries.WithTx(tx)

	// Resolve tag names to IDs inside transaction (new tags roll back on failure)
	if len(req.Tags) > 0 {
		resolvedIDs, err := resolveTagNames(ctx, txq, req.Tags, langCode)
		if err != nil {
			writeResolveTagError(w, err)
			return
		}
		req.TagIDs = append(req.TagIDs, resolvedIDs...)
	}

	tagIDs := deduplicateInt64(req.TagIDs)

	// Pre-validate tag IDs
	for _, tagID := range tagIDs {
		if _, err := txq.GetTagByID(ctx, tagID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				WriteValidationError(w, map[string]string{"tag_ids": fmt.Sprintf("Tag %d not found", tagID)})
			} else {
				LogAndWriteInternalError(w, "Failed to validate tag", "error", err, "tag_id", tagID)
			}
			return
		}
	}

	page, err := txq.CreatePage(ctx, params)
	if err != nil {
		LogAndWriteInternalError(w, "Failed to create page", "error", err)
		return
	}

	for _, catID := range req.CategoryIDs {
		if err := txq.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
			PageID:     page.ID,
			CategoryID: catID,
		}); err != nil {
			LogAndWriteInternalError(w, "Failed to add category to page", "error", err, "page_id", page.ID, "category_id", catID)
			return
		}
	}

	for _, tagID := range tagIDs {
		if err := txq.AddTagToPage(ctx, store.AddTagToPageParams{
			PageID: page.ID,
			TagID:  tagID,
		}); err != nil {
			LogAndWriteInternalError(w, "Failed to add tag to page", "error", err, "page_id", page.ID, "tag_id", tagID)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		LogAndWriteInternalError(w, "Failed to commit page creation", "error", err)
		return
	}

	// Invalidate page cache (for sitemap regeneration on next request)
	h.invalidatePageCache(page.ID)

	resp := storePageToResponse(page)

	// Include categories and tags in response
	h.populatePageCategories(ctx, &resp, page.ID)
	h.populatePageTags(ctx, &resp, page.ID)

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
	if err := decodeJSON(w, r, &req, maxAPIJSONBodyBytes); err != nil {
		WriteBadRequest(w, "Invalid JSON body", nil)
		return
	}

	// Build update params, starting with existing values
	params := store.UpdatePageParams{
		ID:                existing.ID,
		Title:             existing.Title,
		Slug:              existing.Slug,
		Body:              existing.Body,
		Summary:           existing.Summary,
		Status:            existing.Status,
		PageType:          existing.PageType,
		FeaturedImageID:   existing.FeaturedImageID,
		OgImageID:         existing.OgImageID,
		MetaTitle:         existing.MetaTitle,
		MetaDescription:   existing.MetaDescription,
		MetaKeywords:      existing.MetaKeywords,
		CanonicalUrl:      existing.CanonicalUrl,
		NoIndex:           existing.NoIndex,
		NoFollow:          existing.NoFollow,
		ScheduledAt:       existing.ScheduledAt,
		LanguageCode:      existing.LanguageCode,
		HideFeaturedImage: existing.HideFeaturedImage,
		ExcludeFromLists:  existing.ExcludeFromLists,
		PublishedAt:       existing.PublishedAt,
		VideoUrl:          existing.VideoUrl,
		VideoTitle:        existing.VideoTitle,
		UpdatedAt:         time.Now(),
	}

	// Apply updates
	if req.Title != nil {
		params.Title = *req.Title
	}
	if req.Slug != nil {
		// Check slug uniqueness
		if !h.checkPageSlugUniqueExcluding(w, ctx, *req.Slug, existing.ID) {
			return
		}
		params.Slug = *req.Slug
	}
	if req.Body != nil {
		if bodyErr := validatePageBodyMarkupPolicy(*req.Body, h.blockSuspiciousPageMarkup); bodyErr != "" {
			WriteValidationError(w, map[string]string{"body": bodyErr})
			return
		}
		params.Body = sanitizePageBodyForStorage(*req.Body, h.sanitizePageHTML)
	}
	if req.Status != nil {
		if *req.Status != model.PageStatusDraft && *req.Status != model.PageStatusPublished {
			WriteValidationError(w, map[string]string{"status": "Status must be 'draft' or 'published'"})
			return
		}
		// Block unpublishing in demo mode
		if middleware.IsDemoMode() && existing.Status == model.PageStatusPublished && *req.Status != model.PageStatusPublished {
			WriteForbidden(w, middleware.DemoModeMessageDetailed(middleware.RestrictionUnpublishContent))
			return
		}
		params.Status = *req.Status
	}
	if req.FeaturedImageID != nil {
		params.FeaturedImageID = util.NullInt64FromPtr(req.FeaturedImageID)
	}
	if req.OGImageID != nil {
		params.OgImageID = util.NullInt64FromPtr(req.OGImageID)
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
	if req.HideFeaturedImage != nil {
		if *req.HideFeaturedImage {
			params.HideFeaturedImage = 1
		} else {
			params.HideFeaturedImage = 0
		}
	}
	if req.ExcludeFromLists != nil {
		if *req.ExcludeFromLists {
			params.ExcludeFromLists = 1
		} else {
			params.ExcludeFromLists = 0
		}
	}
	if req.PageType != nil {
		if *req.PageType != "post" && *req.PageType != "page" {
			WriteValidationError(w, map[string]string{"page_type": "Page type must be 'post' or 'page'"})
			return
		}
		params.PageType = *req.PageType
	}
	if req.VideoURL != nil {
		params.VideoUrl = *req.VideoURL
	}
	if req.VideoTitle != nil {
		params.VideoTitle = *req.VideoTitle
	}
	// Handle published_at when status changes to published
	if req.Status != nil && *req.Status == model.PageStatusPublished && existing.Status != model.PageStatusPublished {
		params.PublishedAt = sql.NullTime{Time: time.Now(), Valid: true}
	} else if req.Status != nil && *req.Status != model.PageStatusPublished {
		params.PublishedAt = sql.NullTime{Valid: false}
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

	// Pre-validate category IDs if provided
	if req.CategoryIDs != nil {
		for _, catID := range *req.CategoryIDs {
			if _, err := h.queries.GetCategoryByID(ctx, catID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					WriteValidationError(w, map[string]string{"category_ids": fmt.Sprintf("Category %d not found", catID)})
				} else {
					LogAndWriteInternalError(w, "Failed to validate category", "error", err, "category_id", catID)
				}
				return
			}
		}
	}

	// All writes in one transaction
	hasTags := req.TagIDs != nil || req.Tags != nil

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		LogAndWriteInternalError(w, "Failed to start transaction", "error", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck

	txq := h.queries.WithTx(tx)

	// Resolve tag names inside transaction (new tags roll back on failure)
	var tagIDs []int64
	if hasTags {
		if req.TagIDs != nil {
			tagIDs = append(tagIDs, *req.TagIDs...)
		}
		if req.Tags != nil {
			resolvedIDs, err := resolveTagNames(ctx, txq, *req.Tags, existing.LanguageCode)
			if err != nil {
				writeResolveTagError(w, err)
				return
			}
			tagIDs = append(tagIDs, resolvedIDs...)
		}
		tagIDs = deduplicateInt64(tagIDs)

		// Pre-validate tag IDs
		for _, tagID := range tagIDs {
			if _, err := txq.GetTagByID(ctx, tagID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					WriteValidationError(w, map[string]string{"tag_ids": fmt.Sprintf("Tag %d not found", tagID)})
				} else {
					LogAndWriteInternalError(w, "Failed to validate tag", "error", err, "tag_id", tagID)
				}
				return
			}
		}
	}

	page, err := txq.UpdatePage(ctx, params)
	if err != nil {
		LogAndWriteInternalError(w, "Failed to update page", "error", err, "page_id", existing.ID)
		return
	}

	if req.CategoryIDs != nil {
		if err := txq.ClearPageCategories(ctx, existing.ID); err != nil {
			LogAndWriteInternalError(w, "Failed to clear page categories", "error", err, "page_id", existing.ID)
			return
		}
		for _, catID := range *req.CategoryIDs {
			if err := txq.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
				PageID:     existing.ID,
				CategoryID: catID,
			}); err != nil {
				LogAndWriteInternalError(w, "Failed to add category to page", "error", err, "page_id", existing.ID, "category_id", catID)
				return
			}
		}
	}

	if hasTags {
		if err := txq.ClearPageTags(ctx, existing.ID); err != nil {
			LogAndWriteInternalError(w, "Failed to clear page tags", "error", err, "page_id", existing.ID)
			return
		}
		for _, tagID := range tagIDs {
			if err := txq.AddTagToPage(ctx, store.AddTagToPageParams{
				PageID: existing.ID,
				TagID:  tagID,
			}); err != nil {
				LogAndWriteInternalError(w, "Failed to add tag to page", "error", err, "page_id", existing.ID, "tag_id", tagID)
				return
			}
		}
	}

	if err := tx.Commit(); err != nil {
		LogAndWriteInternalError(w, "Failed to commit page update", "error", err, "page_id", existing.ID)
		return
	}

	// Invalidate page cache
	h.invalidatePageCache(page.ID)

	resp := storePageToResponse(page)

	// Include categories and tags
	h.populatePageCategories(ctx, &resp, page.ID)
	h.populatePageTags(ctx, &resp, page.ID)

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

	// Delete page and associated data in a transaction
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		LogAndWriteInternalError(w, "Failed to start transaction", "error", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck

	txq := h.queries.WithTx(tx)

	if err := txq.ClearPageCategories(ctx, page.ID); err != nil {
		LogAndWriteInternalError(w, "Failed to clear page categories", "error", err, "page_id", page.ID)
		return
	}
	if err := txq.ClearPageTags(ctx, page.ID); err != nil {
		LogAndWriteInternalError(w, "Failed to clear page tags", "error", err, "page_id", page.ID)
		return
	}
	if err := txq.DeletePageVersions(ctx, page.ID); err != nil {
		LogAndWriteInternalError(w, "Failed to delete page versions", "error", err, "page_id", page.ID)
		return
	}
	if err := txq.DeletePage(ctx, page.ID); err != nil {
		LogAndWriteInternalError(w, "Failed to delete page", "error", err, "page_id", page.ID)
		return
	}

	if err := tx.Commit(); err != nil {
		LogAndWriteInternalError(w, "Failed to commit page deletion", "error", err, "page_id", page.ID)
		return
	}

	// Invalidate page cache
	h.invalidatePageCache(page.ID)

	w.WriteHeader(http.StatusNoContent)
}

const (
	maxTagsPerRequest = 50
	maxTagNameLength  = 100
)

// tagValidationError is returned by resolveTagNames for client input errors.
type tagValidationError struct {
	Field   string
	Message string
}

func (e *tagValidationError) Error() string { return e.Message }

// writeResolveTagError writes 422 for validation errors, 500 for internal errors.
func writeResolveTagError(w http.ResponseWriter, err error) {
	var ve *tagValidationError
	if errors.As(err, &ve) {
		WriteValidationError(w, map[string]string{ve.Field: ve.Message})
	} else {
		LogAndWriteInternalError(w, "Failed to resolve tag names", "error", err)
	}
}

// deduplicateInt64 returns a slice with duplicate values removed, preserving order.
func deduplicateInt64(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result
}

// resolveTagNames resolves tag names to IDs using the given queries handle,
// creating any tags that don't exist. Pass a transactional queries to ensure
// newly created tags are rolled back if the outer transaction fails.
func resolveTagNames(ctx context.Context, q *store.Queries, names []string, langCode string) ([]int64, error) {
	if len(names) > maxTagsPerRequest {
		return nil, &tagValidationError{Field: "tags", Message: fmt.Sprintf("Too many tags: %d exceeds maximum of %d", len(names), maxTagsPerRequest)}
	}
	ids := make([]int64, 0, len(names))
	now := time.Now()
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if len(name) > maxTagNameLength {
			return nil, &tagValidationError{Field: "tags", Message: fmt.Sprintf("Tag name too long: %d chars exceeds maximum of %d", len(name), maxTagNameLength)}
		}
		slug := util.Slugify(name)
		if slug == "" {
			continue
		}
		// Try to find existing tag by slug
		tag, err := q.GetTagBySlug(ctx, slug)
		if err == nil {
			ids = append(ids, tag.ID)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		// Tag doesn't exist — create it
		tag, err = q.CreateTag(ctx, store.CreateTagParams{
			Name:         name,
			Slug:         slug,
			LanguageCode: langCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			return nil, err
		}
		ids = append(ids, tag.ID)
	}
	return ids, nil
}

// populatePageAuthor fetches and populates author for a page response.
func (h *Handler) populatePageAuthor(ctx context.Context, resp *PageResponse, pageID int64) {
	author, err := h.queries.GetPageAuthor(ctx, pageID)
	if err != nil {
		return
	}
	resp.Author = &AuthorResponse{
		ID:    author.ID,
		Name:  author.Name,
		Email: author.Email,
	}
}

// populatePageCategories fetches and populates categories for a page response.
func (h *Handler) populatePageCategories(ctx context.Context, resp *PageResponse, pageID int64) {
	categories, err := h.queries.GetCategoriesForPage(ctx, pageID)
	if err != nil || len(categories) == 0 {
		return
	}
	resp.Categories = make([]CategoryResponse, 0, len(categories))
	for _, c := range categories {
		resp.Categories = append(resp.Categories, storeCategoryToResponse(c))
	}
}

// populatePageTags fetches and populates tags for a page response.
func (h *Handler) populatePageTags(ctx context.Context, resp *PageResponse, pageID int64) {
	tags, err := h.queries.GetTagsForPage(ctx, pageID)
	if err != nil || len(tags) == 0 {
		return
	}
	resp.Tags = make([]TagResponse, 0, len(tags))
	for _, t := range tags {
		resp.Tags = append(resp.Tags, storeTagToResponse(t))
	}
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
			h.populatePageAuthor(ctx, resp, pageID)
		case "categories":
			h.populatePageCategories(ctx, resp, pageID)
		case "tags":
			h.populatePageTags(ctx, resp, pageID)
		}
	}
}

// requirePageForAPI parses page ID from URL and fetches the page.
// Returns the page and true if successful, or zero value and false if an error occurred (response already written).
func (h *Handler) requirePageForAPI(w http.ResponseWriter, r *http.Request) (store.Page, bool) {
	return requireEntityByID(w, r, "page", func(id int64) (store.Page, error) {
		return h.queries.GetPageByID(r.Context(), id)
	})
}

// checkPageSlugUnique checks if a page slug is unique for creation.
// Returns true if unique, false if duplicate or error (response already written).
func (h *Handler) checkPageSlugUnique(w http.ResponseWriter, ctx context.Context, slug string) bool {
	return checkSlugUnique(w, func() (int64, error) {
		return h.queries.SlugExists(ctx, slug)
	})
}

// checkPageSlugUniqueExcluding checks if a page slug is unique for update (excluding current page).
// Returns true if unique, false if duplicate or error (response already written).
func (h *Handler) checkPageSlugUniqueExcluding(w http.ResponseWriter, ctx context.Context, slug string, pageID int64) bool {
	return checkSlugUnique(w, func() (int64, error) {
		return h.queries.SlugExistsExcluding(ctx, store.SlugExistsExcludingParams{
			Slug: slug,
			ID:   pageID,
		})
	})
}

func validatePageBodyMarkupPolicy(body string, blockSuspicious bool) string {
	if !blockSuspicious {
		return ""
	}
	if strings.TrimSpace(body) == "" {
		return ""
	}

	lowerBody := strings.ToLower(body)
	for _, token := range suspiciousPageMarkupTokens {
		if strings.Contains(lowerBody, token) {
			return "Body contains suspicious HTML markup that is blocked by policy"
		}
	}
	if javascriptURIPattern.MatchString(body) {
		return "Body contains suspicious HTML markup that is blocked by policy"
	}

	return ""
}

func sanitizePageBodyForStorage(raw string, enabled bool) string {
	if !enabled {
		return raw
	}
	return security.SanitizePageHTML(raw)
}
