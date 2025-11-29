package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
	"ocms-go/internal/util"
)

// TagsPerPage is the number of tags to display per page.
const TagsPerPage = 20

// TaxonomyHandler handles tag and category management routes.
type TaxonomyHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewTaxonomyHandler creates a new TaxonomyHandler.
func NewTaxonomyHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *TaxonomyHandler {
	return &TaxonomyHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// TagsListData holds data for the tags list template.
type TagsListData struct {
	Tags        []store.GetTagUsageCountsRow
	CurrentPage int
	TotalPages  int
	TotalCount  int64
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
}

// ListTags handles GET /admin/tags - displays a paginated list of tags.
func (h *TaxonomyHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get page number from query string
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get total count
	totalCount, err := h.queries.CountTags(r.Context())
	if err != nil {
		slog.Error("failed to count tags", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Calculate pagination
	totalPages := int((totalCount + TagsPerPage - 1) / TagsPerPage)
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := int64((page - 1) * TagsPerPage)

	// Fetch tags with usage counts
	tags, err := h.queries.GetTagUsageCounts(r.Context(), store.GetTagUsageCountsParams{
		Limit:  TagsPerPage,
		Offset: offset,
	})
	if err != nil {
		slog.Error("failed to list tags", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := TagsListData{
		Tags:        tags,
		CurrentPage: page,
		TotalPages:  totalPages,
		TotalCount:  totalCount,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
	}

	if err := h.renderer.Render(w, r, "admin/tags_list", render.TemplateData{
		Title: "Tags",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// TagFormData holds data for the tag form template.
type TagFormData struct {
	Tag        *store.Tag
	Errors     map[string]string
	FormValues map[string]string
	IsEdit     bool
}

// NewTagForm handles GET /admin/tags/new - displays the new tag form.
func (h *TaxonomyHandler) NewTagForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	data := TagFormData{
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     false,
	}

	if err := h.renderer.Render(w, r, "admin/tags_form", render.TemplateData{
		Title: "New Tag",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// CreateTag handles POST /admin/tags - creates a new tag.
func (h *TaxonomyHandler) CreateTag(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/tags/new", http.StatusSeeOther)
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name": name,
		"slug": slug,
	}

	// Validate
	errors := make(map[string]string)

	// Name validation
	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) < 2 {
		errors["name"] = "Name must be at least 2 characters"
	}

	// Slug validation - auto-generate if empty
	if slug == "" {
		slug = util.Slugify(name)
		formValues["slug"] = slug
	}

	if slug == "" {
		errors["slug"] = "Slug is required"
	} else if !util.IsValidSlug(slug) {
		errors["slug"] = "Invalid slug format (use lowercase letters, numbers, and hyphens)"
	} else {
		// Check if slug already exists
		exists, err := h.queries.TagSlugExists(r.Context(), slug)
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			errors["slug"] = "Error checking slug"
		} else if exists != 0 {
			errors["slug"] = "Slug already exists"
		}
	}

	// If there are validation errors, re-render the form
	if len(errors) > 0 {
		data := TagFormData{
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     false,
		}

		if err := h.renderer.Render(w, r, "admin/tags_form", render.TemplateData{
			Title: "New Tag",
			User:  user,
			Data:  data,
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Create tag
	now := time.Now()
	newTag, err := h.queries.CreateTag(r.Context(), store.CreateTagParams{
		Name:      name,
		Slug:      slug,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create tag", "error", err)
		h.renderer.SetFlash(r, "Error creating tag", "error")
		http.Redirect(w, r, "/admin/tags/new", http.StatusSeeOther)
		return
	}

	slog.Info("tag created", "tag_id", newTag.ID, "slug", newTag.Slug, "created_by", user.ID)
	h.renderer.SetFlash(r, "Tag created successfully", "success")
	http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
}

// EditTagForm handles GET /admin/tags/{id} - displays the edit tag form.
func (h *TaxonomyHandler) EditTagForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get tag ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid tag ID", "error")
		http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
		return
	}

	// Get tag from database
	tag, err := h.queries.GetTagByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Tag not found", "error")
		} else {
			slog.Error("failed to get tag", "error", err, "tag_id", id)
			h.renderer.SetFlash(r, "Error loading tag", "error")
		}
		http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
		return
	}

	data := TagFormData{
		Tag:        &tag,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     true,
	}

	if err := h.renderer.Render(w, r, "admin/tags_form", render.TemplateData{
		Title: "Edit Tag",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// UpdateTag handles PUT /admin/tags/{id} - updates an existing tag.
func (h *TaxonomyHandler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get tag ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid tag ID", "error")
		http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
		return
	}

	// Get existing tag
	existingTag, err := h.queries.GetTagByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Tag not found", "error")
		} else {
			slog.Error("failed to get tag", "error", err, "tag_id", id)
			h.renderer.SetFlash(r, "Error loading tag", "error")
		}
		http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", id), http.StatusSeeOther)
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name": name,
		"slug": slug,
	}

	// Validate
	errors := make(map[string]string)

	// Name validation
	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) < 2 {
		errors["name"] = "Name must be at least 2 characters"
	}

	// Slug validation
	if slug == "" {
		slug = util.Slugify(name)
		formValues["slug"] = slug
	}

	if slug == "" {
		errors["slug"] = "Slug is required"
	} else if !util.IsValidSlug(slug) {
		errors["slug"] = "Invalid slug format (use lowercase letters, numbers, and hyphens)"
	} else if slug != existingTag.Slug {
		// Only check for uniqueness if slug changed
		exists, err := h.queries.TagSlugExistsExcluding(r.Context(), store.TagSlugExistsExcludingParams{
			Slug: slug,
			ID:   id,
		})
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			errors["slug"] = "Error checking slug"
		} else if exists != 0 {
			errors["slug"] = "Slug already exists"
		}
	}

	// If there are validation errors, re-render the form
	if len(errors) > 0 {
		data := TagFormData{
			Tag:        &existingTag,
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     true,
		}

		if err := h.renderer.Render(w, r, "admin/tags_form", render.TemplateData{
			Title: "Edit Tag",
			User:  user,
			Data:  data,
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Update tag
	now := time.Now()
	updatedTag, err := h.queries.UpdateTag(r.Context(), store.UpdateTagParams{
		ID:        id,
		Name:      name,
		Slug:      slug,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to update tag", "error", err, "tag_id", id)
		h.renderer.SetFlash(r, "Error updating tag", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", id), http.StatusSeeOther)
		return
	}

	slog.Info("tag updated", "tag_id", updatedTag.ID, "slug", updatedTag.Slug, "updated_by", user.ID)
	h.renderer.SetFlash(r, "Tag updated successfully", "success")
	http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
}

// DeleteTag handles DELETE /admin/tags/{id} - deletes a tag.
func (h *TaxonomyHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	// Get tag ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid tag ID", http.StatusBadRequest)
		return
	}

	// Get tag to verify it exists and for logging
	tag, err := h.queries.GetTagByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Tag not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get tag", "error", err, "tag_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Delete the tag (page_tags are cascade deleted by FK constraint)
	err = h.queries.DeleteTag(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete tag", "error", err, "tag_id", id)
		http.Error(w, "Error deleting tag", http.StatusInternalServerError)
		return
	}

	user := middleware.GetUser(r)
	slog.Info("tag deleted", "tag_id", id, "slug", tag.Slug, "deleted_by", user.ID)

	// For HTMX requests, return empty response (row removed)
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For regular requests, redirect
	h.renderer.SetFlash(r, "Tag deleted successfully", "success")
	http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
}

// SearchTags handles GET /admin/tags/search - AJAX search for autocomplete.
func (h *TaxonomyHandler) SearchTags(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var tags []store.Tag
	var err error

	if query == "" {
		// Return all tags if no query
		tags, err = h.queries.ListAllTags(r.Context())
	} else {
		// Search tags by name
		tags, err = h.queries.SearchTags(r.Context(), store.SearchTagsParams{
			Name:  "%" + query + "%",
			Limit: 20,
		})
	}

	if err != nil {
		slog.Error("failed to search tags", "error", err, "query", query)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(tags); err != nil {
		slog.Error("failed to encode tags response", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}
