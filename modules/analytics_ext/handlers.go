// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_ext

import (
	"net/http"
	"strings"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// handleDashboard handles GET /admin/external-analytics - shows the analytics settings page.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	lang := m.ctx.Render.GetAdminLang(r)
	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "analytics_ext.title"), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "analytics_ext.title"), URL: "/admin/external-analytics", Active: true},
	})
	viewData := AnalyticsExtViewData{
		GA4Enabled:       m.settings.GA4Enabled,
		GA4MeasurementID: m.settings.GA4MeasurementID,
		GTMEnabled:       m.settings.GTMEnabled,
		GTMContainerID:   m.settings.GTMContainerID,
		MatomoEnabled:    m.settings.MatomoEnabled,
		MatomoURL:        m.settings.MatomoURL,
		MatomoSiteID:     m.settings.MatomoSiteID,
	}
	render.Templ(w, r, AnalyticsExtPage(pc, viewData))
}

// handleSaveSettings handles POST /admin/external-analytics - saves analytics settings.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		m.ctx.Render.SetFlash(r, middleware.DemoModeMessageDetailed(middleware.RestrictionModuleSettings), "error")
		http.Redirect(w, r, "/admin/external-analytics", http.StatusSeeOther)
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
		m.ctx.Render.SetFlash(r, i18n.T(lang, "analytics_ext.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/external-analytics", http.StatusSeeOther)
		return
	}

	// Build new settings from form
	newSettings := &Settings{
		GA4Enabled:       r.FormValue("ga4_enabled") == "1",
		GA4MeasurementID: strings.TrimSpace(r.FormValue("ga4_measurement_id")),
		GTMEnabled:       r.FormValue("gtm_enabled") == "1",
		GTMContainerID:   strings.TrimSpace(r.FormValue("gtm_container_id")),
		MatomoEnabled:    r.FormValue("matomo_enabled") == "1",
		MatomoURL:        strings.TrimSpace(r.FormValue("matomo_url")),
		MatomoSiteID:     strings.TrimSpace(r.FormValue("matomo_site_id")),
	}

	// Validate required fields when trackers are enabled
	var validationErrors []string

	if newSettings.GA4Enabled && newSettings.GA4MeasurementID == "" {
		validationErrors = append(validationErrors, i18n.T(lang, "analytics_ext.error_ga4_id_required"))
	}

	if newSettings.GTMEnabled && newSettings.GTMContainerID == "" {
		validationErrors = append(validationErrors, i18n.T(lang, "analytics_ext.error_gtm_id_required"))
	}

	if newSettings.MatomoEnabled {
		if newSettings.MatomoURL == "" {
			validationErrors = append(validationErrors, i18n.T(lang, "analytics_ext.error_matomo_url_required"))
		}
		if newSettings.MatomoSiteID == "" {
			validationErrors = append(validationErrors, i18n.T(lang, "analytics_ext.error_matomo_site_id_required"))
		}
	}

	if len(validationErrors) > 0 {
		m.ctx.Render.SetFlash(r, strings.Join(validationErrors, " "), "error")
		http.Redirect(w, r, "/admin/external-analytics", http.StatusSeeOther)
		return
	}

	// Save to database
	if err := saveSettings(m.ctx.DB, newSettings); err != nil {
		m.ctx.Logger.Error("failed to save external analytics settings", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "analytics_ext.error_save"), "error")
		http.Redirect(w, r, "/admin/external-analytics", http.StatusSeeOther)
		return
	}

	// Update in-memory settings
	m.settings = newSettings

	m.ctx.Logger.Info("external analytics settings updated",
		"user", user.Email,
		"ga4_enabled", newSettings.GA4Enabled,
		"gtm_enabled", newSettings.GTMEnabled,
		"matomo_enabled", newSettings.MatomoEnabled,
	)

	m.ctx.Render.SetFlash(r, i18n.T(lang, "analytics_ext.success_save"), "success")
	http.Redirect(w, r, "/admin/external-analytics", http.StatusSeeOther)
}
