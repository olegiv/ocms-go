package handler

import (
	"context"
	"database/sql"
	"encoding/json"
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
	Tags       []store.GetTagUsageCountsWithLanguageRow
	TotalCount int64
	Pagination AdminPagination
}

// ListTags handles GET /admin/tags - displays a paginated list of tags.
func (h *TaxonomyHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	page := ParsePageParam(r)

	// Get total count
	totalCount, err := h.queries.CountTags(r.Context())
	if err != nil {
		slog.Error("failed to count tags", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalCount), TagsPerPage)
	offset := int64((page - 1) * TagsPerPage)

	// Fetch tags with usage counts and language info
	tags, err := h.queries.GetTagUsageCountsWithLanguage(r.Context(), store.GetTagUsageCountsWithLanguageParams{
		Limit:  TagsPerPage,
		Offset: offset,
	})
	if err != nil {
		slog.Error("failed to list tags", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := TagsListData{
		Tags:       tags,
		TotalCount: totalCount,
		Pagination: BuildAdminPagination(page, int(totalCount), TagsPerPage, "/admin/tags", r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/tags_list", render.TemplateData{
		Title: i18n.T(lang, "nav.tags"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.tags"), URL: "/admin/tags", Active: true},
		},
	})
}

// TagTranslationInfo holds information about a tag translation.
type TagTranslationInfo struct {
	Language store.Language
	Tag      store.Tag
}

// TagFormData holds data for the tag form template.
type TagFormData struct {
	Tag              *store.Tag
	Errors           map[string]string
	FormValues       map[string]string
	IsEdit           bool
	Language         *store.Language      // Current tag language
	AllLanguages     []store.Language     // All active languages for selection
	Translations     []TagTranslationInfo // Existing translations
	MissingLanguages []store.Language     // Languages without translations
}

// NewTagForm handles GET /admin/tags/new - displays the new tag form.
func (h *TaxonomyHandler) NewTagForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	// Load all active languages
	allLanguages, err := h.queries.ListActiveLanguages(r.Context())
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		allLanguages = []store.Language{}
	}

	// Get default language for preselection
	var defaultLanguage *store.Language
	for i := range allLanguages {
		if allLanguages[i].IsDefault {
			defaultLanguage = &allLanguages[i]
			break
		}
	}

	data := TagFormData{
		Errors:       make(map[string]string),
		FormValues:   make(map[string]string),
		IsEdit:       false,
		AllLanguages: allLanguages,
		Language:     defaultLanguage,
	}

	h.renderer.RenderPage(w, r, "admin/tags_form", render.TemplateData{
		Title: i18n.T(lang, "tags.new"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.tags"), URL: "/admin/tags"},
			{Label: i18n.T(lang, "tags.new"), URL: "/admin/tags/new", Active: true},
		},
	})
}

// CreateTag handles POST /admin/tags - creates a new tag.
func (h *TaxonomyHandler) CreateTag(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/tags/new", http.StatusSeeOther)
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	languageIDStr := r.FormValue("language_id")

	languageID := h.parseLanguageIDWithDefault(r.Context(), languageIDStr)

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":        name,
		"slug":        slug,
		"language_id": languageIDStr,
	}

	slug = autoGenerateSlug(name, slug, formValues)

	// Validate
	validationErrors := make(map[string]string)
	if errMsg := validateTaxonomyName(name); errMsg != "" {
		validationErrors["name"] = errMsg
	}
	if errMsg := h.validateTagSlugCreate(r.Context(), slug); errMsg != "" {
		validationErrors["slug"] = errMsg
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		// Load all active languages for the form
		allLanguages, _ := h.queries.ListActiveLanguages(r.Context())
		var currentLanguage *store.Language
		if languageID.Valid {
			langObj, err := h.queries.GetLanguageByID(r.Context(), languageID.Int64)
			if err == nil {
				currentLanguage = &langObj
			}
		}

		data := TagFormData{
			Errors:       validationErrors,
			FormValues:   formValues,
			IsEdit:       false,
			AllLanguages: allLanguages,
			Language:     currentLanguage,
		}

		h.renderer.RenderPage(w, r, "admin/tags_form", render.TemplateData{
			Title: i18n.T(lang, "tags.new"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "nav.tags"), URL: "/admin/tags"},
				{Label: i18n.T(lang, "tags.new"), URL: "/admin/tags/new", Active: true},
			},
		})
		return
	}

	// Create tag
	now := time.Now()
	newTag, err := h.queries.CreateTag(r.Context(), store.CreateTagParams{
		Name:       name,
		Slug:       slug,
		LanguageID: languageID,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to create tag", "error", err)
		h.renderer.SetFlash(r, "Error creating tag", "error")
		http.Redirect(w, r, "/admin/tags/new", http.StatusSeeOther)
		return
	}

	slog.Info("tag created", "tag_id", newTag.ID, "slug", newTag.Slug, "created_by", middleware.GetUserID(r))
	h.renderer.SetFlash(r, "Tag created successfully", "success")
	http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
}

