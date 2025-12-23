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

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
	"ocms-go/internal/util"
)

// MenusHandler handles menu management routes.
type MenusHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewMenusHandler creates a new MenusHandler.
func NewMenusHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *MenusHandler {
	return &MenusHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// MenusListData holds data for the menus list template.
type MenusListData struct {
	Menus     []store.ListMenusWithLanguageRow
	Languages []store.Language
}

// List handles GET /admin/menus - displays a list of menus.
func (h *MenusHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	menus, err := h.queries.ListMenusWithLanguage(r.Context())
	if err != nil {
		slog.Error("failed to list menus", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get languages for filter/display
	languages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

	data := MenusListData{
		Menus:     menus,
		Languages: languages,
	}

	h.renderer.RenderPage(w, r, "admin/menus_list", render.TemplateData{
		Title: i18n.T(lang, "nav.menus"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.menus"), URL: redirectAdminMenus, Active: true},
		},
	})
}

// MenuItemNode represents a menu item with children for tree display.
type MenuItemNode struct {
	Item     store.MenuItem
	Children []MenuItemNode
	PageSlug string // If linked to a page
}

// MenuFormData holds data for the menu builder template.
type MenuFormData struct {
	Menu       *store.Menu
	Items      []MenuItemNode
	Pages      []store.Page // Available pages to add
	Targets    []string
	Languages  []store.Language
	Errors     map[string]string
	FormValues map[string]string
	IsEdit     bool
}

// buildMenuTree builds a nested tree from flat menu items.
func buildMenuTree(items []store.ListMenuItemsWithPageRow, parentID sql.NullInt64) []MenuItemNode {
	var nodes []MenuItemNode

	for _, item := range items {
		// Check if this item's parent matches the requested parent
		if item.ParentID.Valid == parentID.Valid &&
			(!item.ParentID.Valid || item.ParentID.Int64 == parentID.Int64) {
			node := MenuItemNode{
				Item: store.MenuItem{
					ID:        item.ID,
					MenuID:    item.MenuID,
					ParentID:  item.ParentID,
					Title:     item.Title,
					Url:       item.Url,
					Target:    item.Target,
					PageID:    item.PageID,
					Position:  item.Position,
					CssClass:  item.CssClass,
					IsActive:  item.IsActive,
					CreatedAt: item.CreatedAt,
					UpdatedAt: item.UpdatedAt,
				},
				PageSlug: item.PageSlug.String,
				Children: buildMenuTree(items, util.NullInt64FromValue(item.ID)),
			}
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// NewForm handles GET /admin/menus/new - displays the new menu form.
func (h *MenusHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get languages for selector
	languages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

	// Get default language for pre-selection
	defaultLang, err := h.queries.GetDefaultLanguage(r.Context())
	if err != nil {
		slog.Error("failed to get default language", "error", err)
	}

	formValues := make(map[string]string)
	if defaultLang.ID > 0 {
		formValues["language_id"] = fmt.Sprintf("%d", defaultLang.ID)
	}

	data := MenuFormData{
		Targets:    model.ValidTargets,
		Languages:  languages,
		Errors:     make(map[string]string),
		FormValues: formValues,
		IsEdit:     false,
	}

	h.renderer.RenderPage(w, r, "admin/menus_form", render.TemplateData{
		Title: i18n.T(lang, "menus.new"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.menus"), URL: redirectAdminMenus},
			{Label: i18n.T(lang, "menus.new"), URL: redirectAdminMenusNew, Active: true},
		},
	})
}

// Create handles POST /admin/menus - creates a new menu.
func (h *MenusHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminMenusNew) {
		return
	}

	input := parseMenuFormInput(r)

	// Validate slug
	if errMsg := h.validateMenuSlugCreate(r.Context(), input.Slug, input.LanguageID); errMsg != "" {
		input.Errors["slug"] = errMsg
	}

	if len(input.Errors) > 0 {
		// Get languages for re-render
		languages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

		data := MenuFormData{
			Targets:    model.ValidTargets,
			Languages:  languages,
			Errors:     input.Errors,
			FormValues: input.FormValues,
			IsEdit:     false,
		}

		h.renderer.RenderPage(w, r, "admin/menus_form", render.TemplateData{
			Title: i18n.T(lang, "menus.new"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.menus"), URL: redirectAdminMenus},
				{Label: i18n.T(lang, "menus.new"), URL: redirectAdminMenusNew, Active: true},
			},
		})
		return
	}

	now := time.Now()
	menu, err := h.queries.CreateMenu(r.Context(), store.CreateMenuParams{
		Name:       input.Name,
		Slug:       input.Slug,
		LanguageID: input.LanguageID,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to create menu", "error", err)
		flashError(w, r, h.renderer, redirectAdminMenusNew, "Error creating menu")
		return
	}

	slog.Info("menu created", "menu_id", menu.ID, "slug", menu.Slug)
	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminMenusID, menu.ID), "Menu created successfully")
}

// EditForm handles GET /admin/menus/{id} - displays the menu builder.
func (h *MenusHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminMenus, "Invalid menu ID")
		return
	}

	menu, ok := h.requireMenuWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Get menu items with page info
	items, err := h.queries.ListMenuItemsWithPage(r.Context(), id)
	if err != nil {
		slog.Error("failed to list menu items", "error", err, "menu_id", id)
		items = []store.ListMenuItemsWithPageRow{}
	}

	// Build tree structure
	tree := buildMenuTree(items, sql.NullInt64{Valid: false})

	// Get available pages
	pages, err := h.queries.ListPages(r.Context(), store.ListPagesParams{
		Limit:  1000,
		Offset: 0,
	})
	if err != nil {
		slog.Error("failed to list pages", "error", err)
		pages = []store.Page{}
	}

	// Get languages for selector
	languages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

	data := MenuFormData{
		Menu:       &menu,
		Items:      tree,
		Pages:      pages,
		Targets:    model.ValidTargets,
		Languages:  languages,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     true,
	}

	h.renderer.RenderPage(w, r, "admin/menus_form", render.TemplateData{
		Title: fmt.Sprintf("Edit Menu - %s", menu.Name),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.menus"), URL: redirectAdminMenus},
			{Label: menu.Name, URL: fmt.Sprintf(redirectAdminMenusID, menu.ID), Active: true},
		},
	})
}

// Update handles PUT /admin/menus/{id} - updates a menu.
func (h *MenusHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminMenus, "Invalid menu ID")
		return
	}

	menu, ok := h.requireMenuWithRedirect(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminMenusID, id)) {
		return
	}

	input := parseMenuFormInput(r)

	// Validate slug
	if errMsg := h.validateMenuSlugUpdate(r.Context(), input.Slug, input.LanguageID, menu.Slug, menu.LanguageID, id); errMsg != "" {
		input.Errors["slug"] = errMsg
	}

	if len(input.Errors) > 0 {
		// Get menu items for re-render
		items, _ := h.queries.ListMenuItemsWithPage(r.Context(), id)
		tree := buildMenuTree(items, sql.NullInt64{Valid: false})
		pages, _ := h.queries.ListPages(r.Context(), store.ListPagesParams{Limit: 1000, Offset: 0})
		languages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

		data := MenuFormData{
			Menu:       &menu,
			Items:      tree,
			Pages:      pages,
			Targets:    model.ValidTargets,
			Languages:  languages,
			Errors:     input.Errors,
			FormValues: input.FormValues,
			IsEdit:     true,
		}

		h.renderer.RenderPage(w, r, "admin/menus_form", render.TemplateData{
			Title: fmt.Sprintf("Edit Menu - %s", menu.Name),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.menus"), URL: redirectAdminMenus},
				{Label: menu.Name, URL: fmt.Sprintf(redirectAdminMenusID, menu.ID), Active: true},
			},
		})
		return
	}

	now := time.Now()
	_, err = h.queries.UpdateMenu(r.Context(), store.UpdateMenuParams{
		ID:         id,
		Name:       input.Name,
		Slug:       input.Slug,
		LanguageID: input.LanguageID,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to update menu", "error", err, "menu_id", id)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminMenusID, id), "Error updating menu")
		return
	}

	// Invalidate menu cache (both old and new slug in case slug changed)
	h.renderer.InvalidateMenuCache(menu.Slug)
	if input.Slug != menu.Slug {
		h.renderer.InvalidateMenuCache(input.Slug)
	}

	slog.Info("menu updated", "menu_id", id, "updated_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminMenusID, id), "Menu updated successfully")
}

// Delete handles DELETE /admin/menus/{id} - deletes a menu.
func (h *MenusHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid menu ID", http.StatusBadRequest)
		return
	}

	menu, ok := h.requireMenuWithError(w, r, id)
	if !ok {
		return
	}

	// Prevent deleting default menus
	if menu.Slug == model.MenuMain || menu.Slug == model.MenuFooter {
		http.Error(w, "Cannot delete default menus", http.StatusForbidden)
		return
	}

	err = h.queries.DeleteMenu(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete menu", "error", err, "menu_id", id)
		http.Error(w, "Error deleting menu", http.StatusInternalServerError)
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	slog.Info("menu deleted", "menu_id", id, "slug", menu.Slug, "deleted_by", middleware.GetUserID(r))

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminMenus, "Menu deleted successfully")
}

