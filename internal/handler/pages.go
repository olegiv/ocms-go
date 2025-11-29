package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

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