// EditTagForm handles GET /admin/tags/{id} - displays the edit tag form.
func (h *TaxonomyHandler) EditTagForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid tag ID", "error")
		http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
		return
	}

	tag, ok := h.requireTagWithRedirect(w, r, id)
	if !ok {
		return
	}

	langInfo := h.loadTagLanguageInfo(r.Context(), tag)

	data := TagFormData{
		Tag:              &tag,
		Errors:           make(map[string]string),
		FormValues:       make(map[string]string),
		IsEdit:           true,
		AllLanguages:     langInfo.AllLanguages,
		Language:         langInfo.TagLanguage,
		Translations:     langInfo.Translations,
		MissingLanguages: langInfo.MissingLanguages,
	}

	h.renderer.RenderPage(w, r, "admin/tags_form", render.TemplateData{
		Title: tag.Name,
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.tags"), URL: "/admin/tags"},
			{Label: tag.Name, URL: fmt.Sprintf("/admin/tags/%d", tag.ID), Active: true},
		},
	})
}

// UpdateTag handles PUT /admin/tags/{id} - updates an existing tag.
func (h *TaxonomyHandler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid tag ID", "error")
		http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
		return
	}

	existingTag, ok := h.requireTagWithRedirect(w, r, id)
	if !ok {
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

	slug = autoGenerateSlug(name, slug, formValues)

	// Validate
	validationErrors := make(map[string]string)
	if errMsg := validateTaxonomyName(name); errMsg != "" {
		validationErrors["name"] = errMsg
	}
	if errMsg := h.validateTagSlugUpdate(r.Context(), slug, existingTag.Slug, id); errMsg != "" {
		validationErrors["slug"] = errMsg
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		langInfo := h.loadTagLanguageInfo(r.Context(), existingTag)

		data := TagFormData{
			Tag:          &existingTag,
			Errors:       validationErrors,
			FormValues:   formValues,
			IsEdit:       true,
			AllLanguages: langInfo.AllLanguages,
			Language:     langInfo.TagLanguage,
		}

		h.renderer.RenderPage(w, r, "admin/tags_form", render.TemplateData{
			Title: existingTag.Name,
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "nav.tags"), URL: "/admin/tags"},
				{Label: existingTag.Name, URL: fmt.Sprintf("/admin/tags/%d", id), Active: true},
			},
		})
		return
	}

	// Update tag (keep existing language_id - language cannot be changed after creation)
	now := time.Now()
	updatedTag, err := h.queries.UpdateTag(r.Context(), store.UpdateTagParams{
		ID:         id,
		Name:       name,
		Slug:       slug,
		LanguageID: existingTag.LanguageID,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to update tag", "error", err, "tag_id", id)
		h.renderer.SetFlash(r, "Error updating tag", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", id), http.StatusSeeOther)
		return
	}

	slog.Info("tag updated", "tag_id", updatedTag.ID, "slug", updatedTag.Slug, "updated_by", middleware.GetUserID(r))
	h.renderer.SetFlash(r, "Tag updated successfully", "success")
	http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
}

// DeleteTag handles DELETE /admin/tags/{id} - deletes a tag.
func (h *TaxonomyHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid tag ID", http.StatusBadRequest)
		return
	}

	tag, ok := h.requireTagWithError(w, r, id)
	if !ok {
		return
	}

	// Delete the tag (page_tags are cascade deleted by FK constraint)
	err = h.queries.DeleteTag(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete tag", "error", err, "tag_id", id)
		http.Error(w, "Error deleting tag", http.StatusInternalServerError)
		return
	}

	slog.Info("tag deleted", "tag_id", id, "slug", tag.Slug, "deleted_by", middleware.GetUserID(r))

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
		writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(tags); err != nil {
		slog.Error("failed to encode tags response", "error", err)
	}
}