// AddItemRequest represents the JSON request for adding a menu item.
type AddItemRequest struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Target   string `json:"target"`
	PageID   *int64 `json:"page_id"`
	ParentID *int64 `json:"parent_id"`
	CSSClass string `json:"css_class"`
}

// AddItem handles POST /admin/menus/{id}/items - adds a menu item.
func (h *MenusHandler) AddItem(w http.ResponseWriter, r *http.Request) {
	menuID, err := ParseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid menu ID")
		return
	}

	menu, ok := h.requireMenuWithJSONError(w, r, menuID)
	if !ok {
		return
	}

	var req AddItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	validated, ok := validateMenuItemInput(w, menuItemInput{
		Title:    req.Title,
		Target:   req.Target,
		PageID:   req.PageID,
		URL:      req.URL,
		CSSClass: req.CSSClass,
	})
	if !ok {
		return
	}

	// Get max position for this parent
	parentID := util.NullInt64FromPtr(req.ParentID)

	maxPos := h.getMaxMenuItemPosition(r, menuID, parentID)

	now := time.Now()
	item, err := h.queries.CreateMenuItem(r.Context(), store.CreateMenuItemParams{
		MenuID:    menuID,
		ParentID:  parentID,
		Title:     req.Title,
		Url:       validated.URL,
		Target:    util.NullStringFromValue(validated.Target),
		PageID:    validated.PageID,
		Position:  maxPos + 1,
		CssClass:  validated.CSSClass,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create menu item", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Error creating menu item")
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	writeJSONSuccess(w, map[string]any{"item": item})
}

