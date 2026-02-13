// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package informer

import (
	"net/http"
	"strings"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// handleDashboard handles GET /admin/informer - shows the informer settings page.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	lang := m.ctx.Render.GetAdminLang(r)

	viewData := InformerViewData{
		Enabled:   m.settings.Enabled,
		Text:      m.settings.Text,
		BgColor:   m.settings.BgColor,
		TextColor: m.settings.TextColor,
	}

	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "informer.title"), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "informer.title"), URL: "/admin/informer", Active: true},
	})
	render.RenderTempl(w, r, InformerPage(pc, viewData))
}

// handleSaveSettings handles POST /admin/informer - saves informer settings.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if middleware.IsDemoMode() {
		m.ctx.Render.SetFlash(r, middleware.DemoModeMessageDetailed(middleware.RestrictionModuleSettings), "error")
		http.Redirect(w, r, "/admin/informer", http.StatusSeeOther)
		return
	}

	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		m.ctx.Logger.Error("failed to parse form", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "informer.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/informer", http.StatusSeeOther)
		return
	}

	newSettings := &Settings{
		Enabled:   r.FormValue("enabled") == "1",
		Text:      strings.TrimSpace(r.FormValue("text")),
		BgColor:   strings.TrimSpace(r.FormValue("bg_color")),
		TextColor: strings.TrimSpace(r.FormValue("text_color")),
	}

	if newSettings.BgColor == "" {
		newSettings.BgColor = "#1e40af"
	}
	if newSettings.TextColor == "" {
		newSettings.TextColor = "#ffffff"
	}

	if err := saveSettings(m.ctx.DB, newSettings); err != nil {
		m.ctx.Logger.Error("failed to save informer settings", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "informer.error_save"), "error")
		http.Redirect(w, r, "/admin/informer", http.StatusSeeOther)
		return
	}

	// Reload to pick up the new updated_at version for cookie invalidation.
	saved, err := loadSettings(m.ctx.DB)
	if err != nil {
		m.ctx.Logger.Error("failed to reload informer settings", "error", err)
		newSettings.Version = m.settings.Version
	} else {
		newSettings = saved
	}
	m.settings = newSettings

	m.ctx.Logger.Info("Informer settings updated",
		"user", user.Email,
		"enabled", newSettings.Enabled,
		"text", newSettings.Text,
	)

	m.ctx.Render.SetFlash(r, i18n.T(lang, "informer.success_save"), "success")
	http.Redirect(w, r, "/admin/informer", http.StatusSeeOther)
}
