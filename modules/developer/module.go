// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package developer provides a developer module for generating test data in oCMS.
// This module creates random tags, categories, media, and pages with translations,
// tracks all generated items, and allows bulk deletion.
package developer

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for the developer module.
type Module struct {
	module.BaseModule
	ctx              *module.Context
	removeMediaFiles func(uploadDir, mediaUUID string) error
}

// New creates a new instance of the developer module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"developer",
			"1.0.0",
			"Developer tools for generating test data with translations",
		),
	}
}

// AllowedEnvs restricts the developer module to non-production environments.
// On first run, the module will be auto-inserted as inactive if the environment
// is not in this list.
func (m *Module) AllowedEnvs() []string {
	return []string{"development"}
}

// Init initializes the module with the given context.
// The developer module is blocked in production environments for security.
func (m *Module) Init(ctx *module.Context) error {
	// Security: Developer module must never run in production
	if ctx.Config.Env == "production" {
		return fmt.Errorf("developer module cannot be enabled in production environment")
	}

	m.ctx = ctx
	if ctx.DB != nil {
		drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.drainMediaCleanup(drainCtx); err != nil {
			ctx.Logger.Warn("developer media cleanup remains pending", "error", err)
		}
	}
	m.ctx.Logger.Info("Developer module initialized", "env", ctx.Config.Env)
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Developer module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for developer module
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Route("/developer", func(r chi.Router) {
		r.Use(middleware.RequireAdmin())
		r.Get("/", m.handleDashboard)
		r.Post("/generate", m.handleGenerate)
		r.Post("/delete", m.handleDelete)
	})
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/developer"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Developer Tools"
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
			Description: "Create developer_generated_items tracking table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS developer_generated_items (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						entity_type TEXT NOT NULL,
						entity_id INTEGER NOT NULL,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					CREATE INDEX IF NOT EXISTS idx_dev_items_type ON developer_generated_items(entity_type);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS developer_generated_items`)
				return err
			},
		},
		{
			Version:     2,
			Description: "Create developer media cleanup retry queue",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS developer_media_cleanup_queue (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						upload_root TEXT NOT NULL,
						media_uuid TEXT NOT NULL,
						generation INTEGER NOT NULL DEFAULT 1,
						attempts INTEGER NOT NULL DEFAULT 0,
						last_error TEXT NOT NULL DEFAULT '',
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						UNIQUE(upload_root, media_uuid)
					);
					CREATE INDEX IF NOT EXISTS idx_developer_media_cleanup_created
						ON developer_media_cleanup_queue(created_at);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS developer_media_cleanup_queue`)
				return err
			},
		},
	}
}
