// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package react_headless provides a React headless CMS integration module for oCMS.
// It adds CORS support for the REST API and ships a ready-to-use React starter app
// that consumes the oCMS API in decoupled/headless mode.
package react_headless

import (
	"database/sql"
	"embed"
	"html/template"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Settings holds CORS configuration for the headless module.
type Settings struct {
	AllowedOrigins   string // Comma-separated list of allowed origins
	AllowCredentials bool   // Whether to allow credentials
	MaxAge           int    // Preflight cache duration in seconds
}

// Module implements the module.Module interface for the React headless module.
type Module struct {
	module.BaseModule
	ctx      *module.Context
	settings *Settings
}

// New creates a new instance of the React headless module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"react_headless",
			"1.0.0",
			"React headless CMS integration with CORS support",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	settings, err := m.loadSettings()
	if err != nil {
		ctx.Logger.Warn("failed to load react_headless settings, using defaults", "error", err)
		settings = &Settings{
			AllowedOrigins: "http://localhost:5173",
			MaxAge:         3600,
		}
	}
	m.settings = settings

	m.ctx.Logger.Info("react_headless module initialized",
		"allowed_origins", settings.AllowedOrigins,
		"allow_credentials", settings.AllowCredentials,
	)
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("react_headless module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes - the React app is a separate frontend
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/react-headless", m.handleDashboard)
	r.Post("/react-headless", m.handleSaveSettings)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return nil
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/react-headless"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "React Headless"
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
			Description: "Create react_headless_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS react_headless_settings (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						allowed_origins TEXT NOT NULL DEFAULT 'http://localhost:5173',
						allow_credentials INTEGER NOT NULL DEFAULT 0,
						max_age INTEGER NOT NULL DEFAULT 3600,
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					INSERT OR IGNORE INTO react_headless_settings (id) VALUES (1);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS react_headless_settings`)
				return err
			},
		},
	}
}

// GetSettings returns a copy of the current settings.
func (m *Module) GetSettings() Settings {
	if m.settings == nil {
		return Settings{}
	}
	return *m.settings
}

// GetAllowedOrigins returns the parsed list of allowed origins.
func (m *Module) GetAllowedOrigins() []string {
	if m.settings == nil || m.settings.AllowedOrigins == "" {
		return nil
	}
	origins := strings.Split(m.settings.AllowedOrigins, ",")
	var result []string
	for _, o := range origins {
		trimmed := strings.TrimSpace(o)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// loadSettings loads settings from the database.
func (m *Module) loadSettings() (*Settings, error) {
	settings := &Settings{}
	err := m.ctx.DB.QueryRow(`
		SELECT allowed_origins, allow_credentials, max_age
		FROM react_headless_settings
		WHERE id = 1
	`).Scan(&settings.AllowedOrigins, &settings.AllowCredentials, &settings.MaxAge)
	if err != nil {
		return nil, err
	}
	return settings, nil
}

// saveSettings saves settings to the database.
func (m *Module) saveSettings(settings *Settings) error {
	_, err := m.ctx.DB.Exec(`
		UPDATE react_headless_settings
		SET allowed_origins = ?, allow_credentials = ?, max_age = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, settings.AllowedOrigins, settings.AllowCredentials, settings.MaxAge)
	return err
}
