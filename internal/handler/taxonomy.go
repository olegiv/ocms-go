// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// TagsPerPage is the number of tags to display per page.
const TagsPerPage = 20

// TaxonomyHandler handles tag and category management routes.
type TaxonomyHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	eventService   *service.EventService
}

// NewTaxonomyHandler creates a new TaxonomyHandler.
func NewTaxonomyHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *TaxonomyHandler {
	return &TaxonomyHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		eventService:   service.NewEventService(db),
	}
}

// TagsListData holds data for the tags list template.
type TagsListData struct {
	Tags       []store.GetTagUsageCountsRow
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
		Tags:       tags,
		TotalCount: totalCount,
		Pagination: BuildAdminPagination(page, int(totalCount), TagsPerPage, redirectAdminTags, r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/tags_list", render.TemplateData{
		Title: i18n.T(lang, "nav.tags"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.tags"), URL: redirectAdminTags, Active: true},
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
	allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

	// Get default language for preselection
	defaultLanguage := FindDefaultLanguage(allLanguages)

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
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.tags"), URL: redirectAdminTags},
			{Label: i18n.T(lang, "tags.new"), URL: redirectAdminTagsNew, Active: true},
		},
	})
}

// CreateTag handles POST /admin/tags - creates a new tag.
func (h *TaxonomyHandler) CreateTag(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminTagsNew) {
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	languageCode := strings.TrimSpace(r.FormValue("language_code"))

	languageCode = h.parseLanguageCodeWithDefault(r.Context(), languageCode)

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":          name,
		"slug":          slug,
		"language_code": languageCode,
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
		allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)
		var currentLanguage *store.Language
		if languageCode != "" {
			langObj, err := h.queries.GetLanguageByCode(r.Context(), languageCode)
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
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.tags"), URL: redirectAdminTags},
				{Label: i18n.T(lang, "tags.new"), URL: redirectAdminTagsNew, Active: true},
			},
		})
		return
	}

	// Create tag
	now := time.Now()
	newTag, err := h.queries.CreateTag(r.Context(), store.CreateTagParams{
		Name:         name,
		Slug:         slug,
		LanguageCode: languageCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to create tag", "error", err)
		flashError(w, r, h.renderer, redirectAdminTagsNew, "Error creating tag")
		return
	}

	slog.Info("tag created", "tag_id", newTag.ID, "slug", newTag.Slug, "created_by", middleware.GetUserID(r))
	_ = h.eventService.LogTagEvent(r.Context(), model.EventLevelInfo, "Tag created", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"tag_id": newTag.ID, "name": newTag.Name, "slug": newTag.Slug})
	flashSuccess(w, r, h.renderer, redirectAdminTags, "Tag created successfully")
}

// EditTagForm handles GET /admin/tags/{id} - displays the edit tag form.
func (h *TaxonomyHandler) EditTagForm(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminTags, "Invalid tag ID")
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
		Language:         langInfo.EntityLanguage,
		Translations:     langInfo.Translations,
		MissingLanguages: langInfo.MissingLanguages,
	}

	renderEntityEditPage(w, r, h.renderer, "admin/tags_form",
		tag.Name, data, lang,
		"nav.tags", redirectAdminTags,
		tag.Name, fmt.Sprintf(redirectAdminTagsID, tag.ID))
}

// UpdateTag handles PUT /admin/tags/{id} - updates an existing tag.
func (h *TaxonomyHandler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminTags, "Invalid tag ID")
		return
	}

	existingTag, ok := h.requireTagWithRedirect(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminTagsID, id)) {
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
			Language:     langInfo.EntityLanguage,
		}

		h.renderer.RenderPage(w, r, "admin/tags_form", render.TemplateData{
			Title: existingTag.Name,
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.tags"), URL: redirectAdminTags},
				{Label: existingTag.Name, URL: fmt.Sprintf(redirectAdminTagsID, id), Active: true},
			},
		})
		return
	}

	// Update tag (keep existing language_code - language cannot be changed after creation)
	now := time.Now()
	updatedTag, err := h.queries.UpdateTag(r.Context(), store.UpdateTagParams{
		ID:           id,
		Name:         name,
		Slug:         slug,
		LanguageCode: existingTag.LanguageCode,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to update tag", "error", err, "tag_id", id)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminTagsID, id), "Error updating tag")
		return
	}

	slog.Info("tag updated", "tag_id", updatedTag.ID, "slug", updatedTag.Slug, "updated_by", middleware.GetUserID(r))
	_ = h.eventService.LogTagEvent(r.Context(), model.EventLevelInfo, "Tag updated", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"tag_id": updatedTag.ID, "name": updatedTag.Name, "slug": updatedTag.Slug})
	flashSuccess(w, r, h.renderer, redirectAdminTags, "Tag updated successfully")
}

