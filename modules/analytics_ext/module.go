// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package analytics_ext provides an external statistics tracking module for oCMS.
// Supports Google Analytics 4 (GA4), Google Tag Manager (GTM), and Matomo.
package analytics_ext

import (
	"database/sql"
	"embed"
	"html/template"
	"net/url"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for the external analytics module.
type Module struct {
	module.BaseModule
	ctx      *module.Context
	settings *Settings

	// settingsMu guards settings: templates and the CSP read it on every
	// request while the settings form replaces it. Settings values are
	// replaced, never modified, so a reader may keep using the pointer it
	// got. TestSettingsAccessHoldsTheLock checks that every access to the
	// field takes the lock.
	settingsMu sync.RWMutex
}

// currentSettings returns the settings in effect, or nil before Init.
func (m *Module) currentSettings() *Settings {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.settings
}

// storeSettings replaces the settings in effect.
func (m *Module) storeSettings(settings *Settings) {
	m.settingsMu.Lock()
	m.settings = settings
	m.settingsMu.Unlock()
}

// New creates a new instance of the external analytics module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"analytics_ext",
			"1.0.0",
			"External Analytics",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	// Load settings from database
	settings, err := loadSettings(ctx.DB)
	if err != nil {
		ctx.Logger.Warn("failed to load analytics_ext settings, using defaults", "error", err)
		settings = &Settings{}
	}
	m.storeSettings(settings)
	m.warnMalformedSettings()

	m.ctx.Logger.Info("External Analytics module initialized",
		"ga4_enabled", settings.GA4Enabled,
		"gtm_enabled", settings.GTMEnabled,
		"matomo_enabled", settings.MatomoEnabled,
	)
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("External Analytics module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for analytics module
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/external-analytics", m.handleDashboard)
	r.Post("/external-analytics", m.handleSaveSettings)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// analyticsExtHead returns tracking scripts for the <head> section
		"analyticsExtHead": func(args ...any) template.HTML {
			return m.renderHeadScripts(firstStringArg(args...))
		},
		// analyticsExtBody returns tracking scripts for the end of <body>
		"analyticsExtBody": func(args ...any) template.HTML {
			return m.renderBodyScripts(firstStringArg(args...))
		},
	}
}

func firstStringArg(args ...any) string {
	if len(args) == 0 {
		return ""
	}
	s, _ := args[0].(string)
	return s
}

// CSPSources allows Google Tag Manager and Google Analytics while the module
// is active, whether or not a tracker is configured, and the configured Matomo
// server while Matomo tracking is enabled with a valid URL and Site ID: the
// tracker loads matomo.js from it and sends hits to it.
func (m *Module) CSPSources() map[string][]string {
	sources := map[string][]string{
		"script-src": {
			"https://www.googletagmanager.com", "https://*.googletagmanager.com",
			"https://www.google-analytics.com",
		},
		"style-src": {"https://www.googletagmanager.com"},
		// GTM's <noscript> fallback is an iframe.
		"frame-src": {"https://www.googletagmanager.com"},
		"connect-src": {
			"https://www.google-analytics.com", "https://*.google-analytics.com",
			"https://*.analytics.google.com",
			"https://www.googletagmanager.com", "https://*.googletagmanager.com",
		},
	}
	if settings := m.currentSettings(); settings != nil && matomoActive(settings) {
		if origin := matomoOrigin(settings.MatomoURL); origin != "" {
			sources["script-src"] = append(sources["script-src"], origin)
			sources["connect-src"] = append(sources["connect-src"], origin)
		}
	}
	return sources
}

// matomoOrigin returns the scheme and host of a Matomo URL that passed
// validMatomoURL, or "" when it cannot be parsed.
func matomoOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/external-analytics"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "External Analytics"
}

// TranslationsFS returns the embedded filesystem containing module translations.
func (m *Module) TranslationsFS() embed.FS {
	return localesFS
}

// Migrations returns database migrations for the module.
func (m *Module) Migrations() []module.Migration {
	return []module.Migration{
		{
			Version:     1,
			Description: "Create analytics_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS analytics_settings (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						ga4_enabled INTEGER NOT NULL DEFAULT 0,
						ga4_measurement_id TEXT NOT NULL DEFAULT '',
						gtm_enabled INTEGER NOT NULL DEFAULT 0,
						gtm_container_id TEXT NOT NULL DEFAULT '',
						matomo_enabled INTEGER NOT NULL DEFAULT 0,
						matomo_url TEXT NOT NULL DEFAULT '',
						matomo_site_id TEXT NOT NULL DEFAULT '',
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					INSERT OR IGNORE INTO analytics_settings (id) VALUES (1);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS analytics_settings`)
				return err
			},
		},
	}
}

// ReloadSettings reloads settings from the database.
func (m *Module) ReloadSettings() error {
	settings, err := loadSettings(m.ctx.DB)
	if err != nil {
		return err
	}
	m.storeSettings(settings)
	m.warnMalformedSettings()
	return nil
}

// warnMalformedSettings logs each stored tracker value that no longer passes
// the format check, since such a value is skipped at render time and tracking
// would otherwise stop without a trace.
func (m *Module) warnMalformedSettings() {
	for _, key := range validateTrackerFormats(m.currentSettings()) {
		m.ctx.Logger.Warn("analytics_ext: stored tracker value is malformed and will not be rendered", "field", key)
	}
}