// TranslateTag handles POST /admin/tags/{id}/translate/{langCode} - creates a translation.
func (h *TaxonomyHandler) TranslateTag(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid tag ID", "error")
		http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
		return
	}

	// Get language code from URL
	langCode := chi.URLParam(r, "langCode")
	if langCode == "" {
		h.renderer.SetFlash(r, "Language code is required", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", id), http.StatusSeeOther)
		return
	}

	sourceTag, ok := h.requireTagWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Get target language
	targetLang, err := h.queries.GetLanguageByCode(r.Context(), langCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderer.SetFlash(r, "Language not found", "error")
		} else {
			slog.Error("failed to get language", "error", err, "lang_code", langCode)
			h.renderer.SetFlash(r, "Error loading language", "error")
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", id), http.StatusSeeOther)
		return
	}

	// Check if translation already exists
	existingTranslation, err := h.queries.GetTranslation(r.Context(), store.GetTranslationParams{
		EntityType: model.EntityTypeTag,
		EntityID:   id,
		LanguageID: targetLang.ID,
	})
	if err == nil && existingTranslation.ID > 0 {
		// Translation already exists, redirect to it
		h.renderer.SetFlash(r, "Translation already exists", "info")
		http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", existingTranslation.TranslationID), http.StatusSeeOther)
		return
	}

	// Generate a unique slug for the translated tag
	baseSlug := sourceTag.Slug + "-" + langCode
	translatedSlug := baseSlug
	counter := 1
	for {
		exists, err := h.queries.TagSlugExists(r.Context(), translatedSlug)
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			h.renderer.SetFlash(r, "Error creating translation", "error")
			http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", id), http.StatusSeeOther)
			return
		}
		if exists == 0 {
			break
		}
		counter++
		translatedSlug = fmt.Sprintf("%s-%d", baseSlug, counter)
	}

	// Create the translated tag with same name
	now := time.Now()
	translatedTag, err := h.queries.CreateTag(r.Context(), store.CreateTagParams{
		Name:       sourceTag.Name, // Keep same name (user will translate)
		Slug:       translatedSlug,
		LanguageID: sql.NullInt64{Int64: targetLang.ID, Valid: true},
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to create translated tag", "error", err)
		h.renderer.SetFlash(r, "Error creating translation", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", id), http.StatusSeeOther)
		return
	}

	// Create translation link from source to translated tag
	_, err = h.queries.CreateTranslation(r.Context(), store.CreateTranslationParams{
		EntityType:    model.EntityTypeTag,
		EntityID:      id,
		LanguageID:    targetLang.ID,
		TranslationID: translatedTag.ID,
		CreatedAt:     now,
	})
	if err != nil {
		slog.Error("failed to create translation link", "error", err)
		// Tag was created, so we should still redirect to it
	}

	slog.Info("tag translation created",
		"source_tag_id", id,
		"translated_tag_id", translatedTag.ID,
		"language", langCode,
		"created_by", middleware.GetUserID(r))

	h.renderer.SetFlash(r, fmt.Sprintf("Translation created for %s. Please translate the name.", targetLang.Name), "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/tags/%d", translatedTag.ID), http.StatusSeeOther)
}

// =============================================================================
// CATEGORY HANDLERS
// =============================================================================

// CategoryTreeNode represents a category with its children for tree display.
type CategoryTreeNode struct {
	Category     store.Category
	Children     []CategoryTreeNode
	Depth        int
	UsageCount   int64
	LanguageCode string
	LanguageName string
}

// CategoriesListData holds data for the categories list template.
type CategoriesListData struct {
	Categories []CategoryTreeNode
	TotalCount int64
}