// DeleteTag handles DELETE /admin/tags/{id} - deletes a tag.
func (h *TaxonomyHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		http.Error(w, middleware.DemoModeMessageDetailed(middleware.RestrictionDeleteTag), http.StatusForbidden)
		return
	}

	params := deleteEntityParams[store.Tag]{EntityName: "tag", IDField: "tag_id", RedirectURL: redirectAdminTags, SuccessMessage: "Tag deleted successfully"}
	params.RequireFn = func(id int64) (store.Tag, bool) { return h.requireTagWithError(w, r, id) }
	params.DeleteFn = h.queries.DeleteTag
	params.GetSlug = func(t store.Tag) string { return t.Slug }
	handleDeleteEntity(w, r, h.renderer, params)
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
		flashError(w, r, h.renderer, redirectAdminTags, "Invalid tag ID")
		return
	}

	redirectURL := fmt.Sprintf(redirectAdminTagsID, id)
	sourceTag, ok := h.requireTagWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Validate language, check for existing translation, and generate unique slug
	setup, ok := h.setupTranslation(w, r, model.EntityTypeTag, id, sourceTag.Slug, redirectURL, func(slug string) (int64, error) {
		return h.queries.TagSlugExists(r.Context(), slug)
	})
	if !ok {
		return
	}

	// Create the translated tag with same name
	translatedTag, err := h.queries.CreateTag(r.Context(), store.CreateTagParams{
		Name:         sourceTag.Name, // Keep same name (user will translate)
		Slug:         setup.TranslatedSlug,
		LanguageCode: setup.TargetContext.TargetLang.Code,
		CreatedAt:    setup.Now,
		UpdatedAt:  setup.Now,
	})
	if err != nil {
		slog.Error("failed to create translated tag", "error", err)
		flashError(w, r, h.renderer, redirectURL, "Error creating translation")
		return
	}

	// Create translation link from source to translated tag
	_, err = h.queries.CreateTranslation(r.Context(), store.CreateTranslationParams{
		EntityType:    model.EntityTypeTag,
		EntityID:      id,
		LanguageID:    setup.TargetContext.TargetLang.ID,
		TranslationID: translatedTag.ID,
		CreatedAt:     setup.Now,
	})
	if err != nil {
		slog.Error("failed to create translation link", "error", err)
		// Tag was created, so we should still redirect to it
	}

	slog.Info("tag translation created",
		"source_tag_id", id,
		"translated_tag_id", translatedTag.ID,
		"language", setup.TargetContext.TargetLang.Code,
		"created_by", middleware.GetUserID(r))

	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminTagsID, translatedTag.ID), fmt.Sprintf("Translation created for %s. Please translate the name.", setup.TargetContext.TargetLang.Name))
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
	descendantIDs, _ := h.queries.GetDescendantIDs(ctx, util.NullInt64FromValue(excludeID))
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
func buildCategoryTreeWithUsage(categories []store.GetCategoryUsageCountsRow, parentID *int64, depth int) []CategoryTreeNode {
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
				ID:           cat.ID,
				Name:         cat.Name,
				Slug:         cat.Slug,
				Description:  cat.Description,
				ParentID:     cat.ParentID,
				Position:     cat.Position,
				LanguageCode: cat.LanguageCode,
				CreatedAt:    cat.CreatedAt,
				UpdatedAt:    cat.UpdatedAt,
			}
			node := CategoryTreeNode{
				Category:     storeCat,
				Depth:        depth,
				UsageCount:   cat.UsageCount,
				LanguageCode: cat.LanguageCode,
				LanguageName: "", // Language name not included in new query
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

	// Get all categories with usage counts
	categories, err := h.queries.GetCategoryUsageCounts(r.Context())
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
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.categories"), URL: redirectAdminCategories, Active: true},
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
	allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

	// Get default language for preselection
	defaultLanguage := FindDefaultLanguage(allLanguages)

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
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.categories"), URL: redirectAdminCategories},
			{Label: i18n.T(lang, "categories.new"), URL: redirectAdminCategoriesNew, Active: true},
		},
	})
}

