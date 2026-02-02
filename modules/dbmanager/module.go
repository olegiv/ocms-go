// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dbmanager provides a database management module for executing SQL queries.
// This module allows administrators to run arbitrary SQL queries and view results.
// Access is restricted to admin users only.
package dbmanager

import (
	"database/sql"
	"embed"
	"html/template"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for the database manager module.
type Module struct {
	module.BaseModule
	ctx *module.Context
}

// New creates a new instance of the database manager module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"dbmanager",
			"1.0.0",
			"Database management tool for executing SQL queries",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx
	m.ctx.Logger.Info("Database Manager module initialized", "env", ctx.Config.Env)
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Database Manager module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for database manager module
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/dbmanager", m.handleDashboard)
	r.Post("/dbmanager/execute", m.handleExecute)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/dbmanager"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "DB Manager"
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
			Description: "Create dbmanager_query_history table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS dbmanager_query_history (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						query TEXT NOT NULL,
						user_id INTEGER NOT NULL,
						executed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						rows_affected INTEGER DEFAULT 0,
						execution_time_ms INTEGER DEFAULT 0,
						error TEXT
					);
					CREATE INDEX IF NOT EXISTS idx_dbmanager_history_user ON dbmanager_query_history(user_id);
					CREATE INDEX IF NOT EXISTS idx_dbmanager_history_time ON dbmanager_query_history(executed_at DESC);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS dbmanager_query_history`)
				return err
			},
		},
	}
}
