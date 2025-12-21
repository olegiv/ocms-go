package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/i18n"
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
	TotalCount         int64
	StatusFilter       string
	CategoryFilter     int64
	LanguageFilter     int64              // Language filter
	SearchFilter       string             // Search query filter
	AllCategories      []PageCategoryNode // For category filter dropdown
	AllLanguages       []store.Language   // All active languages for filter dropdown
	Statuses           []string
	Pagination         AdminPagination
}

// List handles GET /admin/pages - displays a paginated list of pages.
func (h *PagesHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	page := ParsePageParam(r)

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
				LanguageID: util.NullInt64FromValue(languageFilter),
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
				LanguageID: util.NullInt64FromValue(languageFilter),
				Status:     statusFilter,
			})
		} else {
			totalCount, err = h.queries.CountPagesByLanguage(r.Context(), util.NullInt64FromValue(languageFilter))
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

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalCount), PagesPerPage)
	offset := int64((page - 1) * PagesPerPage)

	// Fetch pages for current page
	// Priority: search > language > category > status > all
	if searchFilter != "" {
		if languageFilter > 0 {
			pages, err = h.queries.SearchPagesByLanguage(r.Context(), store.SearchPagesByLanguageParams{
				LanguageID: util.NullInt64FromValue(languageFilter),
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
				LanguageID: util.NullInt64FromValue(languageFilter),
				Status:     statusFilter,
				Limit:      PagesPerPage,
				Offset:     offset,
			})
		} else {
			pages, err = h.queries.ListPagesByLanguage(r.Context(), store.ListPagesByLanguageParams{
				LanguageID: util.NullInt64FromValue(languageFilter),
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
	pageTags := batchFetchRelated(r.Context(), pages,
		func(p store.Page) int64 { return p.ID },
		func(ctx context.Context, id int64) ([]store.Tag, error) { return h.queries.GetTagsForPage(ctx, id) },
		"page_tags",
	)

	// Fetch categories for all displayed pages
	pageCategories := batchFetchRelated(r.Context(), pages,
		func(p store.Page) int64 { return p.ID },
		func(ctx context.Context, id int64) ([]store.Category, error) {
			return h.queries.GetCategoriesForPage(ctx, id)
		},
		"page_categories",
	)

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
				ID:        media.ID,
				Filename:  media.Filename,
				Filepath:  fmt.Sprintf("/uploads/originals/%s/%s", media.Uuid, media.Filename),
				Thumbnail: fmt.Sprintf("/uploads/thumbnail/%s/%s", media.Uuid, media.Filename),
				Mimetype:  media.MimeType,
			}
		}
	}

	// Fetch languages for all displayed pages
	pageLanguages := batchFetchOptional(r.Context(), pages,
		func(p store.Page) int64 { return p.ID },
		func(p store.Page) sql.NullInt64 { return p.LanguageID },
		func(ctx context.Context, id int64) (store.Language, error) { return h.queries.GetLanguageByID(ctx, id) },
		"page_languages",
	)

	// Load all categories for filter dropdown
	allCategories, err := h.queries.ListCategories(r.Context())
	if err != nil {
		slog.Error("failed to list categories for filter", "error", err)
		allCategories = []store.Category{}
	}
	categoryTree := buildPageCategoryTree(allCategories, nil, 0)

	// Load all active languages for filter dropdown
	allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

	data := PagesListData{
		Pages:              pages,
		PageTags:           pageTags,
		PageCategories:     pageCategories,
		PageFeaturedImages: pageFeaturedImages,
		PageLanguages:      pageLanguages,
		TotalCount:         totalCount,
		StatusFilter:       statusFilter,
		CategoryFilter:     categoryFilter,
		LanguageFilter:     languageFilter,
		SearchFilter:       searchFilter,
		AllCategories:      categoryTree,
		AllLanguages:       allLanguages,
		Statuses:           ValidPageStatuses,
		Pagination:         BuildAdminPagination(page, int(totalCount), PagesPerPage, "/admin/pages", r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/pages_list", render.TemplateData{
		Title: i18n.T(lang, "pages.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "pages.title"), URL: "/admin/pages", Active: true},
		},
	})
}

