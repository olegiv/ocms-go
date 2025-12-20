// Package module provides a module system for extending oCMS functionality.
// Modules can register routes, admin routes, template functions, and hooks
// to integrate with the core application.
package module

import (
	"database/sql"
	"embed"
	"html/template"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/config"
	"ocms-go/internal/render"
	"ocms-go/internal/service"
	"ocms-go/internal/store"
)

// Context provides access to application services for modules.
type Context struct {
	DB     *sql.DB
	Store  *store.Queries
	Logger *slog.Logger
	Config *config.Config
	Render *render.Renderer
	Events *service.EventService
	Hooks  *HookRegistry
}

// Module defines the interface that all modules must implement.
type Module interface {
	// Name returns the module name.
	Name() string
	// Version returns the module version.
	Version() string
	// Description returns the module description.
	Description() string
	// Dependencies returns the list of module dependencies.
	Dependencies() []string

	// Init initializes the module with the given context.
	Init(ctx *Context) error
	// Shutdown performs cleanup when the module is shutting down.
	Shutdown() error

	// RegisterRoutes registers public routes for the module.
	RegisterRoutes(r chi.Router)

	// RegisterAdminRoutes registers admin routes for the module.
	RegisterAdminRoutes(r chi.Router)

	// TemplateFuncs returns template functions provided by the module.
	TemplateFuncs() template.FuncMap

	// Migrations returns migrations for the module.
	Migrations() []Migration

	// AdminURL returns the admin dashboard URL for the module (e.g., "/admin/developer").
	// Return empty string if module has no admin dashboard.
	AdminURL() string

	// SidebarLabel returns the display label for the admin sidebar.
	// Return empty string to use the module name as label.
	SidebarLabel() string

	// TranslationsFS returns an embedded filesystem containing module translations.
	// Expected structure: locales/{lang}/messages.json
	// Return nil if module has no translations.
	TranslationsFS() embed.FS
}

// Migration represents a database migration for a module.
type Migration struct {
	Version     int64
	Description string
	Up          func(db *sql.DB) error
	Down        func(db *sql.DB) error
}

// BaseModule provides a default implementation of the Module interface.
// Modules can embed this struct to get default no-op implementations.
type BaseModule struct {
	name        string
	version     string
	description string
	ctx         *Context
}

// NewBaseModule creates a new BaseModule with the given metadata.
func NewBaseModule(name, version, description string) BaseModule {
	return BaseModule{
		name:        name,
		version:     version,
		description: description,
	}
}

// Name returns the module name.
func (m *BaseModule) Name() string { return m.name }

// Version returns the module version.
func (m *BaseModule) Version() string { return m.version }

// Description returns the module description.
func (m *BaseModule) Description() string { return m.description }

// Dependencies returns the list of module dependencies (empty by default).
func (m *BaseModule) Dependencies() []string { return nil }

// Init initializes the module with the given context.
func (m *BaseModule) Init(ctx *Context) error {
	m.ctx = ctx
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *BaseModule) Shutdown() error { return nil }

// RegisterRoutes registers public routes (no-op by default).
func (m *BaseModule) RegisterRoutes(_ chi.Router) {}

// RegisterAdminRoutes registers admin routes (no-op by default).
func (m *BaseModule) RegisterAdminRoutes(_ chi.Router) {}

// TemplateFuncs returns template functions (empty by default).
func (m *BaseModule) TemplateFuncs() template.FuncMap { return nil }

// Migrations returns module migrations (empty by default).
func (m *BaseModule) Migrations() []Migration { return nil }

// AdminURL returns the admin dashboard URL (empty by default).
func (m *BaseModule) AdminURL() string { return "" }

// SidebarLabel returns the sidebar display label (empty = use name).
func (m *BaseModule) SidebarLabel() string { return "" }

// TranslationsFS returns nil (no translations by default).
func (m *BaseModule) TranslationsFS() embed.FS { return embed.FS{} }

// Context returns the module context (for use by embedded modules).
func (m *BaseModule) Context() *Context { return m.ctx }