// CreateCategory handles POST /admin/categories - creates a new category.
func (h *TaxonomyHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminCategoriesNew) {
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	description := strings.TrimSpace(r.FormValue("description"))
	parentIDStr := r.FormValue("parent_id")
	languageCode := strings.TrimSpace(r.FormValue("language_code"))

	// Parse parent ID
	parentID := util.ParseNullInt64(parentIDStr)

	languageCode = h.parseLanguageCodeWithDefault(r.Context(), languageCode)

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":          name,
		"slug":          slug,
		"description":   description,
		"parent_id":     parentIDStr,
		"language_code": languageCode,
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
		allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)
		var currentLanguage *store.Language
		if languageCode != "" {
			langObj, err := h.queries.GetLanguageByCode(r.Context(), languageCode)
			if err == nil {
				currentLanguage = &langObj
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
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.categories"), URL: redirectAdminCategories},
				{Label: i18n.T(lang, "categories.new"), URL: redirectAdminCategoriesNew, Active: true},
			},
		})
		return
	}

	// Create category
	now := time.Now()
	newCategory, err := h.queries.CreateCategory(r.Context(), store.CreateCategoryParams{
		Name:         name,
		Slug:         slug,
		Description:  util.NullStringFromValue(description),
		ParentID:     parentID,
		Position:     0,
		LanguageCode: languageCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to create category", "error", err)
		flashError(w, r, h.renderer, redirectAdminCategoriesNew, "Error creating category")
		return
	}

	slog.Info("category created", "category_id", newCategory.ID, "slug", newCategory.Slug, "created_by", middleware.GetUserID(r))
	_ = h.eventService.LogCategoryEvent(r.Context(), model.EventLevelInfo, "Category created", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"category_id": newCategory.ID, "name": newCategory.Name, "slug": newCategory.Slug})
	flashSuccess(w, r, h.renderer, redirectAdminCategories, "Category created successfully")
}

// EditCategoryForm handles GET /admin/categories/{id} - displays the edit category form.
func (h *TaxonomyHandler) EditCategoryForm(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminCategories, "Invalid category ID")
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
		Language:         langInfo.EntityLanguage,
		Translations:     langInfo.Translations,
		MissingLanguages: langInfo.MissingLanguages,
	}

	renderEntityEditPage(w, r, h.renderer, "admin/categories_form",
		category.Name, data, lang,
		"nav.categories", redirectAdminCategories,
		category.Name, fmt.Sprintf(redirectAdminCategoriesID, category.ID))
}

