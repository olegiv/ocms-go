// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package privacy provides consent management using Klaro with Google Consent Mode v2 support.
package privacy

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for the privacy/consent module.
type Module struct {
	module.BaseModule
	ctx      *module.Context
	settings *Settings
}

// New creates a new instance of the privacy module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"privacy",
			"1.0.0",
			"Consent management with Klaro and Google Consent Mode v2",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	// Load settings from database
	settings, err := loadSettings(ctx.DB)
	if err != nil {
		ctx.Logger.Warn("failed to load privacy settings, using defaults", "error", err)
		settings = &Settings{}
	}
	m.settings = settings

	m.ctx.Logger.Info("Privacy module initialized",
		"enabled", settings.Enabled,
		"gcm_enabled", settings.GCMEnabled,
	)
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Privacy module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for privacy module
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/privacy", m.handleDashboard)
	r.Post("/privacy", m.handleSaveSettings)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// privacyHead returns consent scripts for the <head> section
		// MUST be called BEFORE analyticsExtHead for proper GCM initialization
		"privacyHead": func() template.HTML {
			return m.renderHeadScripts()
		},
		// privacyFooterLink returns a link to open the consent modal
		// Returns empty string if privacy is disabled
		"privacyFooterLink": func() template.HTML {
			return m.renderFooterLink()
		},
	}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/privacy"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Privacy"
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
			Description: "Create privacy_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS privacy_settings (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						enabled INTEGER NOT NULL DEFAULT 0,

						-- Privacy URLs
						privacy_policy_url TEXT NOT NULL DEFAULT '',

						-- Cookie config
						cookie_name TEXT NOT NULL DEFAULT 'klaro',
						cookie_expires_days INTEGER NOT NULL DEFAULT 365,

						-- Appearance
						theme TEXT NOT NULL DEFAULT 'light',
						position TEXT NOT NULL DEFAULT 'bottom-right',

						-- Google Consent Mode v2
						gcm_enabled INTEGER NOT NULL DEFAULT 1,
						gcm_default_analytics INTEGER NOT NULL DEFAULT 0,
						gcm_default_ad_storage INTEGER NOT NULL DEFAULT 0,
						gcm_default_ad_user_data INTEGER NOT NULL DEFAULT 0,
						gcm_default_ad_personalization INTEGER NOT NULL DEFAULT 0,
						gcm_wait_for_update INTEGER NOT NULL DEFAULT 500,

						-- Services JSON array
						services TEXT NOT NULL DEFAULT '[]',

						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					INSERT OR IGNORE INTO privacy_settings (id) VALUES (1);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS privacy_settings`)
				return err
			},
		},
		{
			Version:     2,
			Description: "Add debug column to privacy_settings",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`ALTER TABLE privacy_settings ADD COLUMN debug INTEGER NOT NULL DEFAULT 0`)
				return err
			},
			Down: func(db *sql.DB) error {
				// SQLite doesn't support DROP COLUMN easily, so we skip the down migration
				return nil
			},
		},
		{
			Version:     3,
			Description: "Update essential cookies service",
			Up: func(db *sql.DB) error {
				var servicesJSON string
				err := db.QueryRow(`SELECT services FROM privacy_settings WHERE id = 1`).Scan(&servicesJSON)
				if err != nil {
					return fmt.Errorf("reading services: %w", err)
				}

				if servicesJSON == "" || servicesJSON == "[]" {
					return nil
				}

				var services []Service
				if err := json.Unmarshal([]byte(servicesJSON), &services); err != nil {
					return fmt.Errorf("parsing services JSON: %w", err)
				}

				found := false
				for i := range services {
					if services[i].Name == "klaro" {
						services[i].Title = "Essential Cookies"
						services[i].Description = "Stores consent choices and UI preferences (required)"
						services[i].Purposes = []string{"essential"}
						services[i].Cookies = []Cookie{
							{Pattern: "^klaro"},
							{Pattern: "^ocms_lang$"},
							{Pattern: "^(__Host-)?session$"},
							{Pattern: "^ocms_informer_dismissed"},
						}
						found = true
						break
					}
				}

				if !found {
					return nil
				}

				data, err := json.Marshal(services)
				if err != nil {
					return fmt.Errorf("marshaling services: %w", err)
				}

				_, err = db.Exec(`UPDATE privacy_settings SET services = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`, string(data))
				return err
			},
			Down: func(db *sql.DB) error {
				return nil
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

// IsEnabled returns whether privacy consent is enabled.
func (m *Module) IsEnabled() bool {
	return m.settings != nil && m.settings.Enabled
}
