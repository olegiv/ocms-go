// Package developer provides a developer module for generating test data in oCMS.
// This module creates random tags, categories, media, and pages with translations,
// tracks all generated items, and allows bulk deletion.
package developer

import (
	"database/sql"
	"embed"
	"html/template"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for the developer module.
type Module struct {
	module.BaseModule
	ctx *module.ModuleContext
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

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.ModuleContext) error {
	m.ctx = ctx
	m.ctx.Logger.Info("Developer module initialized")
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
func (m *Module) RegisterRoutes(r chi.Router) {
	// No public routes for developer module
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/developer", m.handleDashboard)
	r.Post("/developer/generate", m.handleGenerate)
	r.Post("/developer/delete", m.handleDelete)
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
	}
}
