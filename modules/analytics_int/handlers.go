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
	viewData := AnalyticsIntViewData{
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

	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "analytics_int.title"), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "analytics_int.title"), URL: "/admin/internal-analytics", Active: true},
	})
	render.Templ(w, r, AnalyticsIntPage(pc, viewData))
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

// maxReadBodySize is the maximum size of a read beacon request body.
const maxReadBodySize = 1024 // 1 KB

// handleRecordRead handles POST /analytics/read - records a read event.
// This is a public endpoint that receives read beacons from the frontend JS.
func (m *Module) handleRecordRead(w http.ResponseWriter, r *http.Request) {
	// Skip if tracking is disabled
	if m.settings == nil || !m.settings.Enabled {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxReadBodySize)

	var req ReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Path == "" || req.ScrollDepth < 60 || req.TimeOnPage < 5 {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Validate path format: must start with /, no traversal, reasonable length
	if req.Path[0] != '/' || len(req.Path) > 512 || strings.Contains(req.Path, "..") {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Cap scroll depth to 100
	if req.ScrollDepth > 100 {
		req.ScrollDepth = 100
	}

	// Extract identity before spawning goroutine to avoid capturing *http.Request
	id := m.extractIdentity(r)
	if id == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Record the read asynchronously
	go m.recordReadWithIdentity(id, &req)

	w.WriteHeader(http.StatusNoContent)
}

// reportPerPage is the number of items per page in the views/reads report.
const reportPerPage = 25

// handleViewsReadsReport renders the admin views/reads report page.
func (m *Module) handleViewsReadsReport(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)
	dateRange, startDate, endDate := parseDateRangeParam(r)

	// Parse pagination
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	offset := (page - 1) * reportPerPage

	rows := m.getPageStatsReport(r.Context(), startDate, endDate, reportPerPage, offset)
	totalCount := m.getPageStatsReportCount(r.Context(), startDate, endDate)
	totalPages := (totalCount + reportPerPage - 1) / reportPerPage

	viewData := ReportViewData{
		Rows:       rows,
		DateRange:  dateRange,
		Page:       page,
		TotalPages: totalPages,
		TotalCount: totalCount,
	}

	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "analytics_int.views_reads_report"), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "analytics_int.title"), URL: "/admin/internal-analytics"},
		{Label: i18n.T(lang, "analytics_int.views_reads_report"), URL: "/admin/internal-analytics/report", Active: true},
	})
	render.Templ(w, r, AnalyticsIntReportPage(pc, viewData))
}

// handleSaveSettings saves module settings.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		m.ctx.Render.SetFlash(r, middleware.DemoModeMessageDetailed(middleware.RestrictionModuleSettings), "error")
		http.Redirect(w, r, "/admin/internal-analytics", http.StatusSeeOther)
		return
	}

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
	m.settings.ShowPostStats = r.FormValue("show_post_stats") == "1"

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
