package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/cache"
	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
	"ocms-go/internal/theme"
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
	Themes []theme.ThemeInfo
}

// ThemeSettingsData holds data for the theme settings template.
type ThemeSettingsData struct {
	Theme    theme.ThemeInfo
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

	if err := h.renderer.Render(w, r, "admin/themes_list", render.TemplateData{
		Title: i18n.T(lang, "nav.themes"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.themes"), URL: "/admin/themes", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Activate handles POST /admin/themes/activate - activates a theme.
func (h *ThemesHandler) Activate(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/themes", http.StatusSeeOther)
		return
	}

	themeName := r.FormValue("theme")
	if themeName == "" {
		h.renderer.SetFlash(r, "Theme name is required", "error")
		http.Redirect(w, r, "/admin/themes", http.StatusSeeOther)
		return
	}

	// Check if theme exists
	if !h.themeManager.HasTheme(themeName) {
		h.renderer.SetFlash(r, "Theme not found", "error")
		http.Redirect(w, r, "/admin/themes", http.StatusSeeOther)
		return
	}

	// Activate the theme in manager
	if err := h.themeManager.SetActiveTheme(themeName); err != nil {
		slog.Error("failed to activate theme", "theme", themeName, "error", err)
		h.renderer.SetFlash(r, "Failed to activate theme", "error")
		http.Redirect(w, r, "/admin/themes", http.StatusSeeOther)
		return
	}

	// Store the active theme in config
	now := time.Now()
	updatedBy := sql.NullInt64{Int64: user.ID, Valid: true}

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

	slog.Info("theme activated", "theme", themeName, "activated_by", user.ID)
	h.renderer.SetFlash(r, "Theme activated successfully", "success")
	http.Redirect(w, r, "/admin/themes", http.StatusSeeOther)
}

// Settings handles GET /admin/themes/{name}/settings - displays theme settings form.
func (h *ThemesHandler) Settings(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)
	themeName := chi.URLParam(r, "name")

	thm, err := h.themeManager.GetTheme(themeName)
	if err != nil {
		h.renderer.SetFlash(r, "Theme not found", "error")
		http.Redirect(w, r, "/admin/themes", http.StatusSeeOther)
		return
	}

	// Get theme info with active status
	activeTheme := h.themeManager.GetActiveTheme()
	themeInfo := theme.ThemeInfo{
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

	if err := h.renderer.Render(w, r, "admin/themes_settings", render.TemplateData{
		Title: thm.Config.Name + " Settings",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.themes"), URL: "/admin/themes"},
			{Label: thm.Config.Name + " Settings", URL: "/admin/themes/" + themeName + "/settings", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// SaveSettings handles PUT /admin/themes/{name}/settings - saves theme settings.
func (h *ThemesHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	themeName := chi.URLParam(r, "name")

	thm, err := h.themeManager.GetTheme(themeName)
	if err != nil {
		h.renderer.SetFlash(r, "Theme not found", "error")
		http.Redirect(w, r, "/admin/themes", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/themes/"+themeName+"/settings", http.StatusSeeOther)
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
		h.renderer.SetFlash(r, "Error saving settings", "error")
		http.Redirect(w, r, "/admin/themes/"+themeName+"/settings", http.StatusSeeOther)
		return
	}

	now := time.Now()
	updatedBy := sql.NullInt64{Int64: user.ID, Valid: true}
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
		h.renderer.SetFlash(r, "Error saving settings", "error")
		http.Redirect(w, r, "/admin/themes/"+themeName+"/settings", http.StatusSeeOther)
		return
	}

	// Invalidate theme settings cache
	if h.cacheManager != nil {
		h.cacheManager.InvalidateThemeSettings()
	}

	slog.Info("theme settings saved", "theme", themeName, "saved_by", user.ID)
	h.renderer.SetFlash(r, "Theme settings saved successfully", "success")
	http.Redirect(w, r, "/admin/themes/"+themeName+"/settings", http.StatusSeeOther)
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

	return settings
}
