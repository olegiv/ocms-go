package handler

import (
	"database/sql"
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

// Page statuses
const (
	PageStatusDraft     = "draft"
	PageStatusPublished = "published"
)

// ValidPageStatuses contains all valid page statuses.
var ValidPageStatuses = []string{PageStatusDraft, PageStatusPublished}

// PagesPerPage is the number of pages to display per page.
const PagesPerPage = 10

// PagesHandler handles page management routes.
type PagesHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewPagesHandler creates a new PagesHandler.
func NewPagesHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *PagesHandler {
	return &PagesHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// PagesListData holds data for the pages list template.
type PagesListData struct {
	Pages        []store.Page
	CurrentPage  int
	TotalPages   int
	TotalCount   int64
	HasPrev      bool
	HasNext      bool
	PrevPage     int
	NextPage     int
	StatusFilter string
	Statuses     []string
}

// List handles GET /admin/pages - displays a paginated list of pages.
func (h *PagesHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get page number from query string
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get status filter from query string
	statusFilter := r.URL.Query().Get("status")
	if statusFilter != "" && statusFilter != "all" && !isValidPageStatus(statusFilter) {
		statusFilter = ""
	}

	var totalCount int64
	var pages []store.Page
	var err error

	// Get total count based on filter
	if statusFilter != "" && statusFilter != "all" {
		totalCount, err = h.queries.CountPagesByStatus(r.Context(), statusFilter)
	} else {
		totalCount, err = h.queries.CountPages(r.Context())
	}
	if err != nil {
		slog.Error("failed to count pages", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Calculate pagination
	totalPages := int((totalCount + PagesPerPage - 1) / PagesPerPage)
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := int64((page - 1) * PagesPerPage)

	// Fetch pages for current page
	if statusFilter != "" && statusFilter != "all" {
		pages, err = h.queries.ListPagesByStatus(r.Context(), store.ListPagesByStatusParams{
			Status: statusFilter,
			Limit:  PagesPerPage,
			Offset: offset,
		})
	} else {
		pages, err = h.queries.ListPages(r.Context(), store.ListPagesParams{
			Limit:  PagesPerPage,
			Offset: offset,
		})
	}
	if err != nil {
		slog.Error("failed to list pages", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := PagesListData{
		Pages:        pages,
		CurrentPage:  page,
		TotalPages:   totalPages,
		TotalCount:   totalCount,
		HasPrev:      page > 1,
		HasNext:      page < totalPages,
		PrevPage:     page - 1,
		NextPage:     page + 1,
		StatusFilter: statusFilter,
		Statuses:     ValidPageStatuses,
	}

	if err := h.renderer.Render(w, r, "admin/pages_list", render.TemplateData{
		Title: "Pages",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// PageFormData holds data for the page form template.
type PageFormData struct {
	Page       *store.Page
	Statuses   []string
	Errors     map[string]string
	FormValues map[string]string
	IsEdit     bool
}

// NewForm handles GET /admin/pages/new - displays the new page form.
func (h *PagesHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	data := PageFormData{
		Statuses:   ValidPageStatuses,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     false,
	}

	if err := h.renderer.Render(w, r, "admin/pages_form", render.TemplateData{
		Title: "New Page",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Create handles POST /admin/pages - creates a new page.
func (h *PagesHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/pages/new", http.StatusSeeOther)
		return
	}

	// Get form values
	title := strings.TrimSpace(r.FormValue("title"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	body := r.FormValue("body")
	status := r.FormValue("status")

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"title":  title,
		"slug":   slug,
		"body":   body,
		"status": status,
	}

	// Validate
	errors := make(map[string]string)

	// Title validation
	if title == "" {
		errors["title"] = "Title is required"
	} else if len(title) < 2 {
		errors["title"] = "Title must be at least 2 characters"
	}

	// Slug validation - auto-generate if empty
	if slug == "" {
		slug = util.Slugify(title)
		formValues["slug"] = slug
	}

	if slug == "" {
		errors["slug"] = "Slug is required"
	} else if !util.IsValidSlug(slug) {
		errors["slug"] = "Invalid slug format (use lowercase letters, numbers, and hyphens)"
	} else {
		// Check if slug already exists
		exists, err := h.queries.SlugExists(r.Context(), slug)
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			errors["slug"] = "Error checking slug"
		} else if exists != 0 {
			errors["slug"] = "Slug already exists"
		}
	}

	// Status validation
	if status == "" {
		status = PageStatusDraft
		formValues["status"] = status
	} else if !isValidPageStatus(status) {
		errors["status"] = "Invalid status"
	}

	// If there are validation errors, re-render the form
	if len(errors) > 0 {
		data := PageFormData{
			Statuses:   ValidPageStatuses,
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     false,
		}

		if err := h.renderer.Render(w, r, "admin/pages_form", render.TemplateData{
			Title: "New Page",
			User:  user,
			Data:  data,
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Create page
	now := time.Now()
	newPage, err := h.queries.CreatePage(r.Context(), store.CreatePageParams{
		Title:     title,
		Slug:      slug,
		Body:      body,
		Status:    status,
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create page", "error", err)
		h.renderer.SetFlash(r, "Error creating page", "error")
		http.Redirect(w, r, "/admin/pages/new", http.StatusSeeOther)
		return
	}

	// Create initial version
	_, err = h.queries.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
		PageID:    newPage.ID,
		Title:     title,
		Body:      body,
		ChangedBy: user.ID,
		CreatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create page version", "error", err)
		// Page was created but version failed - log but don't fail the request
	}

	slog.Info("page created", "page_id", newPage.ID, "slug", newPage.Slug, "created_by", user.ID)
	h.renderer.SetFlash(r, "Page created successfully", "success")
	http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
}

// isValidPageStatus checks if a status is valid.
func isValidPageStatus(status string) bool {
	for _, s := range ValidPageStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// EditForm handles GET /admin/pages/{id} - displays the edit page form.
func (h *PagesHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get page ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid page ID", "error")
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	// Get page from database
	page, err := h.queries.GetPageByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Page not found", "error")
		} else {
			slog.Error("failed to get page", "error", err, "page_id", id)
			h.renderer.SetFlash(r, "Error loading page", "error")
		}
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	data := PageFormData{
		Page:       &page,
		Statuses:   ValidPageStatuses,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     true,
	}

	if err := h.renderer.Render(w, r, "admin/pages_form", render.TemplateData{
		Title: "Edit Page",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Update handles PUT /admin/pages/{id} - updates an existing page.
func (h *PagesHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get page ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid page ID", "error")
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	// Get existing page
	existingPage, err := h.queries.GetPageByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Page not found", "error")
		} else {
			slog.Error("failed to get page", "error", err, "page_id", id)
			h.renderer.SetFlash(r, "Error loading page", "error")
		}
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
		return
	}

	// Get form values
	title := strings.TrimSpace(r.FormValue("title"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	body := r.FormValue("body")
	status := r.FormValue("status")

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"title":  title,
		"slug":   slug,
		"body":   body,
		"status": status,
	}

	// Validate
	errors := make(map[string]string)

	// Title validation
	if title == "" {
		errors["title"] = "Title is required"
	} else if len(title) < 2 {
		errors["title"] = "Title must be at least 2 characters"
	}

	// Slug validation
	if slug == "" {
		slug = util.Slugify(title)
		formValues["slug"] = slug
	}

	if slug == "" {
		errors["slug"] = "Slug is required"
	} else if !util.IsValidSlug(slug) {
		errors["slug"] = "Invalid slug format (use lowercase letters, numbers, and hyphens)"
	} else if slug != existingPage.Slug {
		// Only check for uniqueness if slug changed
		exists, err := h.queries.SlugExistsExcluding(r.Context(), store.SlugExistsExcludingParams{
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

	// Status validation
	if status == "" {
		status = existingPage.Status
		formValues["status"] = status
	} else if !isValidPageStatus(status) {
		errors["status"] = "Invalid status"
	}

	// If there are validation errors, re-render the form
	if len(errors) > 0 {
		data := PageFormData{
			Page:       &existingPage,
			Statuses:   ValidPageStatuses,
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     true,
		}

		if err := h.renderer.Render(w, r, "admin/pages_form", render.TemplateData{
			Title: "Edit Page",
			User:  user,
			Data:  data,
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Update page
	now := time.Now()
	updatedPage, err := h.queries.UpdatePage(r.Context(), store.UpdatePageParams{
		ID:        id,
		Title:     title,
		Slug:      slug,
		Body:      body,
		Status:    status,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to update page", "error", err, "page_id", id)
		h.renderer.SetFlash(r, "Error updating page", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
		return
	}

	// Create new version (only if title or body changed)
	if title != existingPage.Title || body != existingPage.Body {
		_, err = h.queries.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
			PageID:    id,
			Title:     title,
			Body:      body,
			ChangedBy: user.ID,
			CreatedAt: now,
		})
		if err != nil {
			slog.Error("failed to create page version", "error", err, "page_id", id)
			// Don't fail the request - page was updated
		}
	}

	slog.Info("page updated", "page_id", updatedPage.ID, "slug", updatedPage.Slug, "updated_by", user.ID)
	h.renderer.SetFlash(r, "Page updated successfully", "success")
	http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
}

// Delete handles DELETE /admin/pages/{id} - deletes a page.
func (h *PagesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Get page ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid page ID", http.StatusBadRequest)
		return
	}

	// Get page to verify it exists and for logging
	page, err := h.queries.GetPageByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Page not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get page", "error", err, "page_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Delete the page (versions are cascade deleted by FK constraint)
	err = h.queries.DeletePage(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete page", "error", err, "page_id", id)
		http.Error(w, "Error deleting page", http.StatusInternalServerError)
		return
	}

	user := middleware.GetUser(r)
	slog.Info("page deleted", "page_id", id, "slug", page.Slug, "deleted_by", user.ID)

	// For HTMX requests, return empty response (row removed)
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For regular requests, redirect
	h.renderer.SetFlash(r, "Page deleted successfully", "success")
	http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
}

// TogglePublish handles POST /admin/pages/{id}/publish - toggles publish status.
func (h *PagesHandler) TogglePublish(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get page ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid page ID", "error")
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	// Get existing page
	page, err := h.queries.GetPageByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Page not found", "error")
		} else {
			slog.Error("failed to get page", "error", err, "page_id", id)
			h.renderer.SetFlash(r, "Error loading page", "error")
		}
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	now := time.Now()
	var message string

	if page.Status == PageStatusPublished {
		// Unpublish
		_, err = h.queries.UnpublishPage(r.Context(), store.UnpublishPageParams{
			ID:        id,
			UpdatedAt: now,
		})
		message = "Page unpublished successfully"
		slog.Info("page unpublished", "page_id", id, "slug", page.Slug, "unpublished_by", user.ID)
	} else {
		// Publish
		_, err = h.queries.PublishPage(r.Context(), store.PublishPageParams{
			ID:          id,
			UpdatedAt:   now,
			PublishedAt: sql.NullTime{Time: now, Valid: true},
		})
		message = "Page published successfully"
		slog.Info("page published", "page_id", id, "slug", page.Slug, "published_by", user.ID)
	}

	if err != nil {
		slog.Error("failed to toggle publish status", "error", err, "page_id", id)
		h.renderer.SetFlash(r, "Error updating page status", "error")
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	h.renderer.SetFlash(r, message, "success")
	http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
}