// buildCategoryTree builds a tree structure from flat category list.
func buildCategoryTree(categories []store.Category, parentID *int64, depth int) []CategoryTreeNode {
	var nodes []CategoryTreeNode

	for _, cat := range categories {
		// Check if this category belongs to the current parent
		var catParentID *int64
		if cat.ParentID.Valid {
			catParentID = &cat.ParentID.Int64
		}

		parentMatch := (parentID == nil && catParentID == nil) ||
			(parentID != nil && catParentID != nil && *parentID == *catParentID)

		if parentMatch {
			node := CategoryTreeNode{
				Category: cat,
				Depth:    depth,
				Children: buildCategoryTree(categories, &cat.ID, depth+1),
			}
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// flattenCategoryTree flattens tree for display with indentation.
func flattenCategoryTree(nodes []CategoryTreeNode) []CategoryTreeNode {
	var result []CategoryTreeNode
	for _, node := range nodes {
		result = append(result, node)
		result = append(result, flattenCategoryTree(node.Children)...)
	}
	return result
}

// buildFilteredCategoryTree builds a flat category tree excluding a category and its descendants.
// Used for parent selectors where the category cannot be its own parent or ancestor.
func (h *TaxonomyHandler) buildFilteredCategoryTree(ctx context.Context, excludeID int64) []CategoryTreeNode {
	categories, err := h.queries.ListCategories(ctx)
	if err != nil {
		slog.Error("failed to list categories", "error", err)
		categories = []store.Category{}
	}

	// Get descendant IDs to exclude
	descendantIDs, _ := h.queries.GetDescendantIDs(ctx, sql.NullInt64{Int64: excludeID, Valid: true})
	excludeIDs := make(map[int64]bool)
	excludeIDs[excludeID] = true
	for _, did := range descendantIDs {
		excludeIDs[did] = true
	}

	// Filter out excluded categories
	var filteredCategories []store.Category
	for _, cat := range categories {
		if !excludeIDs[cat.ID] {
			filteredCategories = append(filteredCategories, cat)
		}
	}

	tree := buildCategoryTree(filteredCategories, nil, 0)
	return flattenCategoryTree(tree)
}

// buildCategoryTreeWithUsage builds a tree structure from categories with usage counts.
func buildCategoryTreeWithUsage(categories []store.GetCategoryUsageCountsWithLanguageRow, parentID *int64, depth int) []CategoryTreeNode {
	var nodes []CategoryTreeNode

	for _, cat := range categories {
		// Check if this category belongs to the current parent
		var catParentID *int64
		if cat.ParentID.Valid {
			catParentID = &cat.ParentID.Int64
		}

		parentMatch := (parentID == nil && catParentID == nil) ||
			(parentID != nil && catParentID != nil && *parentID == *catParentID)

		if parentMatch {
			// Convert to store.Category for compatibility
			storeCat := store.Category{
				ID:          cat.ID,
				Name:        cat.Name,
				Slug:        cat.Slug,
				Description: cat.Description,
				ParentID:    cat.ParentID,
				Position:    cat.Position,
				LanguageID:  cat.LanguageID,
				CreatedAt:   cat.CreatedAt,
				UpdatedAt:   cat.UpdatedAt,
			}
			node := CategoryTreeNode{
				Category:     storeCat,
				Depth:        depth,
				UsageCount:   cat.UsageCount,
				LanguageCode: cat.LanguageCode,
				LanguageName: cat.LanguageName,
				Children:     buildCategoryTreeWithUsage(categories, &cat.ID, depth+1),
			}
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// ListCategories handles GET /admin/categories - displays category tree.
func (h *TaxonomyHandler) ListCategories(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	// Get all categories with usage counts and language info
	categories, err := h.queries.GetCategoryUsageCountsWithLanguage(r.Context())
	if err != nil {
		slog.Error("failed to list categories", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get total count
	totalCount, err := h.queries.CountCategories(r.Context())
	if err != nil {
		slog.Error("failed to count categories", "error", err)
		totalCount = int64(len(categories))
	}

	// Build tree structure with usage counts
	tree := buildCategoryTreeWithUsage(categories, nil, 0)
	flatTree := flattenCategoryTree(tree)

	data := CategoriesListData{
		Categories: flatTree,
		TotalCount: totalCount,
	}

	h.renderer.RenderPage(w, r, "admin/categories_list", render.TemplateData{
		Title: i18n.T(lang, "nav.categories"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.categories"), URL: "/admin/categories", Active: true},
		},
	})
}

// CategoryTranslationInfo holds information about a category translation.
type CategoryTranslationInfo struct {
	Language store.Language
	Category store.Category
}

// CategoryFormData holds data for the category form template.
type CategoryFormData struct {
	Category         *store.Category
	AllCategories    []CategoryTreeNode // For parent selector
	Errors           map[string]string
	FormValues       map[string]string
	IsEdit           bool
	Language         *store.Language           // Current category language
	AllLanguages     []store.Language          // All active languages for selection
	Translations     []CategoryTranslationInfo // Existing translations
	MissingLanguages []store.Language          // Languages without translations
}

// NewCategoryForm handles GET /admin/categories/new - displays the new category form.
func (h *TaxonomyHandler) NewCategoryForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	// Get all categories for parent selector
	categories, err := h.queries.ListCategories(r.Context())
	if err != nil {
		slog.Error("failed to list categories", "error", err)
		categories = []store.Category{}
	}

	tree := buildCategoryTree(categories, nil, 0)
	flatTree := flattenCategoryTree(tree)

	// Load all active languages
	allLanguages, err := h.queries.ListActiveLanguages(r.Context())
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		allLanguages = []store.Language{}
	}

	// Get default language for preselection
	var defaultLanguage *store.Language
	for i := range allLanguages {
		if allLanguages[i].IsDefault {
			defaultLanguage = &allLanguages[i]
			break
		}
	}

	data := CategoryFormData{
		AllCategories: flatTree,
		Errors:        make(map[string]string),
		FormValues:    make(map[string]string),
		IsEdit:        false,
		AllLanguages:  allLanguages,
		Language:      defaultLanguage,
	}

	h.renderer.RenderPage(w, r, "admin/categories_form", render.TemplateData{
		Title: i18n.T(lang, "categories.new"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.categories"), URL: "/admin/categories"},
			{Label: i18n.T(lang, "categories.new"), URL: "/admin/categories/new", Active: true},
		},
	})
}

// CreateCategory handles POST /admin/categories - creates a new category.
func (h *TaxonomyHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/categories/new", http.StatusSeeOther)
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	description := strings.TrimSpace(r.FormValue("description"))
	parentIDStr := r.FormValue("parent_id")
	languageIDStr := r.FormValue("language_id")

	// Parse parent ID
	var parentID sql.NullInt64
	if parentIDStr != "" && parentIDStr != "0" {
		if pid, err := strconv.ParseInt(parentIDStr, 10, 64); err == nil {
			parentID = sql.NullInt64{Int64: pid, Valid: true}
		}
	}

	languageID := h.parseLanguageIDWithDefault(r.Context(), languageIDStr)

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":        name,
		"slug":        slug,
		"description": description,
		"parent_id":   parentIDStr,
		"language_id": languageIDStr,
	}

	slug = autoGenerateSlug(name, slug, formValues)

	// Validate
	validationErrors := make(map[string]string)
	if errMsg := validateTaxonomyName(name); errMsg != "" {
		validationErrors["name"] = errMsg
	}
	if errMsg := h.validateCategorySlugCreate(r.Context(), slug); errMsg != "" {
		validationErrors["slug"] = errMsg
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		// Get categories for parent selector
		categories, _ := h.queries.ListCategories(r.Context())
		tree := buildCategoryTree(categories, nil, 0)
		flatTree := flattenCategoryTree(tree)

		// Load all active languages for the form
		allLanguages, _ := h.queries.ListActiveLanguages(r.Context())
		var currentLanguage *store.Language
		if languageID.Valid {
			lang, err := h.queries.GetLanguageByID(r.Context(), languageID.Int64)
			if err == nil {
				currentLanguage = &lang
			}
		}

		data := CategoryFormData{
			AllCategories: flatTree,
			Errors:        validationErrors,
			FormValues:    formValues,
			IsEdit:        false,
			AllLanguages:  allLanguages,
			Language:      currentLanguage,
		}

		h.renderer.RenderPage(w, r, "admin/categories_form", render.TemplateData{
			Title: i18n.T(lang, "categories.new"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "nav.categories"), URL: "/admin/categories"},
				{Label: i18n.T(lang, "categories.new"), URL: "/admin/categories/new", Active: true},
			},
		})
		return
	}

	// Create category
	now := time.Now()
	newCategory, err := h.queries.CreateCategory(r.Context(), store.CreateCategoryParams{
		Name:        name,
		Slug:        slug,
		Description: sql.NullString{String: description, Valid: description != ""},
		ParentID:    parentID,
		Position:    0,
		LanguageID:  languageID,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		slog.Error("failed to create category", "error", err)
		h.renderer.SetFlash(r, "Error creating category", "error")
		http.Redirect(w, r, "/admin/categories/new", http.StatusSeeOther)
		return
	}

	slog.Info("category created", "category_id", newCategory.ID, "slug", newCategory.Slug, "created_by", middleware.GetUserID(r))
	h.renderer.SetFlash(r, "Category created successfully", "success")
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

// EditCategoryForm handles GET /admin/categories/{id} - displays the edit category form.
func (h *TaxonomyHandler) EditCategoryForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid category ID", "error")
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return
	}

	category, ok := h.requireCategoryWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Get all categories for parent selector (excluding self and descendants)
	flatTree := h.buildFilteredCategoryTree(r.Context(), id)
	langInfo := h.loadCategoryLanguageInfo(r.Context(), category)

	data := CategoryFormData{
		Category:         &category,
		AllCategories:    flatTree,
		Errors:           make(map[string]string),
		FormValues:       make(map[string]string),
		IsEdit:           true,
		AllLanguages:     langInfo.AllLanguages,
		Language:         langInfo.CategoryLanguage,
		Translations:     langInfo.Translations,
		MissingLanguages: langInfo.MissingLanguages,
	}

	h.renderer.RenderPage(w, r, "admin/categories_form", render.TemplateData{
		Title: category.Name,
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.categories"), URL: "/admin/categories"},
			{Label: category.Name, URL: fmt.Sprintf("/admin/categories/%d", category.ID), Active: true},
		},
	})
}

// UpdateCategory handles PUT /admin/categories/{id} - updates an existing category.
func (h *TaxonomyHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid category ID", "error")
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return
	}

	existingCategory, ok := h.requireCategoryWithRedirect(w, r, id)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/categories/%d", id), http.StatusSeeOther)
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	description := strings.TrimSpace(r.FormValue("description"))
	parentIDStr := r.FormValue("parent_id")

	// Parse parent ID
	var parentID sql.NullInt64
	if parentIDStr != "" && parentIDStr != "0" {
		if pid, err := strconv.ParseInt(parentIDStr, 10, 64); err == nil {
			parentID = sql.NullInt64{Int64: pid, Valid: true}
		}
	}

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":        name,
		"slug":        slug,
		"description": description,
		"parent_id":   parentIDStr,
	}

	slug = autoGenerateSlug(name, slug, formValues)

	// Validate
	validationErrors := make(map[string]string)
	if errMsg := validateTaxonomyName(name); errMsg != "" {
		validationErrors["name"] = errMsg
	}
	if errMsg := h.validateCategorySlugUpdate(r.Context(), slug, existingCategory.Slug, id); errMsg != "" {
		validationErrors["slug"] = errMsg
	}

	// Prevent setting parent to self or descendant
	if parentID.Valid {
		if parentID.Int64 == id {
			validationErrors["parent_id"] = "Category cannot be its own parent"
		} else {
			// Check if parent is a descendant
			descendantIDs, _ := h.queries.GetDescendantIDs(r.Context(), sql.NullInt64{Int64: id, Valid: true})
			for _, did := range descendantIDs {
				if did == parentID.Int64 {
					validationErrors["parent_id"] = "Cannot set a descendant as parent (circular reference)"
					break
				}
			}
		}
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		flatTree := h.buildFilteredCategoryTree(r.Context(), id)
		langInfo := h.loadCategoryLanguageInfo(r.Context(), existingCategory)

		data := CategoryFormData{
			Category:      &existingCategory,
			AllCategories: flatTree,
			Errors:        validationErrors,
			FormValues:    formValues,
			IsEdit:        true,
			AllLanguages:  langInfo.AllLanguages,
			Language:      langInfo.CategoryLanguage,
		}

		h.renderer.RenderPage(w, r, "admin/categories_form", render.TemplateData{
			Title: existingCategory.Name,
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "nav.categories"), URL: "/admin/categories"},
				{Label: existingCategory.Name, URL: fmt.Sprintf("/admin/categories/%d", id), Active: true},
			},
		})
		return
	}

	// Update category (keep existing language_id - language cannot be changed after creation)
	now := time.Now()
	updatedCategory, err := h.queries.UpdateCategory(r.Context(), store.UpdateCategoryParams{
		ID:          id,
		Name:        name,
		Slug:        slug,
		Description: sql.NullString{String: description, Valid: description != ""},
		ParentID:    parentID,
		Position:    existingCategory.Position,
		LanguageID:  existingCategory.LanguageID,
		UpdatedAt:   now,
	})
	if err != nil {
		slog.Error("failed to update category", "error", err, "category_id", id)
		h.renderer.SetFlash(r, "Error updating category", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/categories/%d", id), http.StatusSeeOther)
		return
	}

	slog.Info("category updated", "category_id", updatedCategory.ID, "slug", updatedCategory.Slug, "updated_by", middleware.GetUserID(r))
	h.renderer.SetFlash(r, "Category updated successfully", "success")
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

// DeleteCategory handles DELETE /admin/categories/{id} - deletes a category.
func (h *TaxonomyHandler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid category ID", http.StatusBadRequest)
		return
	}

	category, ok := h.requireCategoryWithError(w, r, id)
	if !ok {
		return
	}

	// Delete the category (page_categories cascade, children get parent_id = NULL)
	err = h.queries.DeleteCategory(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete category", "error", err, "category_id", id)
		http.Error(w, "Error deleting category", http.StatusInternalServerError)
		return
	}

	slog.Info("category deleted", "category_id", id, "slug", category.Slug, "deleted_by", middleware.GetUserID(r))

	// For HTMX requests, return empty response (row removed)
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For regular requests, redirect
	h.renderer.SetFlash(r, "Category deleted successfully", "success")
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

// TranslateCategory handles POST /admin/categories/{id}/translate/{langCode} - creates a translation.
func (h *TaxonomyHandler) TranslateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid category ID", "error")
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return
	}

	// Get language code from URL
	langCode := chi.URLParam(r, "langCode")
	if langCode == "" {
		h.renderer.SetFlash(r, "Language code is required", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/categories/%d", id), http.StatusSeeOther)
		return
	}

	sourceCategory, ok := h.requireCategoryWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Get target language
	targetLang, err := h.queries.GetLanguageByCode(r.Context(), langCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderer.SetFlash(r, "Language not found", "error")
		} else {
			slog.Error("failed to get language", "error", err, "lang_code", langCode)
			h.renderer.SetFlash(r, "Error loading language", "error")
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/categories/%d", id), http.StatusSeeOther)
		return
	}

	// Check if translation already exists
	existingTranslation, err := h.queries.GetTranslation(r.Context(), store.GetTranslationParams{
		EntityType: model.EntityTypeCategory,
		EntityID:   id,
		LanguageID: targetLang.ID,
	})
	if err == nil && existingTranslation.ID > 0 {
		// Translation already exists, redirect to it
		h.renderer.SetFlash(r, "Translation already exists", "info")
		http.Redirect(w, r, fmt.Sprintf("/admin/categories/%d", existingTranslation.TranslationID), http.StatusSeeOther)
		return
	}

	// Generate a unique slug for the translated category
	baseSlug := sourceCategory.Slug + "-" + langCode
	translatedSlug := baseSlug
	counter := 1
	for {
		exists, err := h.queries.CategorySlugExists(r.Context(), translatedSlug)
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			h.renderer.SetFlash(r, "Error creating translation", "error")
			http.Redirect(w, r, fmt.Sprintf("/admin/categories/%d", id), http.StatusSeeOther)
			return
		}
		if exists == 0 {
			break
		}
		counter++
		translatedSlug = fmt.Sprintf("%s-%d", baseSlug, counter)
	}

	// Create the translated category with same name
	now := time.Now()
	translatedCategory, err := h.queries.CreateCategory(r.Context(), store.CreateCategoryParams{
		Name:        sourceCategory.Name, // Keep same name (user will translate)
		Slug:        translatedSlug,
		Description: sourceCategory.Description,
		ParentID:    sql.NullInt64{}, // No parent by default for translations
		Position:    0,
		LanguageID:  sql.NullInt64{Int64: targetLang.ID, Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		slog.Error("failed to create translated category", "error", err)
		h.renderer.SetFlash(r, "Error creating translation", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/categories/%d", id), http.StatusSeeOther)
		return
	}

	// Create translation link from source to translated category
	_, err = h.queries.CreateTranslation(r.Context(), store.CreateTranslationParams{
		EntityType:    model.EntityTypeCategory,
		EntityID:      id,
		LanguageID:    targetLang.ID,
		TranslationID: translatedCategory.ID,
		CreatedAt:     now,
	})
	if err != nil {
		slog.Error("failed to create translation link", "error", err)
		// Category was created, so we should still redirect to it
	}

	slog.Info("category translation created",
		"source_category_id", id,
		"translated_category_id", translatedCategory.ID,
		"language", langCode,
		"created_by", middleware.GetUserID(r))

	h.renderer.SetFlash(r, fmt.Sprintf("Translation created for %s. Please translate the name.", targetLang.Name), "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/categories/%d", translatedCategory.ID), http.StatusSeeOther)
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// requireTagWithRedirect fetches a tag by ID and redirects with flash on error.
// Returns the tag and true if successful, or false if an error occurred (redirect sent).
func (h *TaxonomyHandler) requireTagWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Tag, bool) {
	tag, err := h.queries.GetTagByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderer.SetFlash(r, "Tag not found", "error")
		} else {
			slog.Error("failed to get tag", "error", err, "tag_id", id)
			h.renderer.SetFlash(r, "Error loading tag", "error")
		}
		http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
		return store.Tag{}, false
	}
	return tag, true
}