// PageCategoryNode represents a category with depth for tree display.
type PageCategoryNode struct {
	Category store.Category
	Depth    int
}

// FeaturedImageData holds featured image data for the template.
type FeaturedImageData struct {
	ID        int64
	Filename  string
	Filepath  string
	Thumbnail string
	Mimetype  string
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

// pageLanguageInfo holds language and translation info for a page.
type pageLanguageInfo struct {
	PageLanguage     *store.Language
	AllLanguages     []store.Language
	Translations     []PageTranslationInfo
	MissingLanguages []store.Language
}

// loadPageLanguageInfo loads language and translation info for a page.
func (h *PagesHandler) loadPageLanguageInfo(ctx context.Context, page store.Page) pageLanguageInfo {
	base := loadTranslationBaseInfo(ctx, h.queries, model.EntityTypePage, page.ID, page.LanguageID)
	result := loadEntityTranslations(
		base,
		func(id int64) (store.Page, error) { return h.queries.GetPageByID(ctx, id) },
		func(lang store.Language, p store.Page) PageTranslationInfo {
			return PageTranslationInfo{Language: lang, Page: p}
		},
	)
	return pageLanguageInfo{
		PageLanguage:     result.EntityLanguage,
		AllLanguages:     result.AllLanguages,
		Translations:     result.Translations,
		MissingLanguages: result.MissingLanguages,
	}
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
	lang := h.renderer.GetAdminLang(r)

	// Load all categories for the selector
	allCategories, err := h.queries.ListCategories(r.Context())
	if err != nil {
		slog.Error("failed to list categories", "error", err)
		allCategories = []store.Category{}
	}
	categoryTree := buildPageCategoryTree(allCategories, nil, 0)

	// Load all active languages for selection
	allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

	// Get default language from loaded languages
	defaultLanguage := FindDefaultLanguage(allLanguages)

	data := PageFormData{
		AllCategories: categoryTree,
		AllLanguages:  allLanguages,
		Language:      defaultLanguage,
		Statuses:      ValidPageStatuses,
		Errors:        make(map[string]string),
		FormValues:    make(map[string]string),
		IsEdit:        false,
	}

	h.renderer.RenderPage(w, r, "admin/pages_form", render.TemplateData{
		Title: i18n.T(lang, "pages.new"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "pages.title"), URL: "/admin/pages"},
			{Label: i18n.T(lang, "pages.new"), URL: "/admin/pages/new", Active: true},
		},
	})
}