// UpdateCategory handles PUT /admin/categories/{id} - updates an existing category.
func (h *TaxonomyHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminCategories, "Invalid category ID")
		return
	}

	existingCategory, ok := h.requireCategoryWithRedirect(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminCategoriesID, id)) {
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	description := strings.TrimSpace(r.FormValue("description"))
	parentIDStr := r.FormValue("parent_id")

	// Parse parent ID
	parentID := util.ParseNullInt64(parentIDStr)

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
			descendantIDs, _ := h.queries.GetDescendantIDs(r.Context(), util.NullInt64FromValue(id))
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
			Language:      langInfo.EntityLanguage,
		}

		h.renderer.RenderPage(w, r, "admin/categories_form", render.TemplateData{
			Title: existingCategory.Name,
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.categories"), URL: redirectAdminCategories},
				{Label: existingCategory.Name, URL: fmt.Sprintf(redirectAdminCategoriesID, id), Active: true},
			},
		})
		return
	}

	// Update category (keep existing language_code - language cannot be changed after creation)
	now := time.Now()
	updatedCategory, err := h.queries.UpdateCategory(r.Context(), store.UpdateCategoryParams{
		ID:           id,
		Name:         name,
		Slug:         slug,
		Description:  util.NullStringFromValue(description),
		ParentID:     parentID,
		Position:     existingCategory.Position,
		LanguageCode: existingCategory.LanguageCode,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to update category", "error", err, "category_id", id)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminCategoriesID, id), "Error updating category")
		return
	}

	slog.Info("category updated", "category_id", updatedCategory.ID, "slug", updatedCategory.Slug, "updated_by", middleware.GetUserID(r))
	_ = h.eventService.LogCategoryEvent(r.Context(), model.EventLevelInfo, "Category updated", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"category_id": updatedCategory.ID, "name": updatedCategory.Name, "slug": updatedCategory.Slug})
	flashSuccess(w, r, h.renderer, redirectAdminCategories, "Category updated successfully")
}

// DeleteCategory handles DELETE /admin/categories/{id} - deletes a category.
func (h *TaxonomyHandler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		http.Error(w, middleware.DemoModeMessageDetailed(middleware.RestrictionDeleteCategory), http.StatusForbidden)
		return
	}

	handleDeleteEntity(w, r, h.renderer, deleteEntityParams[store.Category]{
		EntityName:     "category",
		IDField:        "category_id",
		RedirectURL:    redirectAdminCategories,
		SuccessMessage: "Category deleted successfully",
		RequireFn:      func(id int64) (store.Category, bool) { return h.requireCategoryWithError(w, r, id) },
		DeleteFn:       h.queries.DeleteCategory,
		GetSlug:        func(c store.Category) string { return c.Slug },
	})
}

// TranslateCategory handles POST /admin/categories/{id}/translate/{langCode} - creates a translation.
func (h *TaxonomyHandler) TranslateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminCategories, "Invalid category ID")
		return
	}

	redirectURL := fmt.Sprintf(redirectAdminCategoriesID, id)
	sourceCategory, ok := h.requireCategoryWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Validate language, check for existing translation, and generate unique slug
	setup, ok := h.setupTranslation(w, r, model.EntityTypeCategory, id, sourceCategory.Slug, redirectURL, func(slug string) (int64, error) {
		return h.queries.CategorySlugExists(r.Context(), slug)
	})
	if !ok {
		return
	}

	// Create the translated category with same name
	translatedCategory, err := h.queries.CreateCategory(r.Context(), store.CreateCategoryParams{
		Name:         sourceCategory.Name, // Keep same name (user will translate)
		Slug:         setup.TranslatedSlug,
		Description:  sourceCategory.Description,
		ParentID:     sql.NullInt64{}, // No parent by default for translations
		Position:     0,
		LanguageCode: setup.TargetContext.TargetLang.Code,
		CreatedAt:    setup.Now,
		UpdatedAt:    setup.Now,
	})
	if err != nil {
		slog.Error("failed to create translated category", "error", err)
		flashError(w, r, h.renderer, redirectURL, "Error creating translation")
		return
	}

	// Create translation link from source to translated category
	_, err = h.queries.CreateTranslation(r.Context(), store.CreateTranslationParams{
		EntityType:    model.EntityTypeCategory,
		EntityID:      id,
		LanguageID:    setup.TargetContext.TargetLang.ID,
		TranslationID: translatedCategory.ID,
		CreatedAt:     setup.Now,
	})
	if err != nil {
		slog.Error("failed to create translation link", "error", err)
		// Category was created, so we should still redirect to it
	}

	slog.Info("category translation created",
		"source_category_id", id,
		"translated_category_id", translatedCategory.ID,
		"language", setup.TargetContext.TargetLang.Code,
		"created_by", middleware.GetUserID(r))

	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminCategoriesID, translatedCategory.ID), fmt.Sprintf("Translation created for %s. Please translate the name.", setup.TargetContext.TargetLang.Name))
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// requireTagWithRedirect fetches a tag by ID and redirects with flash on error.
func (h *TaxonomyHandler) requireTagWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Tag, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, redirectAdminTags, "Tag", id,
		func(id int64) (store.Tag, error) { return h.queries.GetTagByID(r.Context(), id) })
}