// UpdateItemRequest represents the JSON request for updating a menu item.
type UpdateItemRequest struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Target   string `json:"target"`
	PageID   *int64 `json:"page_id"`
	CSSClass string `json:"css_class"`
	IsActive bool   `json:"is_active"`
}

// UpdateItem handles PUT /admin/menus/{id}/items/{itemId} - updates a menu item.
func (h *MenusHandler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	menu, item, ok := h.requireMenuAndItemForJSON(w, r)
	if !ok {
		return
	}

	var req UpdateItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	validated, ok := validateMenuItemInput(w, menuItemInput{
		Title:    req.Title,
		Target:   req.Target,
		PageID:   req.PageID,
		URL:      req.URL,
		CSSClass: req.CSSClass,
	})
	if !ok {
		return
	}

	now := time.Now()
	updatedItem, err := h.queries.UpdateMenuItem(r.Context(), store.UpdateMenuItemParams{
		ID:        item.ID,
		ParentID:  item.ParentID, // Keep existing parent
		Title:     req.Title,
		Url:       validated.URL,
		Target:    util.NullStringFromValue(validated.Target),
		PageID:    validated.PageID,
		Position:  item.Position, // Keep existing position
		CssClass:  validated.CSSClass,
		IsActive:  req.IsActive,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to update menu item", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Error updating menu item")
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	writeJSONSuccess(w, map[string]any{"item": updatedItem})
}

// DeleteItem handles DELETE /admin/menus/{id}/items/{itemId} - deletes a menu item.
func (h *MenusHandler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	menu, item, ok := h.requireMenuAndItemForJSON(w, r)
	if !ok {
		return
	}

	if err := h.queries.DeleteMenuItem(r.Context(), item.ID); err != nil {
		slog.Error("failed to delete menu item", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Error deleting menu item")
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	writeJSONSuccess(w, nil)
}

// ReorderItem represents an item in the reorder request.
type ReorderItem struct {
	ID       int64         `json:"id"`
	ParentID *int64        `json:"parent_id"`
	Children []ReorderItem `json:"children"`
}

// ReorderRequest represents the JSON request for reordering menu items.
type ReorderRequest struct {
	Items []ReorderItem `json:"items"`
}

// Reorder handles POST /admin/menus/{id}/reorder - reorders menu items.
func (h *MenusHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	menuID, err := ParseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid menu ID")
		return
	}

	menu, ok := h.requireMenuWithJSONError(w, r, menuID)
	if !ok {
		return
	}

	var req ReorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.processReorderItems(r, menuID, req.Items); err != nil {
		slog.Error("failed to reorder menu items", "error", err)
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	writeJSONSuccess(w, nil)
}

// processReorderItems recursively processes and updates menu item positions.
func (h *MenusHandler) processReorderItems(r *http.Request, menuID int64, items []ReorderItem) error {
	now := time.Now()
	pos := int64(0)

	var processItems func(items []ReorderItem, parentID sql.NullInt64, position *int64) error
	processItems = func(items []ReorderItem, parentID sql.NullInt64, position *int64) error {
		for _, item := range items {
			// Verify item belongs to this menu
			existingItem, err := h.queries.GetMenuItemByID(r.Context(), item.ID)
			if err != nil {
				return fmt.Errorf("item %d not found", item.ID)
			}
			if existingItem.MenuID != menuID {
				return fmt.Errorf("item %d does not belong to this menu", item.ID)
			}

			// Update position
			if err = h.queries.UpdateMenuItemPosition(r.Context(), store.UpdateMenuItemPositionParams{
				ID:        item.ID,
				ParentID:  parentID,
				Position:  *position,
				UpdatedAt: now,
			}); err != nil {
				return fmt.Errorf("failed to update item %d: %w", item.ID, err)
			}
			*position++

			// Process children
			if len(item.Children) > 0 {
				childPos := int64(0)
				if err = processItems(item.Children, util.NullInt64FromValue(item.ID), &childPos); err != nil {
					return err
				}
			}
		}
		return nil
	}

	return processItems(items, sql.NullInt64{Valid: false}, &pos)
}

// Helper functions

// requireMenuWithRedirect fetches menu by ID and handles errors with flash messages and redirect.
func (h *MenusHandler) requireMenuWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Menu, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, redirectAdminMenus, "Menu", id,
		func(id int64) (store.Menu, error) { return h.queries.GetMenuByID(r.Context(), id) })
}

// requireMenuWithError fetches menu by ID and handles errors with http.Error.
func (h *MenusHandler) requireMenuWithError(w http.ResponseWriter, r *http.Request, id int64) (store.Menu, bool) {
	return requireEntityWithError(w, "Menu", id,
		func(id int64) (store.Menu, error) { return h.queries.GetMenuByID(r.Context(), id) })
}

// menuFormInput holds parsed and validated menu form input.
type menuFormInput struct {
	Name       string
	Slug       string
	LanguageID sql.NullInt64
	FormValues map[string]string
	Errors     map[string]string
}

// parseMenuFormInput parses and validates menu form input (name, slug, language_id).
func parseMenuFormInput(r *http.Request) menuFormInput {
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	languageIDStr := r.FormValue("language_id")

	formValues := map[string]string{
		"name":        name,
		"slug":        slug,
		"language_id": languageIDStr,
	}

	validationErrors := make(map[string]string)

	// Parse language_id
	var languageID sql.NullInt64
	if languageIDStr != "" {
		langID, err := strconv.ParseInt(languageIDStr, 10, 64)
		if err != nil {
			validationErrors["language_id"] = "Invalid language"
		} else {
			languageID = util.NullInt64FromValue(langID)
		}
	}

	// Validate name
	if name == "" {
		validationErrors["name"] = "Name is required"
	} else if len(name) < 2 {
		validationErrors["name"] = "Name must be at least 2 characters"
	}

	// Generate slug from name if empty
	if slug == "" {
		slug = util.Slugify(name)
		formValues["slug"] = slug
	}

	return menuFormInput{
		Name:       name,
		Slug:       slug,
		LanguageID: languageID,
		FormValues: formValues,
		Errors:     validationErrors,
	}
}

// validateMenuItemTarget validates the target field and returns default if empty.
func validateMenuItemTarget(target string) (string, error) {
	if target == "" {
		return model.TargetSelf, nil
	}
	if !model.IsValidTarget(target) {
		return "", errors.New("invalid target")
	}
	return target, nil
}

// menuItemInput represents common input fields for menu item creation/update.
type menuItemInput struct {
	Title    string
	Target   string
	PageID   *int64
	URL      string
	CSSClass string
}

// menuItemValidated holds validated menu item data ready for database operations.
type menuItemValidated struct {
	Target   string
	PageID   sql.NullInt64
	URL      sql.NullString
	CSSClass sql.NullString
}

// validateMenuItemInput validates common menu item fields and writes JSON error on failure.
// Returns validated data and true on success, or zero value and false on validation error.
func validateMenuItemInput(w http.ResponseWriter, input menuItemInput) (menuItemValidated, bool) {
	if input.Title == "" {
		writeJSONError(w, http.StatusBadRequest, "Title is required")
		return menuItemValidated{}, false
	}

	target, err := validateMenuItemTarget(input.Target)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid target")
		return menuItemValidated{}, false
	}

	return menuItemValidated{
		Target:   target,
		PageID:   util.NullInt64FromPtr(input.PageID),
		URL:      util.NullStringFromValue(input.URL),
		CSSClass: util.NullStringFromValue(input.CSSClass),
	}, true
}