// requireTagWithError fetches a tag by ID and returns http.Error on error.
// Returns the tag and true if successful, or false if an error occurred.
func (h *TaxonomyHandler) requireTagWithError(w http.ResponseWriter, r *http.Request, id int64) (store.Tag, bool) {
	tag, err := h.queries.GetTagByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Tag not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get tag", "error", err, "tag_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return store.Tag{}, false
	}
	return tag, true
}

// requireCategoryWithRedirect fetches a category by ID and redirects with flash on error.
// Returns the category and true if successful, or false if an error occurred (redirect sent).
func (h *TaxonomyHandler) requireCategoryWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Category, bool) {
	category, err := h.queries.GetCategoryByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderer.SetFlash(r, "Category not found", "error")
		} else {
			slog.Error("failed to get category", "error", err, "category_id", id)
			h.renderer.SetFlash(r, "Error loading category", "error")
		}
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return store.Category{}, false
	}
	return category, true
}

// requireCategoryWithError fetches a category by ID and returns http.Error on error.
// Returns the category and true if successful, or false if an error occurred.
func (h *TaxonomyHandler) requireCategoryWithError(w http.ResponseWriter, r *http.Request, id int64) (store.Category, bool) {
	category, err := h.queries.GetCategoryByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Category not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get category", "error", err, "category_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return store.Category{}, false
	}
	return category, true
}