// requireTagWithError fetches a tag by ID and returns http.Error on error.
func (h *TaxonomyHandler) requireTagWithError(w http.ResponseWriter, r *http.Request, id int64) (store.Tag, bool) {
	return requireEntityWithError(w, "Tag", id,
		func(id int64) (store.Tag, error) { return h.queries.GetTagByID(r.Context(), id) })
}

// requireCategoryWithRedirect fetches a category by ID and redirects with flash on error.
func (h *TaxonomyHandler) requireCategoryWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Category, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, redirectAdminCategories, "Category", id,
		func(id int64) (store.Category, error) { return h.queries.GetCategoryByID(r.Context(), id) })
}

// requireCategoryWithError fetches a category by ID and returns http.Error on error.
func (h *TaxonomyHandler) requireCategoryWithError(w http.ResponseWriter, r *http.Request, id int64) (store.Category, bool) {
	return requireEntityWithError(w, "Category", id,
		func(id int64) (store.Category, error) { return h.queries.GetCategoryByID(r.Context(), id) })
}

// parseLanguageCodeWithDefault parses language code from string and falls back to default language.
func (h *TaxonomyHandler) parseLanguageCodeWithDefault(ctx context.Context, languageCode string) string {
	// If no language specified, use default
	if languageCode == "" {
		defaultLang, err := h.queries.GetDefaultLanguage(ctx)
		if err == nil {
			languageCode = defaultLang.Code
		}
	}

	return languageCode
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

// validateTagSlugCreate validates a tag slug for creation.
func (h *TaxonomyHandler) validateTagSlugCreate(ctx context.Context, slug string) string {
	return ValidateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.TagSlugExists(ctx, slug)
	})
}

// validateTagSlugUpdate validates a tag slug for update.
func (h *TaxonomyHandler) validateTagSlugUpdate(ctx context.Context, slug, currentSlug string, tagID int64) string {
	return ValidateSlugForUpdate(slug, currentSlug, func() (int64, error) {
		return h.queries.TagSlugExistsExcluding(ctx, store.TagSlugExistsExcludingParams{
			Slug: slug,
			ID:   tagID,
		})
	})
}

// validateCategorySlugCreate validates a category slug for creation.
func (h *TaxonomyHandler) validateCategorySlugCreate(ctx context.Context, slug string) string {
	return ValidateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.CategorySlugExists(ctx, slug)
	})
}

// validateCategorySlugUpdate validates a category slug for update.
func (h *TaxonomyHandler) validateCategorySlugUpdate(ctx context.Context, slug, currentSlug string, categoryID int64) string {
	return ValidateSlugForUpdate(slug, currentSlug, func() (int64, error) {
		return h.queries.CategorySlugExistsExcluding(ctx, store.CategorySlugExistsExcludingParams{
			Slug: slug,
			ID:   categoryID,
		})
	})
}

// SlugExistsChecker is a function that checks if a slug exists (returns count).
type SlugExistsChecker func(slug string) (int64, error)

// generateUniqueSlug generates a unique slug by appending langCode and incrementing counter if needed.
func generateUniqueSlug(baseSlug, langCode string, slugExists SlugExistsChecker) (string, error) {
	translatedSlug := baseSlug + "-" + langCode
	counter := 1
	for {
		exists, err := slugExists(translatedSlug)
		if err != nil {
			return "", err
		}
		if exists == 0 {
			return translatedSlug, nil
		}
		counter++
		translatedSlug = fmt.Sprintf("%s-%s-%d", baseSlug, langCode, counter)
	}
}

// translationSetupResult holds the result of translation setup validation.
type translationSetupResult struct {
	TargetContext  *translationContext
	TranslatedSlug string
	Now            time.Time
}

