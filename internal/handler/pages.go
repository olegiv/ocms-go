package handler

import (
	"context"
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
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
	"ocms-go/internal/util"
	"ocms-go/internal/webhook"
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
	dispatcher     *webhook.Dispatcher
}

// NewPagesHandler creates a new PagesHandler.
func NewPagesHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *PagesHandler {
	return &PagesHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// SetDispatcher sets the webhook dispatcher for event dispatching.
func (h *PagesHandler) SetDispatcher(d *webhook.Dispatcher) {
	h.dispatcher = d
}

// dispatchPageEvent dispatches a page-related webhook event.
func (h *PagesHandler) dispatchPageEvent(ctx context.Context, eventType string, page store.Page, authorEmail string) {
	if h.dispatcher == nil {
		return
	}

	var languageID *int64
	if page.LanguageID.Valid {
		languageID = &page.LanguageID.Int64
	}

	var publishedAt *string
	if page.PublishedAt.Valid {
		t := page.PublishedAt.Time.Format(time.RFC3339)
		publishedAt = &t
	}

	data := webhook.PageEventData{
		ID:          page.ID,
		Title:       page.Title,
		Slug:        page.Slug,
		Status:      page.Status,
		AuthorID:    page.AuthorID,
		AuthorEmail: authorEmail,
		LanguageID:  languageID,
		PublishedAt: publishedAt,
	}

	if err := h.dispatcher.DispatchEvent(ctx, eventType, data); err != nil {
		slog.Error("failed to dispatch webhook event",
			"error", err,
			"event_type", eventType,
			"page_id", page.ID)
	}
}

// PagesListData holds data for the pages list template.
type PagesListData struct {
	Pages              []store.Page
	PageTags           map[int64][]store.Tag        // Map of page ID to tags
	PageCategories     map[int64][]store.Category   // Map of page ID to categories
	PageFeaturedImages map[int64]*FeaturedImageData // Map of page ID to featured image
	PageLanguages      map[int64]*store.Language    // Map of page ID to language
	CurrentPage        int
	TotalPages         int
	TotalCount         int64
	HasPrev            bool
	HasNext            bool
	PrevPage           int
	NextPage           int
	StatusFilter       string
	CategoryFilter     int64
	LanguageFilter     int64              // Language filter
	SearchFilter       string             // Search query filter
	AllCategories      []PageCategoryNode // For category filter dropdown
	AllLanguages       []store.Language   // All active languages for filter dropdown
	Statuses           []string
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
	// Allow "scheduled" as a special filter in addition to regular statuses
	if statusFilter != "" && statusFilter != "all" && statusFilter != "scheduled" && !isValidPageStatus(statusFilter) {
		statusFilter = ""
	}

	// Get category filter from query string
	var categoryFilter int64
	categoryFilterStr := r.URL.Query().Get("category")
	if categoryFilterStr != "" {
		if cid, err := strconv.ParseInt(categoryFilterStr, 10, 64); err == nil && cid > 0 {
			categoryFilter = cid
		}
	}

	// Get search filter from query string
	searchFilter := strings.TrimSpace(r.URL.Query().Get("search"))

	// Get language filter from query string
	var languageFilter int64
	languageFilterStr := r.URL.Query().Get("language")
	if languageFilterStr != "" {
		if lid, err := strconv.ParseInt(languageFilterStr, 10, 64); err == nil && lid > 0 {
			languageFilter = lid
		}
	}

	var totalCount int64
	var pages []store.Page
	var err error

	// Create search pattern for LIKE queries
	searchPattern := "%" + searchFilter + "%"

	// Get total count based on filters
	// Priority: search > language > category > status > all
	if searchFilter != "" {
		if languageFilter > 0 {
			totalCount, err = h.queries.CountSearchPagesByLanguage(r.Context(), store.CountSearchPagesByLanguageParams{
				LanguageID: sql.NullInt64{Int64: languageFilter, Valid: true},
				Title:      searchPattern,
				Body:       searchPattern,
				Slug:       searchPattern,
			})
		} else if statusFilter != "" && statusFilter != "all" && statusFilter != "scheduled" {
			totalCount, err = h.queries.CountSearchPagesByStatus(r.Context(), store.CountSearchPagesByStatusParams{
				Status: statusFilter,
				Title:  searchPattern,
				Body:   searchPattern,
				Slug:   searchPattern,
			})
		} else {
			totalCount, err = h.queries.CountSearchPages(r.Context(), store.CountSearchPagesParams{
				Title: searchPattern,
				Body:  searchPattern,
				Slug:  searchPattern,
			})
		}
	} else if languageFilter > 0 {
		if statusFilter != "" && statusFilter != "all" && statusFilter != "scheduled" {
			totalCount, err = h.queries.CountPagesByLanguageAndStatus(r.Context(), store.CountPagesByLanguageAndStatusParams{
				LanguageID: sql.NullInt64{Int64: languageFilter, Valid: true},
				Status:     statusFilter,
			})
		} else {
			totalCount, err = h.queries.CountPagesByLanguage(r.Context(), sql.NullInt64{Int64: languageFilter, Valid: true})
		}
	} else if categoryFilter > 0 {
		totalCount, err = h.queries.CountPagesByCategory(r.Context(), categoryFilter)
	} else if statusFilter == "scheduled" {
		totalCount, err = h.queries.CountScheduledPages(r.Context())
	} else if statusFilter != "" && statusFilter != "all" {
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
	// Priority: search > language > category > status > all
	if searchFilter != "" {
		if languageFilter > 0 {
			pages, err = h.queries.SearchPagesByLanguage(r.Context(), store.SearchPagesByLanguageParams{
				LanguageID: sql.NullInt64{Int64: languageFilter, Valid: true},
				Title:      searchPattern,
				Body:       searchPattern,
				Slug:       searchPattern,
				Limit:      PagesPerPage,
				Offset:     offset,
			})
		} else if statusFilter != "" && statusFilter != "all" && statusFilter != "scheduled" {
			pages, err = h.queries.SearchPagesByStatus(r.Context(), store.SearchPagesByStatusParams{
				Status: statusFilter,
				Title:  searchPattern,
				Body:   searchPattern,
				Slug:   searchPattern,
				Limit:  PagesPerPage,
				Offset: offset,
			})
		} else {
			pages, err = h.queries.SearchPages(r.Context(), store.SearchPagesParams{
				Title:  searchPattern,
				Body:   searchPattern,
				Slug:   searchPattern,
				Limit:  PagesPerPage,
				Offset: offset,
			})
		}
	} else if languageFilter > 0 {
		if statusFilter != "" && statusFilter != "all" && statusFilter != "scheduled" {
			pages, err = h.queries.ListPagesByLanguageAndStatus(r.Context(), store.ListPagesByLanguageAndStatusParams{
				LanguageID: sql.NullInt64{Int64: languageFilter, Valid: true},
				Status:     statusFilter,
				Limit:      PagesPerPage,
				Offset:     offset,
			})
		} else {
			pages, err = h.queries.ListPagesByLanguage(r.Context(), store.ListPagesByLanguageParams{
				LanguageID: sql.NullInt64{Int64: languageFilter, Valid: true},
				Limit:      PagesPerPage,
				Offset:     offset,
			})
		}
	} else if categoryFilter > 0 {
		pages, err = h.queries.ListPagesByCategory(r.Context(), store.ListPagesByCategoryParams{
			CategoryID: categoryFilter,
			Limit:      PagesPerPage,
			Offset:     offset,
		})
	} else if statusFilter == "scheduled" {
		pages, err = h.queries.ListScheduledPages(r.Context(), store.ListScheduledPagesParams{
			Limit:  PagesPerPage,
			Offset: offset,
		})
	} else if statusFilter != "" && statusFilter != "all" {
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

	// Fetch tags for all displayed pages
	pageTags := make(map[int64][]store.Tag)
	for _, p := range pages {
		tags, err := h.queries.GetTagsForPage(r.Context(), p.ID)
		if err != nil {
			slog.Error("failed to get tags for page", "error", err, "page_id", p.ID)
			continue
		}
		pageTags[p.ID] = tags
	}

	// Fetch categories for all displayed pages
	pageCategories := make(map[int64][]store.Category)
	for _, p := range pages {
		categories, err := h.queries.GetCategoriesForPage(r.Context(), p.ID)
		if err != nil {
			slog.Error("failed to get categories for page", "error", err, "page_id", p.ID)
			continue
		}
		pageCategories[p.ID] = categories
	}

	// Fetch featured images for all displayed pages
	pageFeaturedImages := make(map[int64]*FeaturedImageData)
	for _, p := range pages {
		if p.FeaturedImageID.Valid {
			media, err := h.queries.GetMediaByID(r.Context(), p.FeaturedImageID.Int64)
			if err != nil {
				slog.Error("failed to get featured image for page", "error", err, "page_id", p.ID, "media_id", p.FeaturedImageID.Int64)
				continue
			}
			pageFeaturedImages[p.ID] = &FeaturedImageData{
				ID:       media.ID,
				Filename: media.Filename,
				Filepath: fmt.Sprintf("/uploads/originals/%s/%s", media.Uuid, media.Filename),
				Mimetype: media.MimeType,
			}
		}
	}

	// Fetch languages for all displayed pages
	pageLanguages := make(map[int64]*store.Language)
	for _, p := range pages {
		if p.LanguageID.Valid {
			lang, err := h.queries.GetLanguageByID(r.Context(), p.LanguageID.Int64)
			if err != nil {
				if err != sql.ErrNoRows {
					slog.Error("failed to get language for page", "error", err, "page_id", p.ID, "language_id", p.LanguageID.Int64)
				}
				continue
			}
			pageLanguages[p.ID] = &lang
		}
	}

	// Load all categories for filter dropdown
	allCategories, err := h.queries.ListCategories(r.Context())
	if err != nil {
		slog.Error("failed to list categories for filter", "error", err)
		allCategories = []store.Category{}
	}
	categoryTree := buildPageCategoryTree(allCategories, nil, 0)

	// Load all active languages for filter dropdown
	allLanguages, err := h.queries.ListActiveLanguages(r.Context())
	if err != nil {
		slog.Error("failed to list languages for filter", "error", err)
		allLanguages = []store.Language{}
	}

	data := PagesListData{
		Pages:              pages,
		PageTags:           pageTags,
		PageCategories:     pageCategories,
		PageFeaturedImages: pageFeaturedImages,
		PageLanguages:      pageLanguages,
		CurrentPage:        page,
		TotalPages:         totalPages,
		TotalCount:         totalCount,
		HasPrev:            page > 1,
		HasNext:            page < totalPages,
		PrevPage:           page - 1,
		NextPage:           page + 1,
		StatusFilter:       statusFilter,
		CategoryFilter:     categoryFilter,
		LanguageFilter:     languageFilter,
		SearchFilter:       searchFilter,
		AllCategories:      categoryTree,
		AllLanguages:       allLanguages,
		Statuses:           ValidPageStatuses,
	}

	if err := h.renderer.Render(w, r, "admin/pages_list", render.TemplateData{
		Title: "Pages",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Pages", URL: "/admin/pages", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// PageCategoryNode represents a category with depth for tree display.
type PageCategoryNode struct {
	Category store.Category
	Depth    int
}

// FeaturedImageData holds featured image data for the template.
type FeaturedImageData struct {
	ID       int64
	Filename string
	Filepath string
	Mimetype string
}

// PageFormData holds data for the page form template.
type PageFormData struct {
	Page          *store.Page
	Tags          []store.Tag
	Categories    []store.Category   // Selected categories for the page
	AllCategories []PageCategoryNode // All categories for selection (with tree structure)
	FeaturedImage *FeaturedImageData
	Statuses      []string
	Errors        map[string]string
	FormValues    map[string]string
	IsEdit        bool
	// Language and translation support
	Language         *store.Language       // Current page language
	AllLanguages     []store.Language      // All active languages for selection
	Translations     []PageTranslationInfo // Existing translations
	MissingLanguages []store.Language      // Languages without translations
}

// PageTranslationInfo holds information about a page translation.
type PageTranslationInfo struct {
	Language store.Language
	Page     store.Page
}

// buildPageCategoryTree builds a flat list with depth for display.
func buildPageCategoryTree(categories []store.Category, parentID *int64, depth int) []PageCategoryNode {
	var nodes []PageCategoryNode

	for _, cat := range categories {
		var catParentID *int64
		if cat.ParentID.Valid {
			catParentID = &cat.ParentID.Int64
		}

		parentMatch := (parentID == nil && catParentID == nil) ||
			(parentID != nil && catParentID != nil && *parentID == *catParentID)

		if parentMatch {
			nodes = append(nodes, PageCategoryNode{
				Category: cat,
				Depth:    depth,
			})
			// Recursively add children
			children := buildPageCategoryTree(categories, &cat.ID, depth+1)
			nodes = append(nodes, children...)
		}
	}

	return nodes
}

// NewForm handles GET /admin/pages/new - displays the new page form.
func (h *PagesHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Load all categories for the selector
	allCategories, err := h.queries.ListCategories(r.Context())
	if err != nil {
		slog.Error("failed to list categories", "error", err)
		allCategories = []store.Category{}
	}
	categoryTree := buildPageCategoryTree(allCategories, nil, 0)

	// Load all active languages for selection
	allLanguages, err := h.queries.ListActiveLanguages(r.Context())
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		allLanguages = []store.Language{}
	}

	// Get default language
	var defaultLanguage *store.Language
	defaultLang, err := h.queries.GetDefaultLanguage(r.Context())
	if err == nil {
		defaultLanguage = &defaultLang
	}

	data := PageFormData{
		AllCategories: categoryTree,
		AllLanguages:  allLanguages,
		Language:      defaultLanguage,
		Statuses:      ValidPageStatuses,
		Errors:        make(map[string]string),
		FormValues:    make(map[string]string),
		IsEdit:        false,
	}

	if err := h.renderer.Render(w, r, "admin/pages_form", render.TemplateData{
		Title: "New Page",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Pages", URL: "/admin/pages"},
			{Label: "New Page", URL: "/admin/pages/new", Active: true},
		},
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
	featuredImageIDStr := r.FormValue("featured_image_id")

	// Get SEO form values
	metaTitle := strings.TrimSpace(r.FormValue("meta_title"))
	metaDescription := strings.TrimSpace(r.FormValue("meta_description"))
	metaKeywords := strings.TrimSpace(r.FormValue("meta_keywords"))
	ogImageIDStr := r.FormValue("og_image_id")
	noIndexStr := r.FormValue("no_index")
	noFollowStr := r.FormValue("no_follow")
	canonicalURL := strings.TrimSpace(r.FormValue("canonical_url"))

	// Parse featured image ID
	var featuredImageID sql.NullInt64
	if featuredImageIDStr != "" {
		if imgID, err := strconv.ParseInt(featuredImageIDStr, 10, 64); err == nil && imgID > 0 {
			featuredImageID = sql.NullInt64{Int64: imgID, Valid: true}
		}
	}

	// Parse OG image ID
	var ogImageID sql.NullInt64
	if ogImageIDStr != "" {
		if imgID, err := strconv.ParseInt(ogImageIDStr, 10, 64); err == nil && imgID > 0 {
			ogImageID = sql.NullInt64{Int64: imgID, Valid: true}
		}
	}

	// Parse boolean SEO fields (checkboxes)
	var noIndex int64
	if noIndexStr == "1" || noIndexStr == "on" {
		noIndex = 1
	}
	var noFollow int64
	if noFollowStr == "1" || noFollowStr == "on" {
		noFollow = 1
	}

	// Parse scheduled_at for scheduled publishing
	scheduledAtStr := strings.TrimSpace(r.FormValue("scheduled_at"))
	var scheduledAt sql.NullTime
	if scheduledAtStr != "" {
		if t, err := time.Parse("2006-01-02T15:04", scheduledAtStr); err == nil {
			scheduledAt = sql.NullTime{Time: t, Valid: true}
		}
	}

	// Parse language_id
	languageIDStr := r.FormValue("language_id")
	var languageID sql.NullInt64
	if languageIDStr != "" {
		if lid, err := strconv.ParseInt(languageIDStr, 10, 64); err == nil && lid > 0 {
			languageID = sql.NullInt64{Int64: lid, Valid: true}
		}
	} else {
		// Default to the default language if not specified
		defaultLang, err := h.queries.GetDefaultLanguage(r.Context())
		if err == nil {
			languageID = sql.NullInt64{Int64: defaultLang.ID, Valid: true}
		}
	}

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"title":             title,
		"slug":              slug,
		"body":              body,
		"status":            status,
		"featured_image_id": featuredImageIDStr,
		"meta_title":        metaTitle,
		"meta_description":  metaDescription,
		"meta_keywords":     metaKeywords,
		"og_image_id":       ogImageIDStr,
		"no_index":          noIndexStr,
		"no_follow":         noFollowStr,
		"canonical_url":     canonicalURL,
		"scheduled_at":      scheduledAtStr,
		"language_id":       languageIDStr,
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
		// Load languages for re-rendering
		allLanguages, _ := h.queries.ListActiveLanguages(r.Context())

		data := PageFormData{
			AllLanguages: allLanguages,
			Statuses:     ValidPageStatuses,
			Errors:       errors,
			FormValues:   formValues,
			IsEdit:       false,
		}

		if err := h.renderer.Render(w, r, "admin/pages_form", render.TemplateData{
			Title: "New Page",
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: "Dashboard", URL: "/admin"},
				{Label: "Pages", URL: "/admin/pages"},
				{Label: "New Page", URL: "/admin/pages/new", Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Create page
	now := time.Now()
	newPage, err := h.queries.CreatePage(r.Context(), store.CreatePageParams{
		Title:           title,
		Slug:            slug,
		Body:            body,
		Status:          status,
		AuthorID:        user.ID,
		FeaturedImageID: featuredImageID,
		MetaTitle:       metaTitle,
		MetaDescription: metaDescription,
		MetaKeywords:    metaKeywords,
		OgImageID:       ogImageID,
		NoIndex:         noIndex,
		NoFollow:        noFollow,
		CanonicalUrl:    canonicalURL,
		ScheduledAt:     scheduledAt,
		LanguageID:      languageID,
		CreatedAt:       now,
		UpdatedAt:       now,
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

	// Save tags
	tagIDs := r.Form["tags[]"]
	for _, tagIDStr := range tagIDs {
		tagID, err := strconv.ParseInt(tagIDStr, 10, 64)
		if err != nil {
			continue
		}
		err = h.queries.AddTagToPage(r.Context(), store.AddTagToPageParams{
			PageID: newPage.ID,
			TagID:  tagID,
		})
		if err != nil {
			slog.Error("failed to add tag to page", "error", err, "page_id", newPage.ID, "tag_id", tagID)
		}
	}

	// Save categories
	categoryIDs := r.Form["categories[]"]
	for _, categoryIDStr := range categoryIDs {
		categoryID, err := strconv.ParseInt(categoryIDStr, 10, 64)
		if err != nil {
			continue
		}
		err = h.queries.AddCategoryToPage(r.Context(), store.AddCategoryToPageParams{
			PageID:     newPage.ID,
			CategoryID: categoryID,
		})
		if err != nil {
			slog.Error("failed to add category to page", "error", err, "page_id", newPage.ID, "category_id", categoryID)
		}
	}

	slog.Info("page created", "page_id", newPage.ID, "slug", newPage.Slug, "created_by", user.ID)

	// Dispatch page.created webhook event
	h.dispatchPageEvent(r.Context(), model.EventPageCreated, newPage, user.Email)

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

	// Get tags for this page
	tags, err := h.queries.GetTagsForPage(r.Context(), id)
	if err != nil {
		slog.Error("failed to get tags for page", "error", err, "page_id", id)
		tags = []store.Tag{} // Continue with empty tags on error
	}

	// Get categories for this page
	categories, err := h.queries.GetCategoriesForPage(r.Context(), id)
	if err != nil {
		slog.Error("failed to get categories for page", "error", err, "page_id", id)
		categories = []store.Category{}
	}

	// Load all categories for the selector
	allCategories, err := h.queries.ListCategories(r.Context())
	if err != nil {
		slog.Error("failed to list categories", "error", err)
		allCategories = []store.Category{}
	}
	categoryTree := buildPageCategoryTree(allCategories, nil, 0)

	// Load featured image if set
	var featuredImage *FeaturedImageData
	if page.FeaturedImageID.Valid {
		media, err := h.queries.GetMediaByID(r.Context(), page.FeaturedImageID.Int64)
		if err != nil {
			if err != sql.ErrNoRows {
				slog.Error("failed to get featured image", "error", err, "media_id", page.FeaturedImageID.Int64)
			}
		} else {
			featuredImage = &FeaturedImageData{
				ID:       media.ID,
				Filename: media.Filename,
				Filepath: fmt.Sprintf("/uploads/originals/%s/%s", media.Uuid, media.Filename),
				Mimetype: media.MimeType,
			}
		}
	}

	// Load all active languages
	allLanguages, err := h.queries.ListActiveLanguages(r.Context())
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		allLanguages = []store.Language{}
	}

	// Load current page's language
	var pageLanguage *store.Language
	if page.LanguageID.Valid {
		lang, err := h.queries.GetLanguageByID(r.Context(), page.LanguageID.Int64)
		if err == nil {
			pageLanguage = &lang
		}
	}

	// Load translations for this page
	var translations []PageTranslationInfo
	var missingLanguages []store.Language

	translationLinks, err := h.queries.GetTranslationsForEntity(r.Context(), store.GetTranslationsForEntityParams{
		EntityType: model.EntityTypePage,
		EntityID:   id,
	})
	if err != nil && err != sql.ErrNoRows {
		slog.Error("failed to get translations for page", "error", err, "page_id", id)
	}

	// Build translations list and find missing languages
	translatedLangIDs := make(map[int64]bool)
	if pageLanguage != nil {
		translatedLangIDs[pageLanguage.ID] = true // Current page's language is "taken"
	}

	for _, tl := range translationLinks {
		translatedLangIDs[tl.LanguageID] = true
		// Get the translated page
		translatedPage, err := h.queries.GetPageByID(r.Context(), tl.TranslationID)
		if err == nil {
			lang, err := h.queries.GetLanguageByID(r.Context(), tl.LanguageID)
			if err == nil {
				translations = append(translations, PageTranslationInfo{
					Language: lang,
					Page:     translatedPage,
				})
			}
		}
	}

	// Find languages that don't have translations yet
	for _, lang := range allLanguages {
		if !translatedLangIDs[lang.ID] {
			missingLanguages = append(missingLanguages, lang)
		}
	}

	data := PageFormData{
		Page:             &page,
		Tags:             tags,
		Categories:       categories,
		AllCategories:    categoryTree,
		FeaturedImage:    featuredImage,
		AllLanguages:     allLanguages,
		Language:         pageLanguage,
		Translations:     translations,
		MissingLanguages: missingLanguages,
		Statuses:         ValidPageStatuses,
		Errors:           make(map[string]string),
		FormValues:       make(map[string]string),
		IsEdit:           true,
	}

	if err := h.renderer.Render(w, r, "admin/pages_form", render.TemplateData{
		Title: "Edit Page",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Pages", URL: "/admin/pages"},
			{Label: page.Title, URL: fmt.Sprintf("/admin/pages/%d", page.ID), Active: true},
		},
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
	featuredImageIDStr := r.FormValue("featured_image_id")

	// Get SEO form values
	metaTitle := strings.TrimSpace(r.FormValue("meta_title"))
	metaDescription := strings.TrimSpace(r.FormValue("meta_description"))
	metaKeywords := strings.TrimSpace(r.FormValue("meta_keywords"))
	ogImageIDStr := r.FormValue("og_image_id")
	noIndexStr := r.FormValue("no_index")
	noFollowStr := r.FormValue("no_follow")
	canonicalURL := strings.TrimSpace(r.FormValue("canonical_url"))

	// Parse featured image ID
	var featuredImageID sql.NullInt64
	if featuredImageIDStr != "" {
		if imgID, err := strconv.ParseInt(featuredImageIDStr, 10, 64); err == nil && imgID > 0 {
			featuredImageID = sql.NullInt64{Int64: imgID, Valid: true}
		}
	}

	// Parse OG image ID
	var ogImageID sql.NullInt64
	if ogImageIDStr != "" {
		if imgID, err := strconv.ParseInt(ogImageIDStr, 10, 64); err == nil && imgID > 0 {
			ogImageID = sql.NullInt64{Int64: imgID, Valid: true}
		}
	}

	// Parse boolean SEO fields (checkboxes)
	var noIndex int64
	if noIndexStr == "1" || noIndexStr == "on" {
		noIndex = 1
	}
	var noFollow int64
	if noFollowStr == "1" || noFollowStr == "on" {
		noFollow = 1
	}

	// Parse scheduled_at for scheduled publishing
	scheduledAtStr := strings.TrimSpace(r.FormValue("scheduled_at"))
	var scheduledAt sql.NullTime
	if scheduledAtStr != "" {
		if t, err := time.Parse("2006-01-02T15:04", scheduledAtStr); err == nil {
			scheduledAt = sql.NullTime{Time: t, Valid: true}
		}
	}

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"title":             title,
		"slug":              slug,
		"body":              body,
		"status":            status,
		"featured_image_id": featuredImageIDStr,
		"meta_title":        metaTitle,
		"meta_description":  metaDescription,
		"meta_keywords":     metaKeywords,
		"og_image_id":       ogImageIDStr,
		"no_index":          noIndexStr,
		"no_follow":         noFollowStr,
		"canonical_url":     canonicalURL,
		"scheduled_at":      scheduledAtStr,
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
			Breadcrumbs: []render.Breadcrumb{
				{Label: "Dashboard", URL: "/admin"},
				{Label: "Pages", URL: "/admin/pages"},
				{Label: existingPage.Title, URL: fmt.Sprintf("/admin/pages/%d", id), Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Update page
	now := time.Now()
	updatedPage, err := h.queries.UpdatePage(r.Context(), store.UpdatePageParams{
		ID:              id,
		Title:           title,
		Slug:            slug,
		Body:            body,
		Status:          status,
		FeaturedImageID: featuredImageID,
		MetaTitle:       metaTitle,
		MetaDescription: metaDescription,
		MetaKeywords:    metaKeywords,
		OgImageID:       ogImageID,
		NoIndex:         noIndex,
		NoFollow:        noFollow,
		CanonicalUrl:    canonicalURL,
		ScheduledAt:     scheduledAt,
		LanguageID:      existingPage.LanguageID,
		UpdatedAt:       now,
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

	// Update tags - clear existing and add new
	err = h.queries.ClearPageTags(r.Context(), id)
	if err != nil {
		slog.Error("failed to clear page tags", "error", err, "page_id", id)
	}

	tagIDs := r.Form["tags[]"]
	for _, tagIDStr := range tagIDs {
		tagID, err := strconv.ParseInt(tagIDStr, 10, 64)
		if err != nil {
			continue
		}
		err = h.queries.AddTagToPage(r.Context(), store.AddTagToPageParams{
			PageID: id,
			TagID:  tagID,
		})
		if err != nil {
			slog.Error("failed to add tag to page", "error", err, "page_id", id, "tag_id", tagID)
		}
	}

	// Update categories - clear existing and add new
	err = h.queries.ClearPageCategories(r.Context(), id)
	if err != nil {
		slog.Error("failed to clear page categories", "error", err, "page_id", id)
	}

	categoryIDs := r.Form["categories[]"]
	for _, categoryIDStr := range categoryIDs {
		categoryID, err := strconv.ParseInt(categoryIDStr, 10, 64)
		if err != nil {
			continue
		}
		err = h.queries.AddCategoryToPage(r.Context(), store.AddCategoryToPageParams{
			PageID:     id,
			CategoryID: categoryID,
		})
		if err != nil {
			slog.Error("failed to add category to page", "error", err, "page_id", id, "category_id", categoryID)
		}
	}

	slog.Info("page updated", "page_id", updatedPage.ID, "slug", updatedPage.Slug, "updated_by", user.ID)

	// Dispatch page.updated webhook event
	h.dispatchPageEvent(r.Context(), model.EventPageUpdated, updatedPage, user.Email)

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

	// Dispatch page.deleted webhook event
	h.dispatchPageEvent(r.Context(), model.EventPageDeleted, page, user.Email)

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
	var eventType string

	if page.Status == PageStatusPublished {
		// Unpublish
		_, err = h.queries.UnpublishPage(r.Context(), store.UnpublishPageParams{
			ID:        id,
			UpdatedAt: now,
		})
		message = "Page unpublished successfully"
		eventType = model.EventPageUnpublished
		slog.Info("page unpublished", "page_id", id, "slug", page.Slug, "unpublished_by", user.ID)
	} else {
		// Publish
		_, err = h.queries.PublishPage(r.Context(), store.PublishPageParams{
			ID:          id,
			UpdatedAt:   now,
			PublishedAt: sql.NullTime{Time: now, Valid: true},
		})
		message = "Page published successfully"
		eventType = model.EventPagePublished
		slog.Info("page published", "page_id", id, "slug", page.Slug, "published_by", user.ID)
	}

	if err != nil {
		slog.Error("failed to toggle publish status", "error", err, "page_id", id)
		h.renderer.SetFlash(r, "Error updating page status", "error")
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	// Get updated page for webhook event
	updatedPage, err := h.queries.GetPageByID(r.Context(), id)
	if err == nil {
		h.dispatchPageEvent(r.Context(), eventType, updatedPage, user.Email)
	}

	h.renderer.SetFlash(r, message, "success")
	http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
}

// VersionsPerPage is the number of versions to display per page.
const VersionsPerPage = 20

// PageVersionsData holds data for the page versions template.
type PageVersionsData struct {
	Page        store.Page
	Versions    []store.ListPageVersionsWithUserRow
	CurrentPage int
	TotalPages  int
	TotalCount  int64
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
}

// Versions handles GET /admin/pages/{id}/versions - displays version history.
func (h *PagesHandler) Versions(w http.ResponseWriter, r *http.Request) {
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

	// Get page number from query string
	pageStr := r.URL.Query().Get("page")
	pageNum := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			pageNum = p
		}
	}

	// Get total count
	totalCount, err := h.queries.CountPageVersions(r.Context(), id)
	if err != nil {
		slog.Error("failed to count page versions", "error", err, "page_id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Calculate pagination
	totalPages := int((totalCount + VersionsPerPage - 1) / VersionsPerPage)
	if totalPages < 1 {
		totalPages = 1
	}
	if pageNum > totalPages {
		pageNum = totalPages
	}

	offset := int64((pageNum - 1) * VersionsPerPage)

	// Fetch versions for current page
	versions, err := h.queries.ListPageVersionsWithUser(r.Context(), store.ListPageVersionsWithUserParams{
		PageID: id,
		Limit:  VersionsPerPage,
		Offset: offset,
	})
	if err != nil {
		slog.Error("failed to list page versions", "error", err, "page_id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := PageVersionsData{
		Page:        page,
		Versions:    versions,
		CurrentPage: pageNum,
		TotalPages:  totalPages,
		TotalCount:  totalCount,
		HasPrev:     pageNum > 1,
		HasNext:     pageNum < totalPages,
		PrevPage:    pageNum - 1,
		NextPage:    pageNum + 1,
	}

	if err := h.renderer.Render(w, r, "admin/pages_versions", render.TemplateData{
		Title: fmt.Sprintf("Version History - %s", page.Title),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Pages", URL: "/admin/pages"},
			{Label: page.Title, URL: fmt.Sprintf("/admin/pages/%d", page.ID)},
			{Label: "Versions", URL: fmt.Sprintf("/admin/pages/%d/versions", page.ID), Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// RestoreVersion handles POST /admin/pages/{id}/versions/{versionId}/restore - restores a version.
func (h *PagesHandler) RestoreVersion(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get page ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid page ID", "error")
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	// Get version ID from URL
	versionIdStr := chi.URLParam(r, "versionId")
	versionId, err := strconv.ParseInt(versionIdStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid version ID", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d/versions", id), http.StatusSeeOther)
		return
	}

	// Get page to verify it exists
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

	// Get version to restore
	version, err := h.queries.GetPageVersionWithUser(r.Context(), versionId)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Version not found", "error")
		} else {
			slog.Error("failed to get page version", "error", err, "version_id", versionId)
			h.renderer.SetFlash(r, "Error loading version", "error")
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d/versions", id), http.StatusSeeOther)
		return
	}

	// Verify version belongs to this page
	if version.PageID != id {
		h.renderer.SetFlash(r, "Version does not belong to this page", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d/versions", id), http.StatusSeeOther)
		return
	}

	// Update page with version content (keeping SEO fields and scheduling intact)
	now := time.Now()
	_, err = h.queries.UpdatePage(r.Context(), store.UpdatePageParams{
		ID:              id,
		Title:           version.Title,
		Slug:            page.Slug, // Keep the current slug
		Body:            version.Body,
		Status:          page.Status, // Keep the current status
		FeaturedImageID: page.FeaturedImageID,
		MetaTitle:       page.MetaTitle,
		MetaDescription: page.MetaDescription,
		MetaKeywords:    page.MetaKeywords,
		OgImageID:       page.OgImageID,
		NoIndex:         page.NoIndex,
		NoFollow:        page.NoFollow,
		CanonicalUrl:    page.CanonicalUrl,
		ScheduledAt:     page.ScheduledAt, // Keep scheduling intact
		LanguageID:      page.LanguageID,  // Keep language intact
		UpdatedAt:       now,
	})
	if err != nil {
		slog.Error("failed to restore page version", "error", err, "page_id", id, "version_id", versionId)
		h.renderer.SetFlash(r, "Error restoring version", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d/versions", id), http.StatusSeeOther)
		return
	}

	// Create new version to record the restore
	_, err = h.queries.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
		PageID:    id,
		Title:     version.Title,
		Body:      version.Body,
		ChangedBy: user.ID,
		CreatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create page version after restore", "error", err, "page_id", id)
		// Don't fail the request - page was restored
	}

	slog.Info("page version restored", "page_id", id, "version_id", versionId, "restored_by", user.ID)
	h.renderer.SetFlash(r, "Version restored successfully", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
}

// Translate handles POST /admin/pages/{id}/translate/{langCode} - creates a translation.
func (h *PagesHandler) Translate(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Get page ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid page ID", "error")
		http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
		return
	}

	// Get language code from URL
	langCode := chi.URLParam(r, "langCode")
	if langCode == "" {
		h.renderer.SetFlash(r, "Language code is required", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
		return
	}

	// Get source page
	sourcePage, err := h.queries.GetPageByID(r.Context(), id)
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

	// Get target language
	targetLang, err := h.queries.GetLanguageByCode(r.Context(), langCode)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Language not found", "error")
		} else {
			slog.Error("failed to get language", "error", err, "lang_code", langCode)
			h.renderer.SetFlash(r, "Error loading language", "error")
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
		return
	}

	// Check if translation already exists
	existingTranslation, err := h.queries.GetTranslation(r.Context(), store.GetTranslationParams{
		EntityType: model.EntityTypePage,
		EntityID:   id,
		LanguageID: targetLang.ID,
	})
	if err == nil && existingTranslation.ID > 0 {
		// Translation already exists, redirect to it
		h.renderer.SetFlash(r, "Translation already exists", "info")
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", existingTranslation.TranslationID), http.StatusSeeOther)
		return
	}

	// Generate a unique slug for the translated page
	baseSlug := sourcePage.Slug + "-" + langCode
	translatedSlug := baseSlug
	counter := 1
	for {
		exists, err := h.queries.SlugExists(r.Context(), translatedSlug)
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			h.renderer.SetFlash(r, "Error creating translation", "error")
			http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
			return
		}
		if exists == 0 {
			break
		}
		counter++
		translatedSlug = fmt.Sprintf("%s-%d", baseSlug, counter)
	}

	// Create the translated page with same title but empty body
	now := time.Now()
	translatedPage, err := h.queries.CreatePage(r.Context(), store.CreatePageParams{
		Title:           sourcePage.Title, // Keep same title (user will translate)
		Slug:            translatedSlug,
		Body:            "",              // Empty body for translation
		Status:          PageStatusDraft, // Always start as draft
		AuthorID:        user.ID,
		FeaturedImageID: sourcePage.FeaturedImageID,
		MetaTitle:       "",
		MetaDescription: "",
		MetaKeywords:    "",
		OgImageID:       sql.NullInt64{},
		NoIndex:         0,
		NoFollow:        0,
		CanonicalUrl:    "",
		ScheduledAt:     sql.NullTime{},
		LanguageID:      sql.NullInt64{Int64: targetLang.ID, Valid: true},
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		slog.Error("failed to create translated page", "error", err)
		h.renderer.SetFlash(r, "Error creating translation", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
		return
	}

	// Create initial version for the translated page
	_, err = h.queries.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
		PageID:    translatedPage.ID,
		Title:     translatedPage.Title,
		Body:      translatedPage.Body,
		ChangedBy: user.ID,
		CreatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create page version for translation", "error", err)
		// Don't fail - page was created
	}

	// Create translation link from source to translated page
	_, err = h.queries.CreateTranslation(r.Context(), store.CreateTranslationParams{
		EntityType:    model.EntityTypePage,
		EntityID:      id,
		LanguageID:    targetLang.ID,
		TranslationID: translatedPage.ID,
		CreatedAt:     now,
	})
	if err != nil {
		slog.Error("failed to create translation link", "error", err)
		// Page was created, so we should still redirect to it
	}

	slog.Info("page translation created",
		"source_page_id", id,
		"translated_page_id", translatedPage.ID,
		"language", langCode,
		"created_by", user.ID)

	h.renderer.SetFlash(r, fmt.Sprintf("Translation created for %s. Please translate the content.", targetLang.Name), "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", translatedPage.ID), http.StatusSeeOther)
}
