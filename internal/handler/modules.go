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
	Modules []module.ModuleInfo
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

	if err := h.renderer.Render(w, r, "admin/modules_list", render.TemplateData{
		Title: i18n.T(lang, "nav.modules"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
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

// ToggleActive handles POST /admin/modules/{name}/toggle - toggles module active status.
func (h *ModulesHandler) ToggleActive(w http.ResponseWriter, r *http.Request) {
	moduleName := chi.URLParam(r, "name")
	if moduleName == "" {
		http.Error(w, "Module name required", http.StatusBadRequest)
		return
	}

	var req ToggleActiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.registry.SetActive(moduleName, req.Active); err != nil {
		slog.Error("failed to toggle module active status", "module", moduleName, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ToggleActiveResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	slog.Info("module active status toggled", "module", moduleName, "active", req.Active)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ToggleActiveResponse{
		Success: true,
		Active:  req.Active,
	})
}
