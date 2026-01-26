// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package migrator provides a module for migrating content from other CMS platforms to oCMS.
// It supports multiple source systems through a pluggable importer architecture.
package migrator

import (
	"database/sql"
	"embed"
	"html/template"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/modules/migrator/sources/elefant"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for the migrator module.
type Module struct {
	module.BaseModule
	ctx *module.Context
}

// New creates a new instance of the migrator module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"migrator",
			"1.0.0",
			"Migrate content from other CMS platforms to oCMS",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	// Register available sources
	RegisterSource(elefant.NewSource())

	m.ctx.Logger.Info("Migrator module initialized", "sources", len(sources))
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Migrator module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for migrator module
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/migrator", m.handleListSources)
	r.Get("/migrator/{source}", m.handleSourceForm)
	r.Post("/migrator/{source}/test", m.handleTestConnection)
	r.Post("/migrator/{source}/import", m.handleImport)
	r.Post("/migrator/{source}/delete", m.handleDeleteImported)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/migrator"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Content Migrator"
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
			Description: "Create migrator_imported_items tracking table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS migrator_imported_items (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						source TEXT NOT NULL,
						entity_type TEXT NOT NULL,
						entity_id INTEGER NOT NULL,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					CREATE INDEX IF NOT EXISTS idx_migrator_source ON migrator_imported_items(source);
					CREATE INDEX IF NOT EXISTS idx_migrator_entity ON migrator_imported_items(source, entity_type);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS migrator_imported_items`)
				return err
			},
		},
	}
}
