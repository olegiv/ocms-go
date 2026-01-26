// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package analytics_ext provides an external statistics tracking module for oCMS.
// Supports Google Analytics 4 (GA4), Google Tag Manager (GTM), and Matomo.
package analytics_ext

import (
	"database/sql"
	"embed"
	"html/template"

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
	m.settings = settings

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
		"analyticsExtHead": func() template.HTML {
			return m.renderHeadScripts()
		},
		// analyticsExtBody returns tracking scripts for the end of <body>
		"analyticsExtBody": func() template.HTML {
			return m.renderBodyScripts()
		},
	}
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
	m.settings = settings
	return nil
}
