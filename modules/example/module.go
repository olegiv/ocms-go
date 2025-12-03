// Package example provides an example module demonstrating the oCMS module system.
// This module serves as a reference implementation for creating custom modules.
package example

import (
	"context"
	"database/sql"
	"html/template"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/module"
)

// Module implements the module.Module interface.
type Module struct {
	module.BaseModule
	ctx *module.ModuleContext
}

// New creates a new instance of the example module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"example",
			"1.0.0",
			"Example module demonstrating the oCMS module system",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.ModuleContext) error {
	m.ctx = ctx
	m.ctx.Logger.Info("Example module initialized")

	// Register hooks
	m.registerHooks()

	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Example module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(r chi.Router) {
	r.Get("/example", m.handleExample)
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/example", m.handleAdminExample)
	r.Get("/example/items", m.handleListItems)
	r.Post("/example/items", m.handleCreateItem)
	r.Delete("/example/items/{id}", m.handleDeleteItem)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"exampleFunc": func() string {
			return "Hello from example module"
		},
		"exampleVersion": func() string {
			return m.Version()
		},
	}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/example"
}

// Migrations returns database migrations for the module.
func (m *Module) Migrations() []module.Migration {
	return []module.Migration{
		{
			Version:     1,
			Description: "Create example_items table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS example_items (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						name TEXT NOT NULL,
						description TEXT DEFAULT '',
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					)
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS example_items`)
				return err
			},
		},
	}
}

// registerHooks registers hook handlers for the module.
func (m *Module) registerHooks() {
	// Register a hook handler for page.after_save
	m.ctx.Hooks.Register(module.HookPageAfterSave, module.HookHandler{
		Name:     "example_page_saved",
		Module:   m.Name(),
		Priority: 10,
		Fn: func(ctx context.Context, data any) (any, error) {
			m.ctx.Logger.Debug("Example module: page saved hook triggered")
			return data, nil
		},
	})

	// Register a hook handler for page.before_render
	m.ctx.Hooks.Register(module.HookPageBeforeRender, module.HookHandler{
		Name:     "example_before_render",
		Module:   m.Name(),
		Priority: 5,
		Fn: func(ctx context.Context, data any) (any, error) {
			m.ctx.Logger.Debug("Example module: before render hook triggered")
			return data, nil
		},
	})
}
