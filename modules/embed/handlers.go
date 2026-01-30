// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/modules/embed/providers"
)

// ProviderListItem represents a provider in the list view.
type ProviderListItem struct {
	ID          string
	Name        string
	Description string
	IsEnabled   bool
	IsConfigured bool
}

// handleList handles GET /admin/embed - shows the list of embed providers.
func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Load settings for all providers
	allSettings, err := loadAllSettings(m.ctx.DB)
	if err != nil {
		m.ctx.Logger.Error("failed to load embed settings", "error", err)
	}

	// Build settings map by provider ID
	settingsMap := make(map[string]*ProviderSettings)
	for _, ps := range allSettings {
		settingsMap[ps.ProviderID] = ps
	}

	// Build provider list
	var providerList []ProviderListItem
	for _, p := range m.providers {
		item := ProviderListItem{
			ID:          p.ID(),
			Name:        p.Name(),
			Description: p.Description(),
		}

		if ps, ok := settingsMap[p.ID()]; ok {
			item.IsEnabled = ps.IsEnabled
			// Check if provider is configured (has required settings)
			if err := p.Validate(ps.Settings); err == nil {
				item.IsConfigured = true
			}
		}

		providerList = append(providerList, item)
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_embed_list", render.TemplateData{
		Title: i18n.T(lang, "embed.title"),
		User:  user,
		Data: map[string]any{
			"Providers": providerList,
		},
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "embed.title"), URL: "/admin/embed", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleProviderSettings handles GET /admin/embed/{provider} - shows provider settings.
func (m *Module) handleProviderSettings(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "provider")
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Find provider
	provider := m.getProvider(providerID)
	if provider == nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "embed.error_provider_not_found"), "error")
		http.Redirect(w, r, "/admin/embed", http.StatusSeeOther)
		return
	}

	// Load current settings
	ps, err := loadProviderSettings(m.ctx.DB, providerID)
	if err != nil {
		m.ctx.Logger.Error("failed to load provider settings", "error", err, "provider", providerID)
		ps = &ProviderSettings{
			ProviderID: providerID,
			Settings:   make(map[string]string),
		}
	}

	// Get schema with current values
	schema := provider.SettingsSchema()
	for i := range schema {
		if val, ok := ps.Settings[schema[i].ID]; ok {
			schema[i].Default = val
		}
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_embed_provider", render.TemplateData{
		Title: provider.Name(),
		User:  user,
		Data: map[string]any{
			"Provider":  provider,
			"Schema":    schema,
			"Settings":  ps,
			"IsEnabled": ps.IsEnabled,
		},
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "embed.title"), URL: "/admin/embed"},
			{Label: provider.Name(), URL: "/admin/embed/" + providerID, Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleSaveProviderSettings handles POST /admin/embed/{provider} - saves provider settings.
func (m *Module) handleSaveProviderSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := m.getProviderContext(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		m.ctx.Logger.Error("failed to parse form", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "embed.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/embed/"+ctx.ProviderID, http.StatusSeeOther)
		return
	}

	// Build settings from form
	settings := make(map[string]string)
	for _, field := range ctx.Provider.SettingsSchema() {
		settings[field.ID] = strings.TrimSpace(r.FormValue(field.ID))
	}

	isEnabled := r.FormValue("is_enabled") == "1"

	// Validate if enabling
	if isEnabled {
		if err := ctx.Provider.Validate(settings); err != nil {
			m.ctx.Render.SetFlash(r, err.Error(), "error")
			http.Redirect(w, r, "/admin/embed/"+ctx.ProviderID, http.StatusSeeOther)
			return
		}
	}

	// Load existing settings to preserve position
	existingPS, _ := loadProviderSettings(m.ctx.DB, ctx.ProviderID)
	position := 0
	if existingPS != nil {
		position = existingPS.Position
	}

	// Save settings
	ps := &ProviderSettings{
		ProviderID: ctx.ProviderID,
		Settings:   settings,
		IsEnabled:  isEnabled,
		Position:   position,
	}

	if err := saveProviderSettings(m.ctx.DB, ps); err != nil {
		m.ctx.Logger.Error("failed to save provider settings", "error", err, "provider", ctx.ProviderID)
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "embed.error_save"), "error")
		http.Redirect(w, r, "/admin/embed/"+ctx.ProviderID, http.StatusSeeOther)
		return
	}

	// Reload module settings
	if err := m.reloadSettings(); err != nil {
		m.ctx.Logger.Warn("failed to reload embed settings", "error", err)
	}

	m.ctx.Logger.Info("embed provider settings updated",
		"user", ctx.User.Email,
		"provider", ctx.ProviderID,
		"enabled", isEnabled,
	)

	m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "embed.success_save"), "success")
	http.Redirect(w, r, "/admin/embed/"+ctx.ProviderID, http.StatusSeeOther)
}

// handleToggleProvider handles POST /admin/embed/{provider}/toggle - toggles provider.
func (m *Module) handleToggleProvider(w http.ResponseWriter, r *http.Request) {
	ctx, ok := m.getProviderContext(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		m.ctx.Logger.Error("failed to parse form", "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "embed.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/embed", http.StatusSeeOther)
		return
	}

	enabled := r.FormValue("enabled") == "1"

	// If enabling, validate settings first
	if enabled {
		ps, err := loadProviderSettings(m.ctx.DB, ctx.ProviderID)
		if err != nil || ps == nil {
			m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "embed.error_not_configured"), "error")
			http.Redirect(w, r, "/admin/embed/"+ctx.ProviderID, http.StatusSeeOther)
			return
		}

		if err := ctx.Provider.Validate(ps.Settings); err != nil {
			m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "embed.error_configure_first"), "error")
			http.Redirect(w, r, "/admin/embed/"+ctx.ProviderID, http.StatusSeeOther)
			return
		}
	}

	if err := toggleProvider(m.ctx.DB, ctx.ProviderID, enabled); err != nil {
		m.ctx.Logger.Error("failed to toggle provider", "error", err, "provider", ctx.ProviderID)
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "embed.error_toggle"), "error")
		http.Redirect(w, r, "/admin/embed", http.StatusSeeOther)
		return
	}

	// Reload module settings
	if err := m.reloadSettings(); err != nil {
		m.ctx.Logger.Warn("failed to reload embed settings", "error", err)
	}

	m.ctx.Logger.Info("embed provider toggled",
		"user", ctx.User.Email,
		"provider", ctx.ProviderID,
		"enabled", enabled,
	)

	statusKey := "embed.success_disabled"
	if enabled {
		statusKey = "embed.success_enabled"
	}
	m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, statusKey), "success")
	http.Redirect(w, r, "/admin/embed", http.StatusSeeOther)
}

// getProvider finds a provider by ID.
func (m *Module) getProvider(id string) providers.Provider {
	for _, p := range m.providers {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// providerRequestContext holds common request context for provider handlers.
type providerRequestContext struct {
	ProviderID string
	Provider   providers.Provider
	User       *store.User
	Lang       string
}

// getProviderContext extracts and validates common provider request context.
// Returns false if validation failed (response already written).
func (m *Module) getProviderContext(w http.ResponseWriter, r *http.Request) (providerRequestContext, bool) {
	providerID := chi.URLParam(r, "provider")
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return providerRequestContext{}, false
	}

	provider := m.getProvider(providerID)
	if provider == nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "embed.error_provider_not_found"), "error")
		http.Redirect(w, r, "/admin/embed", http.StatusSeeOther)
		return providerRequestContext{}, false
	}

	return providerRequestContext{
		ProviderID: providerID,
		Provider:   provider,
		User:       user,
		Lang:       lang,
	}, true
}