// requireMenuWithJSONError fetches menu by ID and handles errors with JSON response.
func (h *MenusHandler) requireMenuWithJSONError(w http.ResponseWriter, r *http.Request, id int64) (store.Menu, bool) {
	return requireEntityWithJSONError(w, "Menu", id,
		func(id int64) (store.Menu, error) { return h.queries.GetMenuByID(r.Context(), id) })
}

// requireMenuItemWithJSONError fetches menu item by ID and handles errors with JSON response.
func (h *MenusHandler) requireMenuItemWithJSONError(w http.ResponseWriter, r *http.Request, id int64) (store.MenuItem, bool) {
	return requireEntityWithJSONError(w, "Menu item", id,
		func(id int64) (store.MenuItem, error) { return h.queries.GetMenuItemByID(r.Context(), id) })
}

// verifyItemBelongsToMenuJSON checks if menu item belongs to the specified menu.
// Returns true if valid, false if not (response already written).
func verifyItemBelongsToMenuJSON(w http.ResponseWriter, item store.MenuItem, menuID int64) bool {
	if item.MenuID != menuID {
		writeJSONError(w, http.StatusBadRequest, "Item does not belong to this menu")
		return false
	}
	return true
}

// requireMenuAndItemForJSON parses menu and item IDs from URL, validates ownership,
// and returns both entities. Used by UpdateItem and DeleteItem handlers.
func (h *MenusHandler) requireMenuAndItemForJSON(w http.ResponseWriter, r *http.Request) (store.Menu, store.MenuItem, bool) {
	menuID, err := ParseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid menu ID")
		return store.Menu{}, store.MenuItem{}, false
	}

	itemID, err := ParseURLParamInt64(r, "itemId")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid item ID")
		return store.Menu{}, store.MenuItem{}, false
	}

	item, ok := h.requireMenuItemWithJSONError(w, r, itemID)
	if !ok {
		return store.Menu{}, store.MenuItem{}, false
	}

	if !verifyItemBelongsToMenuJSON(w, item, menuID) {
		return store.Menu{}, store.MenuItem{}, false
	}

	menu, ok := h.requireMenuWithJSONError(w, r, menuID)
	if !ok {
		return store.Menu{}, store.MenuItem{}, false
	}

	return menu, item, true
}

