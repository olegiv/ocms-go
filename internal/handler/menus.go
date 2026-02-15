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

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// MenusHandler handles menu management routes.
type MenusHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	eventService   *service.EventService
}

// NewMenusHandler creates a new MenusHandler.
func NewMenusHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *MenusHandler {
	return &MenusHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		eventService:   service.NewEventService(db),
	}
}

// List handles GET /admin/menus - displays a list of menus.
func (h *MenusHandler) List(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	menus, err := h.queries.ListMenus(r.Context())
	if err != nil {
		slog.Error("failed to list menus", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	viewData := adminviews.MenusListData{
		Menus: convertMenuListItems(menus),
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "nav.menus"), menusBreadcrumbs(lang))
	renderTempl(w, r, adminviews.MenusListPage(pc, viewData))
}

// MenuItemNode represents a menu item with children for tree display.
type MenuItemNode struct {
	Item     store.MenuItem
	Children []MenuItemNode
	PageSlug string // If linked to a page
}

// menuItemJSON is a JSON-friendly representation of MenuItem.
type menuItemJSON struct {
	ID       int64  `json:"id"`
	MenuID   int64  `json:"menu_id"`
	ParentID *int64 `json:"parent_id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Target   string `json:"target"`
	PageID   *int64 `json:"page_id"`
	Position int64  `json:"position"`
	CssClass string `json:"css_class"`
	IsActive bool   `json:"is_active"`
}

// menuItemNodeJSON is a JSON-friendly representation of MenuItemNode.
type menuItemNodeJSON struct {
	Item     menuItemJSON       `json:"Item"`
	Children []menuItemNodeJSON `json:"Children"`
	PageSlug string             `json:"PageSlug"`
}

// MarshalJSON implements custom JSON marshaling for MenuItemNode.
func (n MenuItemNode) MarshalJSON() ([]byte, error) {
	return json.Marshal(n.toJSON())
}

// toJSON converts MenuItemNode to its JSON-friendly representation.
func (n MenuItemNode) toJSON() menuItemNodeJSON {
	var parentID *int64
	if n.Item.ParentID.Valid {
		parentID = &n.Item.ParentID.Int64
	}
	var pageID *int64
	if n.Item.PageID.Valid {
		pageID = &n.Item.PageID.Int64
	}

	children := make([]menuItemNodeJSON, 0, len(n.Children))
	for _, child := range n.Children {
		children = append(children, child.toJSON())
	}

	return menuItemNodeJSON{
		Item: menuItemJSON{
			ID:       n.Item.ID,
			MenuID:   n.Item.MenuID,
			ParentID: parentID,
			Title:    n.Item.Title,
			URL:      n.Item.Url.String,
			Target:   n.Item.Target.String,
			PageID:   pageID,
			Position: n.Item.Position,
			CssClass: n.Item.CssClass.String,
			IsActive: n.Item.IsActive,
		},
		Children: children,
		PageSlug: n.PageSlug,
	}
}

// serializeMenuItems JSON-encodes the menu item tree for the Alpine.js data attribute.
func serializeMenuItems(items []MenuItemNode) string {
	if len(items) == 0 {
		return "[]"
	}
	data, err := json.Marshal(items)
	if err != nil {
		slog.Error("failed to serialize menu items", "error", err)
		return "[]"
	}
	return string(data)
}

// buildMenuFormViewData constructs the view-layer form data for templ rendering.
func buildMenuFormViewData(
	isEdit bool,
	menu *store.Menu,
	items []MenuItemNode,
	pages []store.Page,
	languages []store.Language,
	errs map[string]string,
	formValues map[string]string,
) adminviews.MenuFormData {
	return adminviews.MenuFormData{
		IsEdit:     isEdit,
		Menu:       convertMenuInfo(menu),
		ItemsJSON:  serializeMenuItems(items),
		Pages:      convertMenuPages(pages),
		Targets:    model.ValidTargets,
		Languages:  convertLanguageOptions(languages),
		Errors:     errs,
		FormValues: formValues,
	}
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
	lang := h.renderer.GetAdminLang(r)

	// Get languages for selector
	languages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

	// Get default language for pre-selection
	defaultLang, err := h.queries.GetDefaultLanguage(r.Context())
	if err != nil {
		slog.Error("failed to get default language", "error", err)
	}

	formValues := make(map[string]string)
	if defaultLang.Code != "" {
		formValues["language_code"] = defaultLang.Code
	}

	viewData := buildMenuFormViewData(false, nil, nil, nil, languages, make(map[string]string), formValues)
	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "menus.new"), menuFormBreadcrumbs(lang))
	renderTempl(w, r, adminviews.MenuFormPage(pc, viewData))
}

// Create handles POST /admin/menus - creates a new menu.
func (h *MenusHandler) Create(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionContentReadOnly, redirectAdminMenusNew) {
		return
	}

	lang := h.renderer.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminMenusNew) {
		return
	}

	input := parseMenuFormInput(r)

	// Validate slug
	if errMsg := h.validateMenuSlugCreate(r.Context(), input.Slug, input.LanguageCode); errMsg != "" {
		input.Errors["slug"] = errMsg
	}

	if len(input.Errors) > 0 {
		languages := ListActiveLanguagesWithFallback(r.Context(), h.queries)
		viewData := buildMenuFormViewData(false, nil, nil, nil, languages, input.Errors, input.FormValues)
		pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "menus.new"), menuFormBreadcrumbs(lang))
		renderTempl(w, r, adminviews.MenuFormPage(pc, viewData))
		return
	}

	now := time.Now()
	menu, err := h.queries.CreateMenu(r.Context(), store.CreateMenuParams{
		Name:         input.Name,
		Slug:         input.Slug,
		LanguageCode: input.LanguageCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to create menu", "error", err)
		flashError(w, r, h.renderer, redirectAdminMenusNew, "Error creating menu")
		return
	}

	slog.Info("menu created", "menu_id", menu.ID, "slug", menu.Slug)
	_ = h.eventService.LogMenuEvent(r.Context(), model.EventLevelInfo, "Menu created", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"menu_id": menu.ID, "name": menu.Name, "slug": menu.Slug})
	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminMenusID, menu.ID), "Menu created successfully")
}

// EditForm handles GET /admin/menus/{id} - displays the menu builder.
func (h *MenusHandler) EditForm(w http.ResponseWriter, r *http.Request) {
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

	viewData := buildMenuFormViewData(true, &menu, tree, pages, languages, make(map[string]string), make(map[string]string))
	pc := buildPageContext(r, h.sessionManager, h.renderer, fmt.Sprintf("Edit Menu - %s", menu.Name), menuEditBreadcrumbs(lang, menu.Name, menu.ID))
	renderTempl(w, r, adminviews.MenuFormPage(pc, viewData))
}

// Update handles PUT /admin/menus/{id} - updates a menu.
func (h *MenusHandler) Update(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionContentReadOnly, redirectAdminMenus) {
		return
	}

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
	if errMsg := h.validateMenuSlugUpdate(r.Context(), input.Slug, input.LanguageCode, menu.Slug, menu.LanguageCode, id); errMsg != "" {
		input.Errors["slug"] = errMsg
	}

	if len(input.Errors) > 0 {
		// Get menu items for re-render
		items, _ := h.queries.ListMenuItemsWithPage(r.Context(), id)
		tree := buildMenuTree(items, sql.NullInt64{Valid: false})
		pages, _ := h.queries.ListPages(r.Context(), store.ListPagesParams{Limit: 1000, Offset: 0})
		languages := ListActiveLanguagesWithFallback(r.Context(), h.queries)

		viewData := buildMenuFormViewData(true, &menu, tree, pages, languages, input.Errors, input.FormValues)
		pc := buildPageContext(r, h.sessionManager, h.renderer, fmt.Sprintf("Edit Menu - %s", menu.Name), menuEditBreadcrumbs(lang, menu.Name, menu.ID))
		renderTempl(w, r, adminviews.MenuFormPage(pc, viewData))
		return
	}

	now := time.Now()
	_, err = h.queries.UpdateMenu(r.Context(), store.UpdateMenuParams{
		ID:           id,
		Name:         input.Name,
		Slug:         input.Slug,
		LanguageCode: input.LanguageCode,
		UpdatedAt:    now,
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
	_ = h.eventService.LogMenuEvent(r.Context(), model.EventLevelInfo, "Menu updated", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"menu_id": id})
	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminMenusID, id), "Menu updated successfully")
}

// Delete handles DELETE /admin/menus/{id} - deletes a menu.
func (h *MenusHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		http.Error(w, middleware.DemoModeMessageDetailed(middleware.RestrictionDeleteMenu), http.StatusForbidden)
		return
	}

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
	_ = h.eventService.LogMenuEvent(r.Context(), model.EventLevelInfo, "Menu deleted", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"menu_id": id, "slug": menu.Slug})

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
	if demoGuardAPI(w) {
		return
	}

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
	ParentID *int64 `json:"parent_id"`
	CSSClass string `json:"css_class"`
	IsActive bool   `json:"is_active"`
}

// UpdateItem handles PUT /admin/menus/{id}/items/{itemId} - updates a menu item.
func (h *MenusHandler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	if demoGuardAPI(w) {
		return
	}

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

	// Determine parent_id and position
	parentID := item.ParentID
	position := item.Position

	// If parent_id is explicitly provided in the request, use it
	// parent_id: 0 means "move to root level" (no parent)
	// parent_id: N means "move under item N"
	// parent_id: nil (not in request) means "keep existing parent"
	if req.ParentID != nil {
		var newParentID sql.NullInt64
		if *req.ParentID == 0 {
			// Explicitly move to root level
			newParentID = sql.NullInt64{Valid: false}
		} else {
			newParentID = sql.NullInt64{Int64: *req.ParentID, Valid: true}
		}
		// Check if parent is changing
		if newParentID.Valid != parentID.Valid ||
			(newParentID.Valid && newParentID.Int64 != parentID.Int64) {
			parentID = newParentID
			// When moving to a new parent, put at end of that parent's children
			position = h.getMaxMenuItemPosition(r, menu.ID, parentID) + 1
		}
	}

	now := time.Now()
	updatedItem, err := h.queries.UpdateMenuItem(r.Context(), store.UpdateMenuItemParams{
		ID:        item.ID,
		ParentID:  parentID,
		Title:     req.Title,
		Url:       validated.URL,
		Target:    util.NullStringFromValue(validated.Target),
		PageID:    validated.PageID,
		Position:  position,
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
	// Block in demo mode
	if middleware.IsDemoMode() {
		writeJSONError(w, http.StatusForbidden, middleware.DemoModeMessageDetailed(middleware.RestrictionDeleteMenuItem))
		return
	}

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
	if demoGuardAPI(w) {
		return
	}

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
					if err = processItems(item.Children, util.NullInt64FromValue(item.ID), new(int64(0))); err != nil {
					return err
				}
			}
		}
		return nil
	}

	return processItems(items, sql.NullInt64{Valid: false}, new(int64(0)))
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
	Name         string
	Slug         string
	LanguageCode string
	FormValues   map[string]string
	Errors       map[string]string
}

// parseMenuFormInput parses and validates menu form input (name, slug, language_code).
func parseMenuFormInput(r *http.Request) menuFormInput {
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	languageCode := strings.TrimSpace(r.FormValue("language_code"))

	formValues := map[string]string{
		"name":          name,
		"slug":          slug,
		"language_code": languageCode,
	}

	validationErrors := make(map[string]string)

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
		Name:         name,
		Slug:         slug,
		LanguageCode: languageCode,
		FormValues:   formValues,
		Errors:       validationErrors,
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
func (h *MenusHandler) validateMenuSlugCreate(ctx context.Context, slug string, languageCode string) string {
	return ValidateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.MenuSlugExistsForLanguage(ctx, store.MenuSlugExistsForLanguageParams{
			Slug:         slug,
			LanguageCode: languageCode,
		})
	})
}

// validateMenuSlugUpdate validates a menu slug for update within a language.
// Only validates if slug or language has changed.
func (h *MenusHandler) validateMenuSlugUpdate(ctx context.Context, slug string, languageCode string, currentSlug string, currentLanguageCode string, menuID int64) string {
	// If neither slug nor language changed, no validation needed
	if slug == currentSlug && languageCode == currentLanguageCode {
		return ""
	}
	return ValidateSlugWithChecker(slug, func() (int64, error) {
		return h.queries.MenuSlugExistsForLanguageExcluding(ctx, store.MenuSlugExistsForLanguageExcludingParams{
			Slug:         slug,
			LanguageCode: languageCode,
			ID:           menuID,
		})
	})
}
