package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/module"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
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
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	modules := h.registry.ListInfo()
	hooks := h.hooks.ListHookInfo()

	data := ModulesListData{
		Modules: modules,
		Hooks:   hooks,
	}

	h.renderer.RenderPage(w, r, "admin/modules_list", render.TemplateData{
		Title: i18n.T(lang, "nav.modules"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules", Active: true},
		},
	})
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

// ToggleActive handles POST /admin/modules/{name}/toggle - toggles module active status.
func (h *ModulesHandler) ToggleActive(w http.ResponseWriter, r *http.Request) {
	moduleName := chi.URLParam(r, "name")
	if moduleName == "" {
		writeJSONError(w, http.StatusBadRequest, "Module name required")
		return
	}

	var req ToggleActiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.registry.SetActive(moduleName, req.Active); err != nil {
		slog.Error("failed to toggle module active status", "module", moduleName, "error", err)
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	slog.Info("module active status toggled", "module", moduleName, "active", req.Active)

	writeJSONSuccess(w, map[string]any{"active": req.Active})
}

// ToggleSidebar handles POST /admin/modules/{name}/toggle-sidebar - toggles module sidebar visibility.
func (h *ModulesHandler) ToggleSidebar(w http.ResponseWriter, r *http.Request) {
	moduleName := chi.URLParam(r, "name")
	if moduleName == "" {
		writeJSONError(w, http.StatusBadRequest, "Module name required")
		return
	}

	var req ToggleSidebarRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.registry.SetShowInSidebar(moduleName, req.Show); err != nil {
		slog.Error("failed to toggle module sidebar visibility", "module", moduleName, "error", err)
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	slog.Info("module sidebar visibility toggled", "module", moduleName, "show", req.Show)

	writeJSONSuccess(w, map[string]any{"show": req.Show})
}