// getMaxMenuItemPosition returns the max position for menu items under a parent.
func (h *MenusHandler) getMaxMenuItemPosition(r *http.Request, menuID int64, parentID sql.NullInt64) int64 {
	maxPosResult, err := h.queries.GetMaxMenuItemPosition(r.Context(), store.GetMaxMenuItemPositionParams{
		MenuID:   menuID,
		ParentID: parentID,
	})
	if err != nil {
		slog.Error("failed to get max position", "error", err)
		return -1
	}
	if maxPosResult != nil {
		if v, ok := maxPosResult.(int64); ok {
			return v
		}
	}
	return -1
}

// validateMenuSlugCreate validates a menu slug for creation within a language.
func (h *MenusHandler) validateMenuSlugCreate(ctx context.Context, slug string, languageID sql.NullInt64) string {
	if !languageID.Valid {
		// No language specified, use basic validation without uniqueness check
		if slug == "" {
			return "Slug is required"
		}
		if !util.IsValidSlug(slug) {
			return "Invalid slug format"
		}
		return ""
	}
	return ValidateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.MenuSlugExistsForLanguage(ctx, store.MenuSlugExistsForLanguageParams{
			Slug:       slug,
			LanguageID: languageID,
		})
	})
}

// validateMenuSlugUpdate validates a menu slug for update within a language.
// Only validates if slug or language has changed.
func (h *MenusHandler) validateMenuSlugUpdate(ctx context.Context, slug string, languageID sql.NullInt64, currentSlug string, currentLanguageID sql.NullInt64, menuID int64) string {
	// If neither slug nor language changed, no validation needed
	if slug == currentSlug && languageID == currentLanguageID {
		return ""
	}
	return ValidateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.MenuSlugExistsForLanguageExcluding(ctx, store.MenuSlugExistsForLanguageExcludingParams{
			Slug:       slug,
			LanguageID: languageID,
			ID:         menuID,
		})
	})
}
