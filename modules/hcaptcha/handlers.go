// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package hcaptcha

import (
	"net/http"
	"strings"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// handleDashboard handles GET /admin/hcaptcha - shows the hCaptcha settings page.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Mask secret key for display
	displaySettings := struct {
		Enabled       bool
		SiteKey       string
		SecretKey     string
		SecretKeyMask string
		Theme         string
		Size          string
	}{
		Enabled:       m.settings.Enabled,
		SiteKey:       m.settings.SiteKey,
		SecretKey:     m.settings.SecretKey,
		SecretKeyMask: maskSecretKey(m.settings.SecretKey),
		Theme:         m.settings.Theme,
		Size:          m.settings.Size,
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_hcaptcha", render.TemplateData{
		Title: i18n.T(lang, "hcaptcha.title"),
		User:  user,
		Data:  displaySettings,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "hcaptcha.title"), URL: "/admin/hcaptcha", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleSaveSettings handles POST /admin/hcaptcha - saves hCaptcha settings.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		m.ctx.Logger.Error("failed to parse form", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "hcaptcha.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/hcaptcha", http.StatusSeeOther)
		return
	}

	// Build new settings from form
	newSettings := &Settings{
		Enabled: r.FormValue("enabled") == "1",
		SiteKey: strings.TrimSpace(r.FormValue("site_key")),
		Theme:   strings.TrimSpace(r.FormValue("theme")),
		Size:    strings.TrimSpace(r.FormValue("size")),
	}

	// Handle secret key - if unchanged placeholder, keep old value
	secretKey := strings.TrimSpace(r.FormValue("secret_key"))
	if secretKey == "" || isPlaceholder(secretKey) {
		newSettings.SecretKey = m.settings.SecretKey
	} else {
		newSettings.SecretKey = secretKey
	}

	// Set defaults
	if newSettings.Theme == "" {
		newSettings.Theme = "light"
	}
	if newSettings.Size == "" {
		newSettings.Size = "normal"
	}

	// Validate required fields when enabled
	var validationErrors []string

	if newSettings.Enabled {
		if newSettings.SiteKey == "" {
			validationErrors = append(validationErrors, i18n.T(lang, "hcaptcha.error_site_key_required"))
		}
		if newSettings.SecretKey == "" {
			validationErrors = append(validationErrors, i18n.T(lang, "hcaptcha.error_secret_key_required"))
		}
	}

	if len(validationErrors) > 0 {
		m.ctx.Render.SetFlash(r, strings.Join(validationErrors, " "), "error")
		http.Redirect(w, r, "/admin/hcaptcha", http.StatusSeeOther)
		return
	}

	// Save to database
	if err := saveSettings(m.ctx.DB, newSettings); err != nil {
		m.ctx.Logger.Error("failed to save hCaptcha settings", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(lang, "hcaptcha.error_save"), "error")
		http.Redirect(w, r, "/admin/hcaptcha", http.StatusSeeOther)
		return
	}

	// Update in-memory settings (preserve env overrides)
	if m.ctx.Config.HCaptchaSiteKey != "" {
		newSettings.SiteKey = m.ctx.Config.HCaptchaSiteKey
	}
	if m.ctx.Config.HCaptchaSecretKey != "" {
		newSettings.SecretKey = m.ctx.Config.HCaptchaSecretKey
	}
	m.settings = newSettings

	m.ctx.Logger.Info("hCaptcha settings updated",
		"user", user.Email,
		"enabled", newSettings.Enabled,
	)

	m.ctx.Render.SetFlash(r, i18n.T(lang, "hcaptcha.success_save"), "success")
	http.Redirect(w, r, "/admin/hcaptcha", http.StatusSeeOther)
}

// maskSecretKey masks most of a secret key for display.
func maskSecretKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

// isPlaceholder checks if a value is a masked placeholder.
func isPlaceholder(value string) bool {
	return strings.Contains(value, "****")
}
