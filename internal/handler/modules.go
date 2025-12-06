package handler

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"

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