// setupTranslation validates langCode, gets target language, and generates unique slug.
// Returns nil, false if validation failed (flash message sent).
func (h *TaxonomyHandler) setupTranslation(
	w http.ResponseWriter,
	r *http.Request,
	entityType string,
	entityID int64,
	sourceSlug string,
	redirectURL string,
	slugChecker SlugExistsChecker,
) (*translationSetupResult, bool) {
	langCode := chi.URLParam(r, "langCode")
	if langCode == "" {
		flashError(w, r, h.renderer, redirectURL, "Language code is required")
		return nil, false
	}

	// Validate language and check for existing translation
	tc, ok := getTargetLanguageForTranslation(w, r, h.queries, h.renderer, langCode, redirectURL, entityType, entityID)
	if !ok {
		return nil, false
	}

	// Generate a unique slug
	translatedSlug, err := generateUniqueSlug(sourceSlug, langCode, slugChecker)
	if err != nil {
		slog.Error("database error checking slug", "error", err)
		flashError(w, r, h.renderer, redirectURL, "Error creating translation")
		return nil, false
	}

	return &translationSetupResult{
		TargetContext:  tc,
		TranslatedSlug: translatedSlug,
		Now:            time.Now(),
	}, true
}

// translationContext holds common data for translation operations.
type translationContext struct {
	TargetLang store.Language
}

// getTargetLanguageForTranslation validates and returns the target language for translation.
// Returns nil, false if an error occurred (flash message sent).
func getTargetLanguageForTranslation(
	w http.ResponseWriter,
	r *http.Request,
	queries *store.Queries,
	renderer *render.Renderer,
	langCode, redirectURL string,
	entityType string,
	entityID int64,
) (*translationContext, bool) {
	targetLang, err := queries.GetLanguageByCode(r.Context(), langCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			flashError(w, r, renderer, redirectURL, "Language not found")
		} else {
			slog.Error("failed to get language", "error", err, "lang_code", langCode)
			flashError(w, r, renderer, redirectURL, "Error loading language")
		}
		return nil, false
	}

	// Check if translation already exists
	existingTranslation, err := queries.GetTranslation(r.Context(), store.GetTranslationParams{
		EntityType: entityType,
		EntityID:   entityID,
		LanguageID: targetLang.ID,
	})
	if err == nil && existingTranslation.ID > 0 {
		flashAndRedirect(w, r, renderer, fmt.Sprintf("/admin/%ss/%d", entityType, existingTranslation.TranslationID), "Translation already exists", "info")
		return nil, false
	}

	return &translationContext{TargetLang: targetLang}, true
}

// tagLanguageInfo is an alias for entityLanguageInfo with TagTranslationInfo.
type tagLanguageInfo = entityLanguageInfo[TagTranslationInfo]

// loadTagLanguageInfo loads language and translation info for a tag.
func (h *TaxonomyHandler) loadTagLanguageInfo(ctx context.Context, tag store.Tag) tagLanguageInfo {
	return loadLanguageInfo(
		ctx, h.queries, model.EntityTypeTag, tag.ID, tag.LanguageCode,
		func(id int64) (store.Tag, error) { return h.queries.GetTagByID(ctx, id) },
		func(lang store.Language, t store.Tag) TagTranslationInfo { return TagTranslationInfo{Language: lang, Tag: t} },
	)
}

// categoryLanguageInfo is an alias for entityLanguageInfo with CategoryTranslationInfo.
type categoryLanguageInfo = entityLanguageInfo[CategoryTranslationInfo]

// loadCategoryLanguageInfo loads language and translation info for a category.
func (h *TaxonomyHandler) loadCategoryLanguageInfo(ctx context.Context, category store.Category) categoryLanguageInfo {
	info := loadLanguageInfo(ctx, h.queries, model.EntityTypeCategory, category.ID, category.LanguageCode,
		func(id int64) (store.Category, error) { return h.queries.GetCategoryByID(ctx, id) },
		func(lang store.Language, c store.Category) CategoryTranslationInfo { return CategoryTranslationInfo{Language: lang, Category: c} })
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
		categories, err = h.queries.SearchCategories(r.Context(), util.NullStringFromValue(query))
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