// Create handles POST /admin/pages - creates a new page.
func (h *PagesHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, "/admin/pages/new") {
		return
	}

	// Parse form input
	input := parsePageFormInput(r)

	// Default to the default language if not specified
	if !input.LanguageID.Valid {
		defaultLang, err := h.queries.GetDefaultLanguage(r.Context())
		if err == nil {
			input.LanguageID = util.NullInt64FromValue(defaultLang.ID)
		}
	}

	// Validate
	validationErrors := make(map[string]string)

	// Title validation
	if err := validatePageTitle(input.Title); err != "" {
		validationErrors["title"] = err
	}

	// Slug validation - auto-generate if empty
	if input.Slug == "" {
		input.Slug = util.Slugify(input.Title)
		input.FormValues["slug"] = input.Slug
	}

	if errMsg := h.validatePageSlugCreate(r.Context(), input.Slug); errMsg != "" {
		validationErrors["slug"] = errMsg
	}

	// Status validation
	if input.Status == "" {
		input.Status = PageStatusDraft
		input.FormValues["status"] = input.Status
	} else if !isValidPageStatus(input.Status) {
		validationErrors["status"] = "Invalid status"
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		// Load languages for re-rendering
		allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

		data := PageFormData{
			AllLanguages: allLanguages,
			Statuses:     ValidPageStatuses,
			Errors:       validationErrors,
			FormValues:   input.FormValues,
			IsEdit:       false,
		}

		h.renderer.RenderPage(w, r, "admin/pages_form", render.TemplateData{
			Title: i18n.T(lang, "pages.new"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "pages.title"), URL: "/admin/pages"},
				{Label: i18n.T(lang, "pages.new"), URL: "/admin/pages/new", Active: true},
			},
		})
		return
	}

	// Create page
	now := time.Now()
	userID := middleware.GetUserID(r)
	newPage, err := h.queries.CreatePage(r.Context(), store.CreatePageParams{
		Title:           input.Title,
		Slug:            input.Slug,
		Body:            input.Body,
		Status:          input.Status,
		AuthorID:        userID,
		FeaturedImageID: input.FeaturedImageID,
		MetaTitle:       input.MetaTitle,
		MetaDescription: input.MetaDescription,
		MetaKeywords:    input.MetaKeywords,
		OgImageID:       input.OgImageID,
		NoIndex:         input.NoIndex,
		NoFollow:        input.NoFollow,
		CanonicalUrl:    input.CanonicalURL,
		ScheduledAt:     input.ScheduledAt,
		LanguageID:      input.LanguageID,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		slog.Error("failed to create page", "error", err)
		flashError(w, r, h.renderer, "/admin/pages/new", "Error creating page")
		return
	}

	// Create initial version
	_, err = h.queries.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
		PageID:    newPage.ID,
		Title:     input.Title,
		Body:      input.Body,
		ChangedBy: userID,
		CreatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create page version", "error", err)
		// Page was created but version failed - log but don't fail the request
	}

	// Save tags and categories
	h.savePageTags(r.Context(), newPage.ID, r.Form["tags[]"])
	h.savePageCategories(r.Context(), newPage.ID, r.Form["categories[]"])

	slog.Info("page created", "page_id", newPage.ID, "slug", newPage.Slug, "created_by", middleware.GetUserID(r))

	// Dispatch page.created webhook event
	h.dispatchPageEvent(r.Context(), model.EventPageCreated, newPage, middleware.GetUserEmail(r))

	flashSuccess(w, r, h.renderer, "/admin/pages", "Page created successfully")
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
	adminLang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, "/admin/pages", "Invalid page ID")
		return
	}

	page, ok := h.requirePageWithRedirect(w, r, id)
	if !ok {
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
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Error("failed to get featured image", "error", err, "media_id", page.FeaturedImageID.Int64)
			}
		} else {
			featuredImage = &FeaturedImageData{
				ID:        media.ID,
				Filename:  media.Filename,
				Filepath:  fmt.Sprintf("/uploads/originals/%s/%s", media.Uuid, media.Filename),
				Thumbnail: fmt.Sprintf("/uploads/thumbnail/%s/%s", media.Uuid, media.Filename),
				Mimetype:  media.MimeType,
			}
		}
	}

	// Load language and translation info
	langInfo := h.loadPageLanguageInfo(r.Context(), page)

	data := PageFormData{
		Page:             &page,
		Tags:             tags,
		Categories:       categories,
		AllCategories:    categoryTree,
		FeaturedImage:    featuredImage,
		AllLanguages:     langInfo.AllLanguages,
		Language:         langInfo.PageLanguage,
		Translations:     langInfo.Translations,
		MissingLanguages: langInfo.MissingLanguages,
		Statuses:         ValidPageStatuses,
		Errors:           make(map[string]string),
		FormValues:       make(map[string]string),
		IsEdit:           true,
	}

	h.renderer.RenderPage(w, r, "admin/pages_form", render.TemplateData{
		Title: i18n.T(adminLang, "pages.edit"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(adminLang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(adminLang, "pages.title"), URL: "/admin/pages"},
			{Label: page.Title, URL: fmt.Sprintf("/admin/pages/%d", page.ID), Active: true},
		},
	})
}

