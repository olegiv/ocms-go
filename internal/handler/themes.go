// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// ThemesHandler handles theme management routes.
type ThemesHandler struct {
	queries         *store.Queries
	renderer        *render.Renderer
	sessionManager  *scs.SessionManager
	themeManager    *theme.Manager
	cacheManager    *cache.Manager
	eventService    *service.EventService
	loginProtection *middleware.LoginProtection
}

// NewThemesHandler creates a new ThemesHandler. es may be nil, in which case
// theme management is logged only through slog; lp may be nil, in which case
// password confirmations are not throttled (tests only).
func NewThemesHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, tm *theme.Manager, cm *cache.Manager, es *service.EventService, lp *middleware.LoginProtection) *ThemesHandler {
	return &ThemesHandler{
		queries:         store.New(db),
		renderer:        renderer,
		sessionManager:  sm,
		themeManager:    tm,
		cacheManager:    cm,
		eventService:    es,
		loginProtection: lp,
	}
}

// logSecurityEvent records a theme-management action in the admin event log.
// Installing a theme installs code that runs with the site's privileges, so
// these entries use the security category, which excluded IPs cannot mute.
func (h *ThemesHandler) logSecurityEvent(r *http.Request, level, message string, metadata map[string]any) {
	if h.eventService == nil {
		return
	}
	if err := h.eventService.LogEvent(r.Context(), level, model.EventCategorySecurity, message,
		middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), metadata); err != nil {
		slog.ErrorContext(service.WithoutEventLog(r.Context()), "failed to log theme event", "error", err, "message", message, "user_id", middleware.GetUserID(r))
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
	lang := h.renderer.GetAdminLang(r)

	viewData := adminviews.ThemesListData{
		Themes:     convertThemeViewItems(h.themeManager.ListThemesWithActive()),
		IsDemoMode: middleware.IsDemoMode(),
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer,
		i18n.T(lang, "nav.themes"),
		themesBreadcrumbs(lang))
	renderTempl(w, r, adminviews.ThemesListPage(pc, viewData))
}

// Import handles POST /admin/themes/import - installs a new custom theme.
func (h *ThemesHandler) Import(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionThemeManagement, redirectAdminThemes) {
		return
	}
	lang := h.renderer.GetAdminLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, theme.MaxArchiveBytes+(1<<20))
	err := r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminThemes, i18n.T(lang, "themes.invalid_upload"))
		return
	}
	// An imported theme runs with the site's full privileges (templates with
	// safeHTML/safeURL, same-origin scripts), so every import re-authenticates
	// the administrator rather than relying on the session alone. The check
	// shares the login lockout, so a stolen session cannot guess the password
	// here any faster than at the login form.
	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	clientIP := middleware.GetClientIP(r)
	if h.loginProtection != nil {
		if locked, _ := h.loginProtection.IsAccountLocked(user.Email, clientIP); locked {
			slog.Warn("theme import refused: account locked", "user_id", user.ID)
			h.logSecurityEvent(r, model.EventLevelWarning, "Theme import refused: too many failed password confirmations", nil)
			flashError(w, r, h.renderer, redirectAdminThemes, i18n.T(lang, "themes.import_locked"))
			return
		}
	}
	confirmed, err := passwordConfirmed(r.FormValue("confirm_password"), user)
	if err != nil {
		slog.Error("theme import: password could not be verified", "error", err, "user_id", user.ID)
		h.logSecurityEvent(r, model.EventLevelError, "Theme import refused: password could not be verified", nil)
		flashError(w, r, h.renderer, redirectAdminThemes, i18n.T(lang, "themes.import_password_unverified"))
		return
	}
	if !confirmed {
		if h.loginProtection != nil {
			h.loginProtection.RecordFailedAttempt(user.Email, clientIP)
		}
		slog.Warn("theme import refused: password confirmation failed", "user_id", user.ID)
		h.logSecurityEvent(r, model.EventLevelWarning, "Theme import refused: password confirmation failed", nil)
		flashError(w, r, h.renderer, redirectAdminThemes, i18n.T(lang, "themes.import_password_invalid"))
		return
	}
	if h.loginProtection != nil {
		h.loginProtection.RecordSuccessfulLogin(user.Email, clientIP)
	}
	file, header, err := r.FormFile("archive")
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminThemes, i18n.T(lang, "themes.archive_required"))
		return
	}
	defer func() { _ = file.Close() }()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".tar.gz") || header.Size > theme.MaxArchiveBytes {
		flashError(w, r, h.renderer, redirectAdminThemes, i18n.T(lang, "themes.invalid_upload"))
		return
	}
	name, err := h.themeManager.ImportArchive(file)
	if err != nil {
		slog.Warn("failed to import theme", "error", err, "user_id", middleware.GetUserID(r))
		flashError(w, r, h.renderer, redirectAdminThemes, themeErrorMessage(lang, err, "themes.import_failed", "themes.import_error"))
		return
	}
	h.invalidateThemeCaches()
	slog.Info("admin imported theme", "theme", name, "user_id", middleware.GetUserID(r))
	h.logSecurityEvent(r, model.EventLevelInfo, "Theme imported: "+name, map[string]any{"theme": name, "archive": header.Filename, "size": header.Size})
	flashSuccess(w, r, h.renderer, redirectAdminThemes, i18n.T(lang, "themes.import_success"))
}

