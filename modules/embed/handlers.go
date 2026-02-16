// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/modules/embed/providers"
)

// ProviderListItem represents a provider in the list view.
type ProviderListItem struct {
	ID           string
	Name         string
	Description  string
	IsEnabled    bool
	IsConfigured bool
}

// handleList handles GET /admin/embed - shows the list of embed providers.
func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
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

	viewData := EmbedListViewData{
		Providers: providerList,
	}

	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "embed.title"), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "embed.title"), URL: "/admin/embed", Active: true},
	})
	render.Templ(w, r, EmbedListPage(pc, viewData))
}

// handleProviderSettings handles GET /admin/embed/{provider} - shows provider settings.
func (m *Module) handleProviderSettings(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "provider")
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

	viewData := EmbedProviderViewData{
		ProviderID:   provider.ID(),
		ProviderName: provider.Name(),
		ProviderDesc: provider.Description(),
		IsEnabled:    ps.IsEnabled,
		Schema:       schema,
	}

	pc := m.ctx.Render.BuildPageContext(r, provider.Name(), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "embed.title"), URL: "/admin/embed"},
		{Label: provider.Name(), URL: "/admin/embed/" + providerID, Active: true},
	})
	render.Templ(w, r, EmbedProviderPage(pc, viewData))
}

// handleSaveProviderSettings handles POST /admin/embed/{provider} - saves provider settings.
func (m *Module) handleSaveProviderSettings(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		providerID := chi.URLParam(r, "provider")
		m.ctx.Render.SetFlash(r, middleware.DemoModeMessageDetailed(middleware.RestrictionModuleSettings), "error")
		http.Redirect(w, r, "/admin/embed/"+providerID, http.StatusSeeOther)
		return
	}

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
		if err := m.validateProviderEnableSettings(ctx.ProviderID, ctx.Provider, settings); err != nil {
			m.ctx.Render.SetFlash(r, err.Error(), "error")
			http.Redirect(w, r, "/admin/embed/"+ctx.ProviderID, http.StatusSeeOther)
			return
		}
	}

	// Load existing settings to preserve position
	existingPS, _ := loadProviderSettings(m.ctx.DB, ctx.ProviderID)
	position := 0
	oldEndpoint := ""
	if existingPS != nil {
		position = existingPS.Position
		oldEndpoint = strings.TrimSpace(existingPS.Settings["api_endpoint"])
	}
	newEndpoint := strings.TrimSpace(settings["api_endpoint"])
	oldScheme, oldHost := embedEndpointMetadata(oldEndpoint)
	newScheme, newHost := embedEndpointMetadata(newEndpoint)
	endpointChanged := oldScheme != newScheme || oldHost != newHost

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
	if m.ctx.Events != nil {
		metadata := buildEmbedSettingsAuditMetadata(ctx.ProviderID, isEnabled, settings, endpointChanged, oldScheme, oldHost, newScheme, newHost)
		_ = m.ctx.Events.LogSecurityEvent(
			r.Context(),
			model.EventLevelInfo,
			"Embed provider settings updated",
			&ctx.User.ID,
			middleware.GetClientIP(r),
			middleware.GetRequestURL(r),
			metadata,
		)
		if endpointChanged {
			m.ctx.Logger.Warn(
				"embed provider endpoint changed",
				"provider", ctx.ProviderID,
				"old_scheme", oldScheme,
				"old_host", oldHost,
				"new_scheme", newScheme,
				"new_host", newHost,
				"updated_by", ctx.User.Email,
			)
			_ = m.ctx.Events.LogSecurityEvent(
				r.Context(),
				model.EventLevelWarning,
				"Embed provider endpoint changed",
				&ctx.User.ID,
				middleware.GetClientIP(r),
				middleware.GetRequestURL(r),
				map[string]any{
					"provider":         ctx.ProviderID,
					"endpoint_changed": true,
					"old_scheme":       oldScheme,
					"old_host":         oldHost,
					"new_scheme":       newScheme,
					"new_host":         newHost,
				},
			)
		}
	}

	m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "embed.success_save"), "success")
	http.Redirect(w, r, "/admin/embed/"+ctx.ProviderID, http.StatusSeeOther)
}

// handleToggleProvider handles POST /admin/embed/{provider}/toggle - toggles provider.
func (m *Module) handleToggleProvider(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		m.ctx.Render.SetFlash(r, middleware.DemoModeMessageDetailed(middleware.RestrictionModuleSettings), "error")
		http.Redirect(w, r, "/admin/embed", http.StatusSeeOther)
		return
	}

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

		if err := m.validateProviderEnableSettings(ctx.ProviderID, ctx.Provider, ps.Settings); err != nil {
			m.ctx.Render.SetFlash(r, err.Error(), "error")
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
	if m.ctx.Events != nil {
		_ = m.ctx.Events.LogSecurityEvent(
			r.Context(),
			model.EventLevelInfo,
			"Embed provider toggled",
			&ctx.User.ID,
			middleware.GetClientIP(r),
			middleware.GetRequestURL(r),
			map[string]any{
				"provider": ctx.ProviderID,
				"enabled":  enabled,
			},
		)
	}

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

func buildEmbedSettingsAuditMetadata(
	providerID string,
	enabled bool,
	settings map[string]string,
	endpointChanged bool,
	oldScheme, oldHost, newScheme, newHost string,
) map[string]any {
	metadata := map[string]any{
		"provider":         providerID,
		"enabled":          enabled,
		"endpoint_changed": endpointChanged,
	}

	if settings == nil {
		return metadata
	}

	if endpoint := strings.TrimSpace(settings["api_endpoint"]); endpoint != "" {
		metadata["api_endpoint"] = endpoint
	}
	if endpointChanged {
		metadata["old_scheme"] = oldScheme
		metadata["old_host"] = oldHost
		metadata["new_scheme"] = newScheme
		metadata["new_host"] = newHost
	}
	if apiKey, ok := settings["api_key"]; ok {
		metadata["has_api_key"] = strings.TrimSpace(apiKey) != ""
	}

	return metadata
}

func embedEndpointMetadata(rawEndpoint string) (scheme string, host string) {
	parsed, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil || parsed == nil {
		return "", ""
	}

	return strings.ToLower(strings.TrimSpace(parsed.Scheme)), strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func (m *Module) validateProviderRuntimePolicy(providerID string, settings map[string]string) error {
	if m == nil {
		return nil
	}
	if providerID != "dify" || len(m.allowedUpstreamHosts) == 0 {
		return nil
	}

	endpoint := strings.TrimSpace(settings["api_endpoint"])
	if endpoint == "" {
		return nil
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("API endpoint is invalid: %w", err)
	}
	if !m.isUpstreamHostAllowed(parsedEndpoint.Hostname()) {
		return fmt.Errorf("API endpoint host %q is not allowed by OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS policy", parsedEndpoint.Hostname())
	}
	return nil
}

func (m *Module) validateProviderEnableSettings(providerID string, provider providers.Provider, settings map[string]string) error {
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	if err := provider.Validate(settings); err != nil {
		return err
	}
	return m.validateProviderRuntimePolicy(providerID, settings)
}