// Update handles PUT /admin/pages/{id} - updates an existing page.
func (h *PagesHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, "/admin/pages", "Invalid page ID")
		return
	}

	existingPage, ok := h.requirePageWithRedirect(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf("/admin/pages/%d", id)) {
		return
	}

	input := parsePageFormInput(r)

	// Auto-generate slug if empty
	if input.Slug == "" {
		input.Slug = util.Slugify(input.Title)
		input.FormValues["slug"] = input.Slug
	}

	// Validate
	validationErrors := make(map[string]string)

	if err := validatePageTitle(input.Title); err != "" {
		validationErrors["title"] = err
	}

	// Slug validation
	if slugErr := h.validatePageSlugUpdate(r.Context(), input.Slug, existingPage.Slug, id); slugErr != "" {
		validationErrors["slug"] = slugErr
	}

	// Status validation
	status := input.Status
	if status == "" {
		status = existingPage.Status
		input.FormValues["status"] = status
	} else if !isValidPageStatus(status) {
		validationErrors["status"] = "Invalid status"
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		data := PageFormData{
			Page:       &existingPage,
			Statuses:   ValidPageStatuses,
			Errors:     validationErrors,
			FormValues: input.FormValues,
			IsEdit:     true,
		}

		h.renderer.RenderPage(w, r, "admin/pages_form", render.TemplateData{
			Title: i18n.T(lang, "pages.edit"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "pages.title"), URL: "/admin/pages"},
				{Label: existingPage.Title, URL: fmt.Sprintf("/admin/pages/%d", id), Active: true},
			},
		})
		return
	}

	// Update page
	now := time.Now()
	updatedPage, err := h.queries.UpdatePage(r.Context(), store.UpdatePageParams{
		ID:              id,
		Title:           input.Title,
		Slug:            input.Slug,
		Body:            input.Body,
		Status:          status,
		FeaturedImageID: input.FeaturedImageID,
		MetaTitle:       input.MetaTitle,
		MetaDescription: input.MetaDescription,
		MetaKeywords:    input.MetaKeywords,
		OgImageID:       input.OgImageID,
		NoIndex:         input.NoIndex,
		NoFollow:        input.NoFollow,
		CanonicalUrl:    input.CanonicalURL,
		ScheduledAt:     input.ScheduledAt,
		LanguageID:      existingPage.LanguageID,
		UpdatedAt:       now,
	})
	if err != nil {
		slog.Error("failed to update page", "error", err, "page_id", id)
		flashError(w, r, h.renderer, fmt.Sprintf("/admin/pages/%d", id), "Error updating page")
		return
	}

	// Create new version (only if title or body changed)
	if input.Title != existingPage.Title || input.Body != existingPage.Body {
		_, err = h.queries.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
			PageID:    id,
			Title:     input.Title,
			Body:      input.Body,
			ChangedBy: middleware.GetUserID(r),
			CreatedAt: now,
		})
		if err != nil {
			slog.Error("failed to create page version", "error", err, "page_id", id)
			// Don't fail the request - page was updated
		}
	}

	// Update tags - clear existing and add new
	if err = h.queries.ClearPageTags(r.Context(), id); err != nil {
		slog.Error("failed to clear page tags", "error", err, "page_id", id)
	}
	h.savePageTags(r.Context(), id, r.Form["tags[]"])

	// Update categories - clear existing and add new
	if err = h.queries.ClearPageCategories(r.Context(), id); err != nil {
		slog.Error("failed to clear page categories", "error", err, "page_id", id)
	}
	h.savePageCategories(r.Context(), id, r.Form["categories[]"])

	slog.Info("page updated", "page_id", updatedPage.ID, "slug", updatedPage.Slug, "updated_by", middleware.GetUserID(r))

	// Dispatch page.updated webhook event
	h.dispatchPageEvent(r.Context(), model.EventPageUpdated, updatedPage, middleware.GetUserEmail(r))

	flashSuccess(w, r, h.renderer, "/admin/pages", "Page updated successfully")
}

// Delete handles DELETE /admin/pages/{id} - deletes a page.
func (h *PagesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid page ID", http.StatusBadRequest)
		return
	}

	page, ok := h.requirePageWithError(w, r, id)
	if !ok {
		return
	}

	// Delete the page (versions are cascade deleted by FK constraint)
	err = h.queries.DeletePage(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete page", "error", err, "page_id", id)
		http.Error(w, "Error deleting page", http.StatusInternalServerError)
		return
	}

	slog.Info("page deleted", "page_id", id, "slug", page.Slug, "deleted_by", middleware.GetUserID(r))

	// Dispatch page.deleted webhook event
	h.dispatchPageEvent(r.Context(), model.EventPageDeleted, page, middleware.GetUserEmail(r))

	// For HTMX requests, return empty response (row removed)
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For regular requests, redirect
	flashSuccess(w, r, h.renderer, "/admin/pages", "Page deleted successfully")
}