// parseLanguageIDWithDefault parses language ID from string and falls back to default language.
func (h *TaxonomyHandler) parseLanguageIDWithDefault(ctx context.Context, languageIDStr string) sql.NullInt64 {
	var languageID sql.NullInt64
	if languageIDStr != "" && languageIDStr != "0" {
		if lid, err := strconv.ParseInt(languageIDStr, 10, 64); err == nil {
			languageID = sql.NullInt64{Int64: lid, Valid: true}
		}
	}

	// If no language specified, use default
	if !languageID.Valid {
		defaultLang, err := h.queries.GetDefaultLanguage(ctx)
		if err == nil {
			languageID = sql.NullInt64{Int64: defaultLang.ID, Valid: true}
		}
	}

	return languageID
}

// autoGenerateSlug generates a slug from name if slug is empty, updating formValues.
func autoGenerateSlug(name, slug string, formValues map[string]string) string {
	if slug == "" {
		slug = util.Slugify(name)
		formValues["slug"] = slug
	}
	return slug
}

// validateTaxonomyName validates a taxonomy name (tag or category).
func validateTaxonomyName(name string) string {
	if name == "" {
		return "Name is required"
	}
	if len(name) < 2 {
		return "Name must be at least 2 characters"
	}
	return ""
}

// slugExistsFunc is a function type for checking if a slug exists.
type slugExistsFunc func() (int64, error)

