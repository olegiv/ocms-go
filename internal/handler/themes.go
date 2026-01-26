// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
)

// ThemesHandler handles theme management routes.
type ThemesHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	themeManager   *theme.Manager
	cacheManager   *cache.Manager
}

// NewThemesHandler creates a new ThemesHandler.
func NewThemesHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, tm *theme.Manager, cm *cache.Manager) *ThemesHandler {
	return &ThemesHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		themeManager:   tm,
		cacheManager:   cm,
	}
}

// ThemeListData holds data for the theme list template.
type ThemeListData struct {
	Themes []theme.Info
}

// ThemeSettingsData holds data for the theme settings template.
type ThemeSettingsData struct {
	Theme    theme.Info
	Settings map[string]string
	Errors   map[string]string
}

// List handles GET /admin/themes - displays available themes.
func (h *ThemesHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	themes := h.themeManager.ListThemesWithActive()

	data := ThemeListData{
		Themes: themes,
	}

	h.renderer.RenderPage(w, r, "admin/themes_list", render.TemplateData{
		Title: i18n.T(lang, "nav.themes"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.themes"), URL: redirectAdminThemes, Active: true},
		},
	})
}

// Activate handles POST /admin/themes/activate - activates a theme.
func (h *ThemesHandler) Activate(w http.ResponseWriter, r *http.Request) {
	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminThemes) {
		return
	}

	themeName := r.FormValue("theme")
	if themeName == "" {
		flashError(w, r, h.renderer, redirectAdminThemes, "Theme name is required")
		return
	}

	// Check if theme exists
	if !h.themeManager.HasTheme(themeName) {
		flashError(w, r, h.renderer, redirectAdminThemes, "Theme not found")
		return
	}

	// Activate the theme in manager
	if err := h.themeManager.SetActiveTheme(themeName); err != nil {
		slog.Error("failed to activate theme", "theme", themeName, "error", err)
		flashError(w, r, h.renderer, redirectAdminThemes, "Failed to activate theme")
		return
	}

	// Store the active theme in config
	now := time.Now()
	userID := middleware.GetUserID(r)
	updatedBy := sql.NullInt64{Int64: userID, Valid: userID > 0}

	_, err := h.queries.UpsertConfig(r.Context(), store.UpsertConfigParams{
		Key:         "active_theme",
		Value:       themeName,
		Type:        "string",
		Description: "Currently active frontend theme",
		UpdatedAt:   now,
		UpdatedBy:   updatedBy,
	})
	if err != nil {
		slog.Error("failed to save active theme to config", "error", err)
		// Theme is still activated in memory, just not persisted
	}

	// Invalidate config cache (theme settings are cached with config)
	if h.cacheManager != nil {
		h.cacheManager.InvalidateConfig()
	}

	slog.Info("theme activated", "theme", themeName, "activated_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminThemes, "Theme activated successfully")
}

// Settings handles GET /admin/themes/{name}/settings - displays theme settings form.
func (h *ThemesHandler) Settings(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)
	themeName := chi.URLParam(r, "name")

	thm, err := h.themeManager.GetTheme(themeName)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminThemes, "Theme not found")
		return
	}

	// Get theme info with active status
	activeTheme := h.themeManager.GetActiveTheme()
	themeInfo := theme.Info{
		Name:     thm.Name,
		Config:   thm.Config,
		IsActive: activeTheme != nil && activeTheme.Name == thm.Name,
	}

	// Load saved settings from config
	settings := h.loadThemeSettings(r, themeName)

	// Fill in defaults for any missing settings
	for _, setting := range thm.Config.Settings {
		if _, ok := settings[setting.Key]; !ok {
			settings[setting.Key] = setting.Default
		}
	}

	data := ThemeSettingsData{
		Theme:    themeInfo,
		Settings: settings,
		Errors:   make(map[string]string),
	}

	h.renderer.RenderPage(w, r, "admin/themes_settings", render.TemplateData{
		Title: thm.Config.Name + " Settings",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.themes"), URL: redirectAdminThemes},
			{Label: thm.Config.Name + " Settings", URL: redirectAdminThemesSlash + themeName + pathSettings, Active: true},
		},
	})
}

// SaveSettings handles PUT /admin/themes/{name}/settings - saves theme settings.
func (h *ThemesHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	themeName := chi.URLParam(r, "name")

	thm, err := h.themeManager.GetTheme(themeName)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminThemes, "Theme not found")
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminThemesSlash+themeName+pathSettings) {
		return
	}

	// Collect settings values from form
	settings := make(map[string]string)
	for _, setting := range thm.Config.Settings {
		value := r.FormValue(setting.Key)
		if value == "" {
			value = setting.Default
		}
		settings[setting.Key] = value
	}

	// Save settings to config as JSON
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		slog.Error("failed to marshal theme settings", "error", err)
		flashError(w, r, h.renderer, redirectAdminThemesSlash+themeName+pathSettings, "Error saving settings")
		return
	}

	now := time.Now()
	userID := middleware.GetUserID(r)
	updatedBy := sql.NullInt64{Int64: userID, Valid: userID > 0}
	configKey := "theme_settings_" + themeName

	_, err = h.queries.UpsertConfig(r.Context(), store.UpsertConfigParams{
		Key:         configKey,
		Value:       string(settingsJSON),
		Type:        "json",
		Description: "Settings for " + thm.Config.Name + " theme",
		UpdatedAt:   now,
		UpdatedBy:   updatedBy,
	})
	if err != nil {
		slog.Error("failed to save theme settings", "theme", themeName, "error", err)
		flashError(w, r, h.renderer, redirectAdminThemesSlash+themeName+pathSettings, "Error saving settings")
		return
	}

	// Invalidate theme settings cache
	if h.cacheManager != nil {
		h.cacheManager.InvalidateThemeSettings()
	}

	slog.Info("theme settings saved", "theme", themeName, "saved_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminThemes, "Theme settings saved successfully")
}

// loadThemeSettings loads theme settings from the config table.
func (h *ThemesHandler) loadThemeSettings(r *http.Request, themeName string) map[string]string {
	configKey := "theme_settings_" + themeName

	config, err := h.queries.GetConfigByKey(r.Context(), configKey)
	if err != nil {
		// No settings saved yet, return empty map
		return make(map[string]string)
	}

	var settings map[string]string
	if err := json.Unmarshal([]byte(config.Value), &settings); err != nil {
		slog.Warn("failed to unmarshal theme settings", "theme", themeName, "error", err)
		return make(map[string]string)
	}

	// Ensure we never return nil (could happen if JSON was "null")
	if settings == nil {
		return make(map[string]string)
	}

	return settings
}