// TogglePublish handles POST /admin/pages/{id}/publish - toggles publish status.
func (h *PagesHandler) TogglePublish(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, "/admin/pages", "Invalid page ID")
		return
	}

	page, ok := h.requirePageWithRedirect(w, r, id)
	if !ok {
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
		slog.Info("page unpublished", "page_id", id, "slug", page.Slug, "unpublished_by", middleware.GetUserID(r))
	} else {
		// Publish
		_, err = h.queries.PublishPage(r.Context(), store.PublishPageParams{
			ID:          id,
			UpdatedAt:   now,
			PublishedAt: sql.NullTime{Time: now, Valid: true},
		})
		message = "Page published successfully"
		eventType = model.EventPagePublished
		slog.Info("page published", "page_id", id, "slug", page.Slug, "published_by", middleware.GetUserID(r))
	}

	if err != nil {
		slog.Error("failed to toggle publish status", "error", err, "page_id", id)
		flashError(w, r, h.renderer, "/admin/pages", "Error updating page status")
		return
	}

	// Get updated page for webhook event
	updatedPage, err := h.queries.GetPageByID(r.Context(), id)
	if err == nil {
		h.dispatchPageEvent(r.Context(), eventType, updatedPage, middleware.GetUserEmail(r))
	}

	flashSuccess(w, r, h.renderer, "/admin/pages", message)
}

// VersionsPerPage is the number of versions to display per page.
const VersionsPerPage = 20

// PageVersionsData holds data for the page versions template.
type PageVersionsData struct {
	Page       store.Page
	Versions   []store.ListPageVersionsWithUserRow
	TotalCount int64
	Pagination AdminPagination
}

// Versions handles GET /admin/pages/{id}/versions - displays version history.
func (h *PagesHandler) Versions(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, "/admin/pages", "Invalid page ID")
		return
	}

	page, ok := h.requirePageWithRedirect(w, r, id)
	if !ok {
		return
	}

	pageNum := ParsePageParam(r)

	// Get total count
	totalCount, err := h.queries.CountPageVersions(r.Context(), id)
	if err != nil {
		slog.Error("failed to count page versions", "error", err, "page_id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Normalize page to valid range
	pageNum, _ = NormalizePagination(pageNum, int(totalCount), VersionsPerPage)
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
		Page:       page,
		Versions:   versions,
		TotalCount: totalCount,
		Pagination: BuildAdminPagination(pageNum, int(totalCount), VersionsPerPage, fmt.Sprintf("/admin/pages/%d/versions", id), r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/pages_versions", render.TemplateData{
		Title: fmt.Sprintf("%s - %s", i18n.T(lang, "versions.title"), page.Title),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "pages.title"), URL: "/admin/pages"},
			{Label: page.Title, URL: fmt.Sprintf("/admin/pages/%d", page.ID)},
			{Label: i18n.T(lang, "versions.title"), URL: fmt.Sprintf("/admin/pages/%d/versions", page.ID), Active: true},
		},
	})
}

// RestoreVersion handles POST /admin/pages/{id}/versions/{versionId}/restore - restores a version.
func (h *PagesHandler) RestoreVersion(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, "/admin/pages", "Invalid page ID")
		return
	}

	// Get version ID from URL
	versionsURL := fmt.Sprintf("/admin/pages/%d/versions", id)
	versionIdStr := chi.URLParam(r, "versionId")
	versionId, err := strconv.ParseInt(versionIdStr, 10, 64)
	if err != nil {
		flashError(w, r, h.renderer, versionsURL, "Invalid version ID")
		return
	}

	page, ok := h.requirePageWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Get version to restore
	version, err := h.queries.GetPageVersionWithUser(r.Context(), versionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			flashError(w, r, h.renderer, versionsURL, "Version not found")
		} else {
			slog.Error("failed to get page version", "error", err, "version_id", versionId)
			flashError(w, r, h.renderer, versionsURL, "Error loading version")
		}
		return
	}

	// Verify version belongs to this page
	if version.PageID != id {
		flashError(w, r, h.renderer, versionsURL, "Version does not belong to this page")
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
		flashError(w, r, h.renderer, versionsURL, "Error restoring version")
		return
	}

	// Create new version to record the restore
	_, err = h.queries.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
		PageID:    id,
		Title:     version.Title,
		Body:      version.Body,
		ChangedBy: middleware.GetUserID(r),
		CreatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create page version after restore", "error", err, "page_id", id)
		// Don't fail the request - page was restored
	}

	slog.Info("page version restored", "page_id", id, "version_id", versionId, "restored_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, fmt.Sprintf("/admin/pages/%d", id), "Version restored successfully")
}