// validateSlugWithChecker validates a slug using a custom existence checker.
func validateSlugWithChecker(slug string, checkExists slugExistsFunc) string {
	if slug == "" {
		return "Slug is required"
	}
	if !util.IsValidSlug(slug) {
		return "Invalid slug format (use lowercase letters, numbers, and hyphens)"
	}
	exists, err := checkExists()
	if err != nil {
		slog.Error("database error checking slug", "error", err)
		return "Error checking slug"
	}
	if exists != 0 {
		return "Slug already exists"
	}
	return ""
}

// validateTagSlugCreate validates a tag slug for creation.
func (h *TaxonomyHandler) validateTagSlugCreate(ctx context.Context, slug string) string {
	return validateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.TagSlugExists(ctx, slug)
	})
}

// validateTagSlugUpdate validates a tag slug for update.
func (h *TaxonomyHandler) validateTagSlugUpdate(ctx context.Context, slug, currentSlug string, tagID int64) string {
	if slug == currentSlug {
		return "" // No change, no validation needed
	}
	return validateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.TagSlugExistsExcluding(ctx, store.TagSlugExistsExcludingParams{
			Slug: slug,
			ID:   tagID,
		})
	})
}

// validateCategorySlugCreate validates a category slug for creation.
func (h *TaxonomyHandler) validateCategorySlugCreate(ctx context.Context, slug string) string {
	return validateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.CategorySlugExists(ctx, slug)
	})
}

// validateCategorySlugUpdate validates a category slug for update.
func (h *TaxonomyHandler) validateCategorySlugUpdate(ctx context.Context, slug, currentSlug string, categoryID int64) string {
	if slug == currentSlug {
		return "" // No change, no validation needed
	}
	return validateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.CategorySlugExistsExcluding(ctx, store.CategorySlugExistsExcludingParams{
			Slug: slug,
			ID:   categoryID,
		})
	})
}

