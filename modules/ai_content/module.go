// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package ai_content provides an AI Content Manager module for oCMS.
// It supports generating pages (title, body, SEO metadata, featured image)
// using OpenAI, Claude, Groq, or Ollama, with full token/cost tracking.
package ai_content

import (
	"database/sql"
	"embed"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for the AI Content Manager.
type Module struct {
	module.BaseModule
	ctx        *module.Context
	uploadsDir string
}

// New creates a new instance of the AI Content Manager module.
func New(uploadsDir string) *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"ai_content",
			"1.0.0",
			"AI Content Manager",
		),
		uploadsDir: uploadsDir,
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx
	m.ctx.Logger.Info("AI Content Manager module initialized")
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("AI Content Manager module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/ai-content", m.handleDashboard)
	r.Get("/ai-content/settings", m.handleSettings)
	r.Post("/ai-content/settings", m.handleSaveSettings)
	r.Post("/ai-content/settings/test", m.handleTestConnection)
	r.Get("/ai-content/generate", m.handleGenerateForm)
	r.Post("/ai-content/generate", m.handleGenerate)
	r.Post("/ai-content/create-page", m.handleCreatePage)
	r.Get("/ai-content/usage", m.handleUsageLog)
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/ai-content"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "AI Content"
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
			Description: "Create AI content manager tables",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS ai_content_settings (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						provider TEXT NOT NULL UNIQUE,
						api_key TEXT NOT NULL DEFAULT '',
						model TEXT NOT NULL DEFAULT '',
						base_url TEXT NOT NULL DEFAULT '',
						is_enabled INTEGER NOT NULL DEFAULT 0,
						image_enabled INTEGER NOT NULL DEFAULT 0,
						image_model TEXT NOT NULL DEFAULT '',
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);

					CREATE TABLE IF NOT EXISTS ai_content_usage (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						page_id INTEGER,
						provider TEXT NOT NULL,
						model TEXT NOT NULL,
						operation TEXT NOT NULL,
						prompt_tokens INTEGER NOT NULL DEFAULT 0,
						completion_tokens INTEGER NOT NULL DEFAULT 0,
						total_tokens INTEGER NOT NULL DEFAULT 0,
						cost_usd REAL NOT NULL DEFAULT 0.0,
						language_code TEXT NOT NULL DEFAULT '',
						page_title TEXT NOT NULL DEFAULT '',
						created_by INTEGER NOT NULL DEFAULT 0,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);

					CREATE INDEX IF NOT EXISTS idx_ai_content_usage_page ON ai_content_usage(page_id);
					CREATE INDEX IF NOT EXISTS idx_ai_content_usage_provider ON ai_content_usage(provider);
					CREATE INDEX IF NOT EXISTS idx_ai_content_usage_created ON ai_content_usage(created_at);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`
					DROP TABLE IF EXISTS ai_content_usage;
					DROP TABLE IF EXISTS ai_content_settings;
				`)
				return err
			},
		},
	}
}