// Translate handles POST /admin/pages/{id}/translate/{langCode} - creates a translation.
func (h *PagesHandler) Translate(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, "/admin/pages", "Invalid page ID")
		return
	}

	langCode := chi.URLParam(r, "langCode")
	redirectURL := fmt.Sprintf("/admin/pages/%d", id)
	if langCode == "" {
		flashError(w, r, h.renderer, redirectURL, "Language code is required")
		return
	}

	sourcePage, ok := h.requirePageWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Validate language and check for existing translation
	tc, ok := getTargetLanguageForTranslation(w, r, h.queries, h.renderer, langCode, redirectURL, model.EntityTypePage, id)
	if !ok {
		return
	}

	// Generate a unique slug for the translated page
	translatedSlug, err := generateUniqueSlug(sourcePage.Slug, langCode, func(slug string) (int64, error) {
		return h.queries.SlugExists(r.Context(), slug)
	})
	if err != nil {
		slog.Error("database error checking slug", "error", err)
		flashError(w, r, h.renderer, redirectURL, "Error creating translation")
		return
	}

	// Create the translated page with same title but empty body
	now := time.Now()
	userID := middleware.GetUserID(r)
	translatedPage, err := h.queries.CreatePage(r.Context(), store.CreatePageParams{
		Title:           sourcePage.Title, // Keep same title (user will translate)
		Slug:            translatedSlug,
		Body:            "",              // Empty body for translation
		Status:          PageStatusDraft, // Always start as draft
		AuthorID:        userID,
		FeaturedImageID: sourcePage.FeaturedImageID,
		MetaTitle:       "",
		MetaDescription: "",
		MetaKeywords:    "",
		OgImageID:       sql.NullInt64{},
		NoIndex:         0,
		NoFollow:        0,
		CanonicalUrl:    "",
		ScheduledAt:     sql.NullTime{},
		LanguageID:      util.NullInt64FromValue(tc.TargetLang.ID),
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		slog.Error("failed to create translated page", "error", err)
		flashError(w, r, h.renderer, redirectURL, "Error creating translation")
		return
	}

	// Create initial version for the translated page
	_, err = h.queries.CreatePageVersion(r.Context(), store.CreatePageVersionParams{
		PageID:    translatedPage.ID,
		Title:     translatedPage.Title,
		Body:      translatedPage.Body,
		ChangedBy: userID,
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
		LanguageID:    tc.TargetLang.ID,
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
		"created_by", userID)

	flashSuccess(w, r, h.renderer, fmt.Sprintf("/admin/pages/%d", translatedPage.ID), fmt.Sprintf("Translation created for %s. Please translate the content.", tc.TargetLang.Name))
}

// Helper functions

// requirePageWithRedirect fetches page by ID and handles errors with flash messages and redirect.
func (h *PagesHandler) requirePageWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Page, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, "/admin/pages", "Page", id,
		func(id int64) (store.Page, error) { return h.queries.GetPageByID(r.Context(), id) })
}

// requirePageWithError fetches page by ID and handles errors with http.Error.
func (h *PagesHandler) requirePageWithError(w http.ResponseWriter, r *http.Request, id int64) (store.Page, bool) {
	return requireEntityWithError(w, "Page", id,
		func(id int64) (store.Page, error) { return h.queries.GetPageByID(r.Context(), id) })
}

