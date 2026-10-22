// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
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

	loginProtection *middleware.LoginProtection
	eventService    *service.EventService
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

// SetPasswordConfirmation sets what confirming a module activation with the
// administrator's password uses: the login lockout, which wrong passwords
// count toward, and the event log. Either may be nil (tests).
func (h *ModulesHandler) SetPasswordConfirmation(lp *middleware.LoginProtection, es *service.EventService) {
	h.loginProtection = lp
	h.eventService = es
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
	fieldName string // JSON response field name (e.g., "active", "show")
	logMsg    string // Log message (e.g., "module active status toggled")
	// setFn applies the toggle; password is the administrator's current
	// password, when the request carries one.
	setFn func(name string, val bool, password string) error
}

// activationRefusal is a toggle setFn's refusal to show the administrator.
type activationRefusal struct{ message string }

func (e activationRefusal) Error() string { return e.message }

// handleModuleToggle performs a generic toggle operation for a module boolean field.
func (h *ModulesHandler) handleModuleToggle(w http.ResponseWriter, r *http.Request, p moduleToggleParams) {
	moduleName := chi.URLParam(r, "name")
	if moduleName == "" {
		writeJSONError(w, http.StatusBadRequest, "Module name required")
		return
	}

	var req struct {
		Value    bool   `json:"active"`
		Show     bool   `json:"show"`
		Password string `json:"password"`
	}
	if err := decodeJSONWithLimit(w, r, &req, MaxJSONBodyBytes); err != nil {
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

	if err := p.setFn(moduleName, value, req.Password); err != nil {
		var refusal activationRefusal
		if errors.As(err, &refusal) {
			writeJSONError(w, http.StatusForbidden, refusal.message)
			return
		}
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
		setFn: func(name string, active bool, password string) error {
			if !active || !h.registry.ActivationNeedsPassword(name) {
				return h.registry.SetActive(name, active)
			}
			if msg := h.confirmActivation(r, name, password); msg != "" {
				return activationRefusal{message: msg}
			}
			if err := h.registry.ActivateConfirmed(name); err != nil {
				return err
			}
			h.logModuleEvent(r, model.EventLevelInfo, "Module activated with password confirmation", name)
			return nil
		},
	})
}

// confirmActivation checks the current password the administrator gives to
// activate module name, whose activation needs one. It returns the message
// to show, or "" when the password is right.
func (h *ModulesHandler) confirmActivation(r *http.Request, name, password string) string {
	lang := h.renderer.GetAdminLang(r)
	user := middleware.GetUser(r)
	if user == nil {
		return i18n.T(lang, "modules.activation_password_required")
	}
	logRefusal := func(message string) {
		h.logModuleEvent(r, model.EventLevelWarning, message, name)
	}
	c := confirmPassword(r, h.loginProtection, h.eventService, *user, password, func() {
		logRefusal("Module activation refused: wrong current password")
	})
	switch c.Outcome {
	case passwordOK:
		return ""
	case passwordMissing:
		return i18n.T(lang, "modules.activation_password_required")
	case passwordLocked:
		logRefusal("Module activation refused: account locked")
		return i18n.T(lang, "modules.activation_locked")
	case passwordUnverified:
		slog.Error("password check error", "error", c.Err, "user_id", user.ID)
		return i18n.T(lang, "modules.activation_password_unverified")
	}
	return i18n.T(lang, "modules.activation_password_invalid")
}

// logModuleEvent logs message about module name as a security event.
func (h *ModulesHandler) logModuleEvent(r *http.Request, level, message, name string) {
	if h.eventService == nil {
		return
	}
	if err := h.eventService.LogEvent(r.Context(), level, model.EventCategorySecurity, message,
		middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"module": name}); err != nil {
		slog.ErrorContext(service.WithoutEventLog(r.Context()), "failed to log module event", "error", err, "message", message, "module", name)
	}
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
		setFn: func(name string, show bool, _ string) error {
			return h.registry.SetShowInSidebar(name, show)
		},
	})
}
