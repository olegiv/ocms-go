// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package embed provides external embed integration for oCMS.
// Supports embedding third-party services like Dify AI chat.
package embed

import (
	"database/sql"
	"embed"
	"html/template"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/modules/embed/providers"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for the embed module.
type Module struct {
	module.BaseModule
	ctx       *module.Context
	providers []providers.Provider
	settings  []*ProviderSettings
	mu        sync.RWMutex
}

// New creates a new instance of the embed module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"embed",
			"1.0.0",
			"External Embed",
		),
		providers: []providers.Provider{
			providers.NewDify(),
		},
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	// Load enabled provider settings
	if err := m.reloadSettings(); err != nil {
		ctx.Logger.Warn("failed to load embed settings", "error", err)
	}

	m.ctx.Logger.Info("Embed module initialized",
		"providers", len(m.providers),
		"enabled", m.countEnabled(),
	)
	return nil
}

// reloadSettings reloads all enabled provider settings.
func (m *Module) reloadSettings() error {
	settings, err := loadAllEnabledSettings(m.ctx.DB)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.settings = settings
	m.mu.Unlock()

	return nil
}

// countEnabled returns the number of enabled providers.
func (m *Module) countEnabled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.settings)
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Embed module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for embed module
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/embed", m.handleList)
	r.Get("/embed/{provider}", m.handleProviderSettings)
	r.Post("/embed/{provider}", m.handleSaveProviderSettings)
	r.Post("/embed/{provider}/toggle", m.handleToggleProvider)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// embedHead returns scripts for the <head> section
		"embedHead": func() template.HTML {
			return m.renderHead()
		},
		// embedBody returns scripts for before </body>
		"embedBody": func() template.HTML {
			return m.renderBody()
		},
	}
}

// renderScripts generates all enabled provider scripts using the provided render function.
// SECURITY: Output is cast to template.HTML. Provider settings are admin-controlled
// and individual values are escaped with template.HTMLEscapeString before embedding.
func (m *Module) renderScripts(renderFn func(providers.Provider, map[string]string) template.HTML) template.HTML {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var scripts strings.Builder
	for _, ps := range m.settings {
		provider := m.getProvider(ps.ProviderID)
		if provider == nil {
			continue
		}
		scripts.WriteString(string(renderFn(provider, ps.Settings)))
	}
	return template.HTML(scripts.String())
}

// renderHead generates all enabled provider head scripts.
func (m *Module) renderHead() template.HTML {
	return m.renderScripts(func(p providers.Provider, s map[string]string) template.HTML {
		return p.RenderHead(s)
	})
}

// renderBody generates all enabled provider body scripts.
func (m *Module) renderBody() template.HTML {
	return m.renderScripts(func(p providers.Provider, s map[string]string) template.HTML {
		return p.RenderBody(s)
	})
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/embed"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Embed"
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
			Description: "Create embed_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS embed_settings (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						provider TEXT NOT NULL UNIQUE,
						settings TEXT NOT NULL DEFAULT '{}',
						is_enabled INTEGER NOT NULL DEFAULT 0,
						position INTEGER NOT NULL DEFAULT 0,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					CREATE INDEX IF NOT EXISTS idx_embed_settings_enabled ON embed_settings(is_enabled);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS embed_settings`)
				return err
			},
		},
	}
}

// ReloadSettings reloads settings from the database.
func (m *Module) ReloadSettings() error {
	return m.reloadSettings()
}