// pageFormInput holds parsed page form input values.
type pageFormInput struct {
	Title           string
	Slug            string
	Body            string
	Status          string
	FeaturedImageID sql.NullInt64
	MetaTitle       string
	MetaDescription string
	MetaKeywords    string
	OgImageID       sql.NullInt64
	NoIndex         int64
	NoFollow        int64
	CanonicalURL    string
	ScheduledAt     sql.NullTime
	LanguageID      sql.NullInt64
	FormValues      map[string]string
}

// parsePageFormInput parses common page form values from request.
func parsePageFormInput(r *http.Request) pageFormInput {
	title := strings.TrimSpace(r.FormValue("title"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	body := r.FormValue("body")
	status := r.FormValue("status")
	featuredImageIDStr := r.FormValue("featured_image_id")

	// SEO fields
	metaTitle := strings.TrimSpace(r.FormValue("meta_title"))
	metaDescription := strings.TrimSpace(r.FormValue("meta_description"))
	metaKeywords := strings.TrimSpace(r.FormValue("meta_keywords"))
	ogImageIDStr := r.FormValue("og_image_id")
	noIndexStr := r.FormValue("no_index")
	noFollowStr := r.FormValue("no_follow")
	canonicalURL := strings.TrimSpace(r.FormValue("canonical_url"))
	scheduledAtStr := strings.TrimSpace(r.FormValue("scheduled_at"))
	languageIDStr := r.FormValue("language_id")

	// Parse featured image ID
	featuredImageID := util.ParseNullInt64Positive(featuredImageIDStr)

	// Parse OG image ID
	ogImageID := util.ParseNullInt64Positive(ogImageIDStr)

	// Parse boolean SEO fields
	var noIndex int64
	if noIndexStr == "1" || noIndexStr == "on" {
		noIndex = 1
	}
	var noFollow int64
	if noFollowStr == "1" || noFollowStr == "on" {
		noFollow = 1
	}

	// Parse scheduled_at
	var scheduledAt sql.NullTime
	if scheduledAtStr != "" {
		if t, err := time.Parse("2006-01-02T15:04", scheduledAtStr); err == nil {
			scheduledAt = sql.NullTime{Time: t, Valid: true}
		}
	}

	// Parse language_id
	languageID := util.ParseNullInt64Positive(languageIDStr)

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

	return pageFormInput{
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
		CanonicalURL:    canonicalURL,
		ScheduledAt:     scheduledAt,
		LanguageID:      languageID,
		FormValues:      formValues,
	}
}

// validatePageTitle validates the page title and returns error message if invalid.
func validatePageTitle(title string) string {
	if title == "" {
		return "Title is required"
	}
	if len(title) < 2 {
		return "Title must be at least 2 characters"
	}
	return ""
}

// validatePageSlugCreate validates a page slug for creation.
func (h *PagesHandler) validatePageSlugCreate(ctx context.Context, slug string) string {
	return ValidateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.SlugExists(ctx, slug)
	})
}

// validatePageSlugUpdate validates the page slug for update (checks uniqueness excluding current page).
func (h *PagesHandler) validatePageSlugUpdate(ctx context.Context, slug string, currentSlug string, pageID int64) string {
	return ValidateSlugForUpdate(slug, currentSlug, func() (int64, error) {
		return h.queries.SlugExistsExcluding(ctx, store.SlugExistsExcludingParams{
			Slug: slug,
			ID:   pageID,
		})
	})
}

// savePageTags saves tag associations for a page from form values.
func (h *PagesHandler) savePageTags(ctx context.Context, pageID int64, tagIDStrs []string) {
	saveBatchAssociations(tagIDStrs, func(tagID int64) error {
		return h.queries.AddTagToPage(ctx, store.AddTagToPageParams{
			PageID: pageID,
			TagID:  tagID,
		})
	}, "page_tags")
}

// savePageCategories saves category associations for a page from form values.
func (h *PagesHandler) savePageCategories(ctx context.Context, pageID int64, categoryIDStrs []string) {
	saveBatchAssociations(categoryIDStrs, func(categoryID int64) error {
		return h.queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
			PageID:     pageID,
			CategoryID: categoryID,
		})
	}, "page_categories")
}