// Delete handles POST /admin/themes/{name}/delete - removes an inactive custom theme.
func (h *ThemesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionThemeManagement, redirectAdminThemes) {
		return
	}
	lang := h.renderer.GetAdminLang(r)
	name := chi.URLParam(r, "name")
	if err := h.themeManager.RemoveTheme(name); err != nil {
		slog.Warn("failed to delete theme", "theme", name, "error", err, "user_id", middleware.GetUserID(r))
		flashError(w, r, h.renderer, redirectAdminThemes, themeErrorMessage(lang, err, "themes.delete_failed", "themes.delete_error"))
		return
	}
	h.invalidateThemeCaches()
	slog.Info("admin deleted theme", "theme", name, "user_id", middleware.GetUserID(r))
	h.logSecurityEvent(r, model.EventLevelInfo, "Theme deleted: "+name, map[string]any{"theme": name})
	flashSuccess(w, r, h.renderer, redirectAdminThemes, i18n.T(lang, "themes.delete_success"))
}

// passwordConfirmed reports whether password is the current password of
// user. An empty submission never confirms; an error means the stored hash
// could not be checked, which is distinct from a wrong password.
func passwordConfirmed(password string, user *store.User) (bool, error) {
	if password == "" {
		return false, nil
	}
	return auth.CheckPassword(password, user.PasswordHash)
}

// themeErrorMessage returns the flash text for a failed theme operation.
// Validation problems are shown verbatim so the administrator can fix the
// archive; internal errors, which may name server paths, are replaced by the
// generic message and stay in the server log.
func themeErrorMessage(lang string, err error, detailKey, genericKey string) string {
	var validation theme.ValidationError
	if errors.As(err, &validation) {
		return i18n.T(lang, detailKey, validation.Error())
	}
	return i18n.T(lang, genericKey)
}

func (h *ThemesHandler) invalidateThemeCaches() {
	if h.cacheManager != nil {
		h.cacheManager.InvalidateThemeSettings()
		h.cacheManager.InvalidateContent()
	}
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
	lang := h.renderer.GetAdminLang(r)
	themeName := chi.URLParam(r, "name")

	thm, err := h.themeManager.GetTheme(themeName)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminThemes, "Theme not found")
		return
	}

	// Get active status
	activeTheme := h.themeManager.GetActiveTheme()
	isActive := activeTheme != nil && activeTheme.Name == thm.Name

	// Load saved settings from config
	settings := h.loadThemeSettings(r, themeName)

	// Fill in defaults for any missing settings
	for _, setting := range thm.Config.Settings {
		if _, ok := settings[setting.Key]; !ok {
			settings[setting.Key] = setting.Default
		}
	}

	viewData := convertThemeSettingsViewData(thm, themeName, isActive, settings, make(map[string]string))

	title := thm.Config.Name + " Settings"
	pc := buildPageContext(r, h.sessionManager, h.renderer,
		title,
		themeSettingsBreadcrumbs(lang, thm.Config.Name, themeName))
	renderTempl(w, r, adminviews.ThemeSettingsPage(pc, viewData))
}

// SaveSettings handles PUT /admin/themes/{name}/settings - saves theme settings.
func (h *ThemesHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		flashError(w, r, h.renderer, redirectAdminThemes, middleware.DemoModeMessageDetailed(middleware.RestrictionThemeSettings))
		return
	}

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
