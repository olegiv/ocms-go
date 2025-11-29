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
	Menus []store.Menu
}

// List handles GET /admin/menus - displays a list of menus.
func (h *MenusHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	menus, err := h.queries.ListMenus(r.Context())
	if err != nil {
		slog.Error("failed to list menus", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := MenusListData{
		Menus: menus,
	}

	if err := h.renderer.Render(w, r, "admin/menus_list", render.TemplateData{
		Title: "Menus",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Menus", URL: "/admin/menus", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
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
				Children: buildMenuTree(items, sql.NullInt64{Int64: item.ID, Valid: true}),
			}
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// NewForm handles GET /admin/menus/new - displays the new menu form.
func (h *MenusHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	data := MenuFormData{
		Targets:    model.ValidTargets,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     false,
	}

	if err := h.renderer.Render(w, r, "admin/menus_form", render.TemplateData{
		Title: "New Menu",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Menus", URL: "/admin/menus"},
			{Label: "New Menu", URL: "/admin/menus/new", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Create handles POST /admin/menus - creates a new menu.
func (h *MenusHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/menus/new", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))

	formValues := map[string]string{
		"name": name,
		"slug": slug,
	}

	errors := make(map[string]string)

	// Validate name
	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) < 2 {
		errors["name"] = "Name must be at least 2 characters"
	}

	// Validate slug
	if slug == "" {
		slug = util.Slugify(name)
		formValues["slug"] = slug
	}

	if slug == "" {
		errors["slug"] = "Slug is required"
	} else if !util.IsValidSlug(slug) {
		errors["slug"] = "Invalid slug format"
	} else {
		exists, err := h.queries.MenuSlugExists(r.Context(), slug)
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			errors["slug"] = "Error checking slug"
		} else if exists != 0 {
			errors["slug"] = "Slug already exists"
		}
	}

	if len(errors) > 0 {
		data := MenuFormData{
			Targets:    model.ValidTargets,
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     false,
		}

		if err := h.renderer.Render(w, r, "admin/menus_form", render.TemplateData{
			Title: "New Menu",
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: "Dashboard", URL: "/admin"},
				{Label: "Menus", URL: "/admin/menus"},
				{Label: "New Menu", URL: "/admin/menus/new", Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	now := time.Now()
	menu, err := h.queries.CreateMenu(r.Context(), store.CreateMenuParams{
		Name:      name,
		Slug:      slug,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create menu", "error", err)
		h.renderer.SetFlash(r, "Error creating menu", "error")
		http.Redirect(w, r, "/admin/menus/new", http.StatusSeeOther)
		return
	}

	slog.Info("menu created", "menu_id", menu.ID, "slug", menu.Slug)
	h.renderer.SetFlash(r, "Menu created successfully", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/menus/%d", menu.ID), http.StatusSeeOther)
}

// EditForm handles GET /admin/menus/{id} - displays the menu builder.
func (h *MenusHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid menu ID", "error")
		http.Redirect(w, r, "/admin/menus", http.StatusSeeOther)
		return
	}

	menu, err := h.queries.GetMenuByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Menu not found", "error")
		} else {
			slog.Error("failed to get menu", "error", err, "menu_id", id)
			h.renderer.SetFlash(r, "Error loading menu", "error")
		}
		http.Redirect(w, r, "/admin/menus", http.StatusSeeOther)
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

	data := MenuFormData{
		Menu:       &menu,
		Items:      tree,
		Pages:      pages,
		Targets:    model.ValidTargets,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     true,
	}

	if err := h.renderer.Render(w, r, "admin/menus_form", render.TemplateData{
		Title: fmt.Sprintf("Edit Menu - %s", menu.Name),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Menus", URL: "/admin/menus"},
			{Label: menu.Name, URL: fmt.Sprintf("/admin/menus/%d", menu.ID), Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Update handles PUT /admin/menus/{id} - updates a menu.
func (h *MenusHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid menu ID", "error")
		http.Redirect(w, r, "/admin/menus", http.StatusSeeOther)
		return
	}

	menu, err := h.queries.GetMenuByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Menu not found", "error")
		} else {
			slog.Error("failed to get menu", "error", err, "menu_id", id)
			h.renderer.SetFlash(r, "Error loading menu", "error")
		}
		http.Redirect(w, r, "/admin/menus", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/menus/%d", id), http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))

	formValues := map[string]string{
		"name": name,
		"slug": slug,
	}

	errors := make(map[string]string)

	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) < 2 {
		errors["name"] = "Name must be at least 2 characters"
	}

	if slug == "" {
		slug = util.Slugify(name)
		formValues["slug"] = slug
	}

	if slug == "" {
		errors["slug"] = "Slug is required"
	} else if !util.IsValidSlug(slug) {
		errors["slug"] = "Invalid slug format"
	} else if slug != menu.Slug {
		exists, err := h.queries.MenuSlugExistsExcluding(r.Context(), store.MenuSlugExistsExcludingParams{
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

	if len(errors) > 0 {
		// Get menu items for re-render
		items, _ := h.queries.ListMenuItemsWithPage(r.Context(), id)
		tree := buildMenuTree(items, sql.NullInt64{Valid: false})
		pages, _ := h.queries.ListPages(r.Context(), store.ListPagesParams{Limit: 1000, Offset: 0})

		data := MenuFormData{
			Menu:       &menu,
			Items:      tree,
			Pages:      pages,
			Targets:    model.ValidTargets,
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     true,
		}

		if err := h.renderer.Render(w, r, "admin/menus_form", render.TemplateData{
			Title: fmt.Sprintf("Edit Menu - %s", menu.Name),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: "Dashboard", URL: "/admin"},
				{Label: "Menus", URL: "/admin/menus"},
				{Label: menu.Name, URL: fmt.Sprintf("/admin/menus/%d", menu.ID), Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	now := time.Now()
	_, err = h.queries.UpdateMenu(r.Context(), store.UpdateMenuParams{
		ID:        id,
		Name:      name,
		Slug:      slug,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to update menu", "error", err, "menu_id", id)
		h.renderer.SetFlash(r, "Error updating menu", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/menus/%d", id), http.StatusSeeOther)
		return
	}

	// Invalidate menu cache (both old and new slug in case slug changed)
	h.renderer.InvalidateMenuCache(menu.Slug)
	if slug != menu.Slug {
		h.renderer.InvalidateMenuCache(slug)
	}

	slog.Info("menu updated", "menu_id", id, "updated_by", user.ID)
	h.renderer.SetFlash(r, "Menu updated successfully", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/menus/%d", id), http.StatusSeeOther)
}

// Delete handles DELETE /admin/menus/{id} - deletes a menu.
func (h *MenusHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid menu ID", http.StatusBadRequest)
		return
	}

	menu, err := h.queries.GetMenuByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Menu not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get menu", "error", err, "menu_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
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

	user := middleware.GetUser(r)
	slog.Info("menu deleted", "menu_id", id, "slug", menu.Slug, "deleted_by", user.ID)

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	h.renderer.SetFlash(r, "Menu deleted successfully", "success")
	http.Redirect(w, r, "/admin/menus", http.StatusSeeOther)
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
	idStr := chi.URLParam(r, "id")
	menuID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid menu ID", http.StatusBadRequest)
		return
	}

	// Verify menu exists and get slug for cache invalidation
	menu, err := h.queries.GetMenuByID(r.Context(), menuID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Menu not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	var req AddItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate
	if req.Title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}

	if req.Target == "" {
		req.Target = model.TargetSelf
	} else if !model.IsValidTarget(req.Target) {
		http.Error(w, "Invalid target", http.StatusBadRequest)
		return
	}

	// Get max position for this parent
	var parentID sql.NullInt64
	if req.ParentID != nil {
		parentID = sql.NullInt64{Int64: *req.ParentID, Valid: true}
	}

	maxPosResult, err := h.queries.GetMaxMenuItemPosition(r.Context(), store.GetMaxMenuItemPositionParams{
		MenuID:   menuID,
		ParentID: parentID,
	})
	var maxPos int64 = -1
	if err != nil {
		slog.Error("failed to get max position", "error", err)
	} else if maxPosResult != nil {
		if v, ok := maxPosResult.(int64); ok {
			maxPos = v
		}
	}

	var pageID sql.NullInt64
	if req.PageID != nil {
		pageID = sql.NullInt64{Int64: *req.PageID, Valid: true}
	}

	now := time.Now()
	item, err := h.queries.CreateMenuItem(r.Context(), store.CreateMenuItemParams{
		MenuID:    menuID,
		ParentID:  parentID,
		Title:     req.Title,
		Url:       sql.NullString{String: req.URL, Valid: req.URL != ""},
		Target:    sql.NullString{String: req.Target, Valid: req.Target != ""},
		PageID:    pageID,
		Position:  maxPos + 1,
		CssClass:  sql.NullString{String: req.CSSClass, Valid: req.CSSClass != ""},
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create menu item", "error", err)
		http.Error(w, "Error creating menu item", http.StatusInternalServerError)
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"item":    item,
	})
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
	menuIDStr := chi.URLParam(r, "id")
	menuID, err := strconv.ParseInt(menuIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid menu ID", http.StatusBadRequest)
		return
	}

	itemIDStr := chi.URLParam(r, "itemId")
	itemID, err := strconv.ParseInt(itemIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid item ID", http.StatusBadRequest)
		return
	}

	item, err := h.queries.GetMenuItemByID(r.Context(), itemID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Menu item not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if item.MenuID != menuID {
		http.Error(w, "Item does not belong to this menu", http.StatusBadRequest)
		return
	}

	// Get menu for cache invalidation
	menu, err := h.queries.GetMenuByID(r.Context(), menuID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var req UpdateItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}

	if req.Target == "" {
		req.Target = model.TargetSelf
	} else if !model.IsValidTarget(req.Target) {
		http.Error(w, "Invalid target", http.StatusBadRequest)
		return
	}

	var pageID sql.NullInt64
	if req.PageID != nil {
		pageID = sql.NullInt64{Int64: *req.PageID, Valid: true}
	}

	now := time.Now()
	updatedItem, err := h.queries.UpdateMenuItem(r.Context(), store.UpdateMenuItemParams{
		ID:        itemID,
		ParentID:  item.ParentID, // Keep existing parent
		Title:     req.Title,
		Url:       sql.NullString{String: req.URL, Valid: req.URL != ""},
		Target:    sql.NullString{String: req.Target, Valid: req.Target != ""},
		PageID:    pageID,
		Position:  item.Position, // Keep existing position
		CssClass:  sql.NullString{String: req.CSSClass, Valid: req.CSSClass != ""},
		IsActive:  req.IsActive,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to update menu item", "error", err)
		http.Error(w, "Error updating menu item", http.StatusInternalServerError)
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"item":    updatedItem,
	})
}

// DeleteItem handles DELETE /admin/menus/{id}/items/{itemId} - deletes a menu item.
func (h *MenusHandler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	menuIDStr := chi.URLParam(r, "id")
	menuID, err := strconv.ParseInt(menuIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid menu ID", http.StatusBadRequest)
		return
	}

	itemIDStr := chi.URLParam(r, "itemId")
	itemID, err := strconv.ParseInt(itemIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid item ID", http.StatusBadRequest)
		return
	}

	item, err := h.queries.GetMenuItemByID(r.Context(), itemID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Menu item not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if item.MenuID != menuID {
		http.Error(w, "Item does not belong to this menu", http.StatusBadRequest)
		return
	}

	// Get menu for cache invalidation
	menu, err := h.queries.GetMenuByID(r.Context(), menuID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	err = h.queries.DeleteMenuItem(r.Context(), itemID)
	if err != nil {
		slog.Error("failed to delete menu item", "error", err)
		http.Error(w, "Error deleting menu item", http.StatusInternalServerError)
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
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
	menuIDStr := chi.URLParam(r, "id")
	menuID, err := strconv.ParseInt(menuIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid menu ID", http.StatusBadRequest)
		return
	}

	// Get menu for cache invalidation
	menu, err := h.queries.GetMenuByID(r.Context(), menuID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Menu not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	var req ReorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	now := time.Now()

	// Process items recursively
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
			err = h.queries.UpdateMenuItemPosition(r.Context(), store.UpdateMenuItemPositionParams{
				ID:        item.ID,
				ParentID:  parentID,
				Position:  *position,
				UpdatedAt: now,
			})
			if err != nil {
				return fmt.Errorf("failed to update item %d: %w", item.ID, err)
			}
			*position++

			// Process children
			if len(item.Children) > 0 {
				childPos := int64(0)
				err = processItems(item.Children, sql.NullInt64{Int64: item.ID, Valid: true}, &childPos)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}

	pos := int64(0)
	if err := processItems(req.Items, sql.NullInt64{Valid: false}, &pos); err != nil {
		slog.Error("failed to reorder menu items", "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Invalidate menu cache
	h.renderer.InvalidateMenuCache(menu.Slug)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}
