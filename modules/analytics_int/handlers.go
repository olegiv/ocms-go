// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// requireAPIAuth checks if the user is authenticated and returns 401 if not.
// Returns the user if authenticated, nil otherwise.
func (m *Module) requireAPIAuth(w http.ResponseWriter, r *http.Request) *store.User {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return nil
	}
	return user
}

// parseDateRangeParam extracts the date range from query params and returns start/end dates.
func parseDateRangeParam(r *http.Request) (string, time.Time, time.Time) {
	dateRange := r.URL.Query().Get("range")
	if dateRange == "" {
		dateRange = "30d"
	}
	startDate, endDate := parseDateRange(dateRange)
	return dateRange, startDate, endDate
}

// handleDashboard renders the analytics dashboard.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)
	dateRange, startDate, endDate := parseDateRangeParam(r)

	// Fetch all dashboard data
	data := DashboardData{
		Overview:     m.getOverviewStats(r.Context(), startDate, endDate),
		TopPages:     m.getTopPages(r.Context(), startDate, endDate, 10),
		TopReferrers: m.getTopReferrers(r.Context(), startDate, endDate, 10),
		Browsers:     m.getBrowserStats(r.Context(), startDate, endDate),
		Devices:      m.getDeviceStats(r.Context(), startDate, endDate),
		Countries:    m.getCountryStats(r.Context(), startDate, endDate, 10),
		TimeSeries:   m.getTimeSeries(r.Context(), startDate, endDate),
		DateRange:    dateRange,
		Settings:     *m.settings,
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_analytics_int", render.TemplateData{
		Title: i18n.T(lang, "analytics_int.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "analytics_int.title"), URL: "/admin/internal-analytics", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleAPIStats returns JSON stats for HTMX updates.
func (m *Module) handleAPIStats(w http.ResponseWriter, r *http.Request) {
	if m.requireAPIAuth(w, r) == nil {
		return
	}

	_, startDate, endDate := parseDateRangeParam(r)

	data := map[string]any{
		"overview":   m.getOverviewStats(r.Context(), startDate, endDate),
		"topPages":   m.getTopPages(r.Context(), startDate, endDate, 10),
		"timeSeries": m.getTimeSeries(r.Context(), startDate, endDate),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

// handleRealtime returns real-time visitor count.
func (m *Module) handleRealtime(w http.ResponseWriter, r *http.Request) {
	if m.requireAPIAuth(w, r) == nil {
		return
	}

	count := m.GetRealTimeVisitorCount(5)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"visitors": count})
}

// handleSaveSettings saves module settings.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	user := m.requireAPIAuth(w, r)
	if user == nil {
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		m.ctx.Logger.Error("parse form error", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "analytics_int.error_save"), "error")
		http.Redirect(w, r, "/admin/internal-analytics", http.StatusSeeOther)
		return
	}

	// Update settings
	m.settings.Enabled = r.FormValue("enabled") == "1"

	// Parse retention days
	if retentionStr := r.FormValue("retention_days"); retentionStr != "" {
		if retention, err := strconv.Atoi(retentionStr); err == nil && retention > 0 {
			m.settings.RetentionDays = retention
		}
	}

	// Parse excluded paths (newline-separated)
	excludePathsStr := r.FormValue("exclude_paths")
	var excludePaths []string
	for _, path := range strings.Split(excludePathsStr, "\n") {
		path = strings.TrimSpace(path)
		if path != "" {
			excludePaths = append(excludePaths, path)
		}
	}
	m.settings.ExcludePaths = excludePaths

	// Save to database
	if err := m.saveSettings(); err != nil {
		m.ctx.Logger.Error("failed to save settings", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "analytics_int.error_save"), "error")
	} else {
		m.ctx.Logger.Info("internal analytics settings updated", "user", user.Email)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "analytics_int.success_save"), "success")
	}

	http.Redirect(w, r, "/admin/internal-analytics", http.StatusSeeOther)
}

// handleRunAggregation triggers full aggregation of historical data.
func (m *Module) handleRunAggregation(w http.ResponseWriter, r *http.Request) {
	user := m.requireAPIAuth(w, r)
	if user == nil {
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)

	datesProcessed, err := m.RunFullAggregation(r.Context())
	if err != nil {
		m.ctx.Logger.Error("full aggregation failed", "error", err, "user", user.Email)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "analytics_int.error_aggregation"), "error")
	} else {
		m.ctx.Logger.Info("full aggregation completed", "dates_processed", datesProcessed, "user", user.Email)
		msg := fmt.Sprintf(i18n.T(lang, "analytics_int.success_aggregation"), datesProcessed)
		m.ctx.Render.SetFlash(r, msg, "success")
	}

	http.Redirect(w, r, "/admin/internal-analytics", http.StatusSeeOther)
}
