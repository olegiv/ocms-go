// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// ModulesHandler handles module management routes.
type ModulesHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	registry       *module.Registry
	hooks          *module.HookRegistry
}

// NewModulesHandler creates a new ModulesHandler.
func NewModulesHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, registry *module.Registry, hooks *module.HookRegistry) *ModulesHandler {
	return &ModulesHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		registry:       registry,
		hooks:          hooks,
	}
}

// ModulesListData holds data for the modules list template.
type ModulesListData struct {
	Modules []module.Info
	Hooks   []module.HookInfo
}

// List handles GET /admin/modules - displays registered modules.
func (h *ModulesHandler) List(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	viewData := adminviews.ModulesViewData{
		Modules: convertModuleViewItems(h.registry.ListInfo()),
		Hooks:   convertHookViewItems(h.hooks.ListHookInfo()),
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer,
		i18n.T(lang, "nav.modules"),
		modulesBreadcrumbs(lang))
	renderTempl(w, r, adminviews.ModulesPage(pc, viewData))
}

// ToggleActiveRequest represents the request body for toggling module active status.
type ToggleActiveRequest struct {
	Active bool `json:"active"`
}

// ToggleActiveResponse represents the response for toggling module active status.
type ToggleActiveResponse struct {
	Success bool   `json:"success"`
	Active  bool   `json:"active"`
	Message string `json:"message,omitempty"`
}

// ToggleSidebarRequest represents the request body for toggling module sidebar visibility.
type ToggleSidebarRequest struct {
	Show bool `json:"show"`
}

// ToggleSidebarResponse represents the response for toggling module sidebar visibility.
type ToggleSidebarResponse struct {
	Success bool   `json:"success"`
	Show    bool   `json:"show"`
	Message string `json:"message,omitempty"`
}

// moduleToggleParams holds parameters for a generic module toggle operation.
type moduleToggleParams struct {
	fieldName string                            // JSON response field name (e.g., "active", "show")
	logMsg    string                            // Log message (e.g., "module active status toggled")
	setFn     func(name string, val bool) error // Registry setter function
}

// handleModuleToggle performs a generic toggle operation for a module boolean field.
func (h *ModulesHandler) handleModuleToggle(w http.ResponseWriter, r *http.Request, p moduleToggleParams) {
	moduleName := chi.URLParam(r, "name")
	if moduleName == "" {
		writeJSONError(w, http.StatusBadRequest, "Module name required")
		return
	}

	var req struct {
		Value bool `json:"active"`
		Show  bool `json:"show"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Get the actual value based on field name
	var value bool
	switch p.fieldName {
	case "active":
		value = req.Value
	case "show":
		value = req.Show
	}

	if err := p.setFn(moduleName, value); err != nil {
		slog.Error("failed to toggle module "+p.fieldName, "module", moduleName, "error", err)
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	slog.Info(p.logMsg, "module", moduleName, p.fieldName, value)
	writeJSONSuccess(w, map[string]any{p.fieldName: value})
}

// ToggleActive handles POST /admin/modules/{name}/toggle - toggles module active status.
func (h *ModulesHandler) ToggleActive(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		writeJSONError(w, http.StatusForbidden, middleware.DemoModeMessageDetailed(middleware.RestrictionModules))
		return
	}

	h.handleModuleToggle(w, r, moduleToggleParams{
		fieldName: "active",
		logMsg:    "module active status toggled",
		setFn:     h.registry.SetActive,
	})
}

// ToggleSidebar handles POST /admin/modules/{name}/toggle-sidebar - toggles module sidebar visibility.
func (h *ModulesHandler) ToggleSidebar(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		writeJSONError(w, http.StatusForbidden, middleware.DemoModeMessageDetailed(middleware.RestrictionModules))
		return
	}

	h.handleModuleToggle(w, r, moduleToggleParams{
		fieldName: "show",
		logMsg:    "module sidebar visibility toggled",
		setFn:     h.registry.SetShowInSidebar,
	})
}