// translationBaseInfo holds common language info for taxonomy entities.
type translationBaseInfo struct {
	EntityLanguage       *store.Language
	AllLanguages         []store.Language
	TranslatedIDs        map[int64]bool
	TranslationLinks     []store.GetTranslationsForEntityRow
	TranslationLanguages map[int64]store.Language // maps LanguageID to Language for each translation link
	MissingLanguages     []store.Language
}

// loadTranslationBaseInfo loads common language and translation info for any entity.
func (h *TaxonomyHandler) loadTranslationBaseInfo(ctx context.Context, entityType string, entityID int64, languageID sql.NullInt64) translationBaseInfo {
	info := translationBaseInfo{
		TranslatedIDs:        make(map[int64]bool),
		TranslationLanguages: make(map[int64]store.Language),
	}

	// Load all active languages
	allLanguages, err := h.queries.ListActiveLanguages(ctx)
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		allLanguages = []store.Language{}
	}
	info.AllLanguages = allLanguages

	// Build language lookup map
	langByID := make(map[int64]store.Language, len(allLanguages))
	for _, lang := range allLanguages {
		langByID[lang.ID] = lang
	}

	// Load entity's language
	if languageID.Valid {
		if lang, ok := langByID[languageID.Int64]; ok {
			info.EntityLanguage = &lang
			info.TranslatedIDs[lang.ID] = true
		}
	}

	// Load translation links
	translationLinks, err := h.queries.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
		EntityType: entityType,
		EntityID:   entityID,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("failed to get translations", "error", err, "entity_type", entityType, "entity_id", entityID)
	}
	info.TranslationLinks = translationLinks

	// Mark translated language IDs and cache languages
	for _, tl := range translationLinks {
		info.TranslatedIDs[tl.LanguageID] = true
		if lang, ok := langByID[tl.LanguageID]; ok {
			info.TranslationLanguages[tl.LanguageID] = lang
		}
	}

	// Find missing languages
	for _, lang := range allLanguages {
		if !info.TranslatedIDs[lang.ID] {
			info.MissingLanguages = append(info.MissingLanguages, lang)
		}
	}

	return info
}

// tagLanguageInfo holds language and translation info for a tag.
type tagLanguageInfo struct {
	TagLanguage      *store.Language
	AllLanguages     []store.Language
	Translations     []TagTranslationInfo
	MissingLanguages []store.Language
}

// loadTagLanguageInfo loads language and translation info for a tag.
func (h *TaxonomyHandler) loadTagLanguageInfo(ctx context.Context, tag store.Tag) tagLanguageInfo {
	base := h.loadTranslationBaseInfo(ctx, model.EntityTypeTag, tag.ID, tag.LanguageID)

	info := tagLanguageInfo{
		TagLanguage:      base.EntityLanguage,
		AllLanguages:     base.AllLanguages,
		MissingLanguages: base.MissingLanguages,
	}

	// Load translated tags (languages are pre-fetched in base)
	for _, tl := range base.TranslationLinks {
		lang, ok := base.TranslationLanguages[tl.LanguageID]
		if !ok {
			continue
		}
		translatedTag, err := h.queries.GetTagByID(ctx, tl.TranslationID)
		if err == nil {
			info.Translations = append(info.Translations, TagTranslationInfo{
				Language: lang,
				Tag:      translatedTag,
			})
		}
	}

	return info
}

// categoryLanguageInfo holds language and translation info for a category.
type categoryLanguageInfo struct {
	CategoryLanguage *store.Language
	AllLanguages     []store.Language
	Translations     []CategoryTranslationInfo
	MissingLanguages []store.Language
}

// loadCategoryLanguageInfo loads language and translation info for a category.
func (h *TaxonomyHandler) loadCategoryLanguageInfo(ctx context.Context, category store.Category) categoryLanguageInfo {
	base := h.loadTranslationBaseInfo(ctx, model.EntityTypeCategory, category.ID, category.LanguageID)

	info := categoryLanguageInfo{
		CategoryLanguage: base.EntityLanguage,
		AllLanguages:     base.AllLanguages,
		MissingLanguages: base.MissingLanguages,
	}

	// Load translated categories (languages are pre-fetched in base)
	for _, tl := range base.TranslationLinks {
		lang, ok := base.TranslationLanguages[tl.LanguageID]
		if !ok {
			continue
		}
		translatedCategory, err := h.queries.GetCategoryByID(ctx, tl.TranslationID)
		if err == nil {
			info.Translations = append(info.Translations, CategoryTranslationInfo{
				Language: lang,
				Category: translatedCategory,
			})
		}
	}

	return info
}

// SearchCategories handles GET /admin/categories/search - AJAX search.
func (h *TaxonomyHandler) SearchCategories(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var categories []store.Category
	var err error

	if query == "" {
		// Return all categories if no query
		categories, err = h.queries.ListCategories(r.Context())
	} else {
		// Search categories by name
		categories, err = h.queries.SearchCategories(r.Context(), sql.NullString{String: query, Valid: true})
	}

	if err != nil {
		slog.Error("failed to search categories", "error", err, "query", query)
		writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(categories); err != nil {
		slog.Error("failed to encode categories response", "error", err)
	}
}
