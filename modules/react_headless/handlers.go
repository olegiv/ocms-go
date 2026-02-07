// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package react_headless

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// handleDashboard handles GET /admin/react-headless - shows the settings page.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	data := struct {
		Settings       Settings
		Version        string
		APIBaseURL     string
		AllowedOrigins []string
	}{
		Settings:       m.GetSettings(),
		Version:        m.Version(),
		APIBaseURL:     "/api/v1",
		AllowedOrigins: m.GetAllowedOrigins(),
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_react_headless", render.TemplateData{
		Title: i18n.T(lang, "react_headless.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "react_headless.title"), URL: "/admin/react-headless", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleSaveSettings handles POST /admin/react-headless - saves CORS settings.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		m.ctx.Logger.Error("failed to parse form", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "react_headless.error_parse"), "error")
		http.Redirect(w, r, "/admin/react-headless", http.StatusSeeOther)
		return
	}

	// Parse and validate origins
	originsRaw := strings.TrimSpace(r.FormValue("allowed_origins"))
	if originsRaw == "" {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "react_headless.error_origins_required"), "error")
		http.Redirect(w, r, "/admin/react-headless", http.StatusSeeOther)
		return
	}

	// Clean up origins
	var cleanOrigins []string
	for _, o := range strings.Split(originsRaw, ",") {
		trimmed := strings.TrimSpace(o)
		if trimmed != "" {
			cleanOrigins = append(cleanOrigins, trimmed)
		}
	}

	maxAge, err := strconv.Atoi(strings.TrimSpace(r.FormValue("max_age")))
	if err != nil || maxAge < 0 {
		maxAge = 3600
	}

	newSettings := &Settings{
		AllowedOrigins:   strings.Join(cleanOrigins, ", "),
		AllowCredentials: r.FormValue("allow_credentials") == "1",
		MaxAge:           maxAge,
	}

	if err := m.saveSettings(newSettings); err != nil {
		m.ctx.Logger.Error("failed to save react_headless settings", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "react_headless.error_save"), "error")
		http.Redirect(w, r, "/admin/react-headless", http.StatusSeeOther)
		return
	}

	m.settings = newSettings

	m.ctx.Logger.Info("react_headless settings updated",
		"user", user.Email,
		"allowed_origins", newSettings.AllowedOrigins,
	)

	m.ctx.Render.SetFlash(r, i18n.T(lang, "react_headless.success_save"), "success")
	http.Redirect(w, r, "/admin/react-headless", http.StatusSeeOther)
}
