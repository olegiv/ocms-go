// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// handleDashboard handles GET /admin/privacy - shows the privacy settings page.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Prepare display data
	displaySettings := struct {
		Enabled                     bool
		Debug                       bool
		PrivacyPolicyURL            string
		CookieName                  string
		CookieExpiresDays           int
		Theme                       string
		Position                    string
		GCMEnabled                  bool
		GCMDefaultAnalytics         bool
		GCMDefaultAdStorage         bool
		GCMDefaultAdUserData        bool
		GCMDefaultAdPersonalization bool
		GCMWaitForUpdate            int
		Services                    []Service
		ServicesJSON                string
		PredefinedServices          []Service
	}{
		Enabled:                     m.settings.Enabled,
		Debug:                       m.settings.Debug,
		PrivacyPolicyURL:            m.settings.PrivacyPolicyURL,
		CookieName:                  m.settings.CookieName,
		CookieExpiresDays:           m.settings.CookieExpiresDays,
		Theme:                       m.settings.Theme,
		Position:                    m.settings.Position,
		GCMEnabled:                  m.settings.GCMEnabled,
		GCMDefaultAnalytics:         m.settings.GCMDefaultAnalytics,
		GCMDefaultAdStorage:         m.settings.GCMDefaultAdStorage,
		GCMDefaultAdUserData:        m.settings.GCMDefaultAdUserData,
		GCMDefaultAdPersonalization: m.settings.GCMDefaultAdPersonalization,
		GCMWaitForUpdate:            m.settings.GCMWaitForUpdate,
		Services:                    m.settings.Services,
		PredefinedServices:          PredefinedServices,
	}

	// Serialize services to JSON for the UI
	// Note: json.Marshal(nil) returns "null", so we check for nil/empty first
	if len(m.settings.Services) > 0 {
		if servicesData, err := json.Marshal(m.settings.Services); err == nil {
			displaySettings.ServicesJSON = string(servicesData)
		} else {
			displaySettings.ServicesJSON = "[]"
		}
	} else {
		displaySettings.ServicesJSON = "[]"
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_privacy", render.TemplateData{
		Title: i18n.T(lang, "privacy.title"),
		User:  user,
		Data:  displaySettings,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "privacy.title"), URL: "/admin/privacy", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleSaveSettings handles POST /admin/privacy - saves privacy settings.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		m.ctx.Render.SetFlash(r, middleware.DemoModeMessageDetailed(middleware.RestrictionModuleSettings), "error")
		http.Redirect(w, r, "/admin/privacy", http.StatusSeeOther)
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
		m.ctx.Render.SetFlash(r, i18n.T(lang, "privacy.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/privacy", http.StatusSeeOther)
		return
	}

	// Build new settings from form
	newSettings := &Settings{
		Enabled:                     r.FormValue("enabled") == "1",
		Debug:                       r.FormValue("debug") == "1",
		PrivacyPolicyURL:            strings.TrimSpace(r.FormValue("privacy_policy_url")),
		CookieName:                  strings.TrimSpace(r.FormValue("cookie_name")),
		Theme:                       strings.TrimSpace(r.FormValue("theme")),
		Position:                    strings.TrimSpace(r.FormValue("position")),
		GCMEnabled:                  r.FormValue("gcm_enabled") == "1",
		GCMDefaultAnalytics:         r.FormValue("gcm_default_analytics") == "1",
		GCMDefaultAdStorage:         r.FormValue("gcm_default_ad_storage") == "1",
		GCMDefaultAdUserData:        r.FormValue("gcm_default_ad_user_data") == "1",
		GCMDefaultAdPersonalization: r.FormValue("gcm_default_ad_personalization") == "1",
	}

	// Parse integer fields
	if days, err := strconv.Atoi(r.FormValue("cookie_expires_days")); err == nil && days > 0 {
		newSettings.CookieExpiresDays = days
	} else {
		newSettings.CookieExpiresDays = 365
	}

	if waitFor, err := strconv.Atoi(r.FormValue("gcm_wait_for_update")); err == nil && waitFor >= 0 {
		newSettings.GCMWaitForUpdate = waitFor
	} else {
		newSettings.GCMWaitForUpdate = 500
	}

	// Set defaults
	if newSettings.CookieName == "" {
		newSettings.CookieName = "klaro"
	}
	if newSettings.Theme == "" {
		newSettings.Theme = "light"
	}
	if newSettings.Position == "" {
		newSettings.Position = "bottom-right"
	}

	// Parse services JSON
	servicesJSON := r.FormValue("services")
	if servicesJSON != "" && servicesJSON != "[]" {
		var services []Service
		if err := json.Unmarshal([]byte(servicesJSON), &services); err == nil {
			newSettings.Services = services
		} else {
			m.ctx.Logger.Warn("failed to parse services JSON", "error", err)
		}
	}

	// Save to database
	if err := saveSettings(m.ctx.DB, newSettings); err != nil {
		m.ctx.Logger.Error("failed to save privacy settings", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "privacy.error_save"), "error")
		http.Redirect(w, r, "/admin/privacy", http.StatusSeeOther)
		return
	}

	// Update in-memory settings
	m.settings = newSettings

	m.ctx.Logger.Info("Privacy settings updated",
		"user", user.Email,
		"enabled", newSettings.Enabled,
		"gcm_enabled", newSettings.GCMEnabled,
	)

	m.ctx.Render.SetFlash(r, i18n.T(lang, "privacy.success_save"), "success")
	http.Redirect(w, r, "/admin/privacy", http.StatusSeeOther)
}
