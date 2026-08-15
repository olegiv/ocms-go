// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

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

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/scheduler"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// Context provides access to application services for modules.
//
// Every field must be populated where the context is built in cmd/ocms; the
// drift test TestModuleContextFieldsWiredInMain enforces that, because a field
// added here but forgotten there fails silently at runtime rather than at
// compile time.
type Context struct {
	DB                *sql.DB
	Store             *store.Queries
	Logger            *slog.Logger
	Config            *config.Config
	Render            *render.Renderer
	Events            *service.EventService
	Hooks             *HookRegistry
	SchedulerRegistry *scheduler.Registry
	// Cache lets a module invalidate cached content after a bulk write. It may
	// be nil in tests and in embedders that do not wire it, so every use must
	// be nil-guarded.
	Cache *cache.Manager
	// RedirectCacheInvalidator lets modules that write redirects make those
	// changes visible immediately without depending on the concrete middleware.
	// It may be nil in tests and embedders.
	RedirectCacheInvalidator RedirectCacheInvalidator
	// PublicRouteChecker lets modules that create database redirects avoid
	// shadowing routes registered by core or another module. The registry wires
	// itself during InitAll, after every module has been registered.
	PublicRouteChecker PublicRouteChecker
}

// RedirectCacheInvalidator is the narrow cache capability exposed to modules
// that create or delete redirects.
type RedirectCacheInvalidator interface {
	InvalidateCache()
}

// PublicRouteChecker reports whether a concrete URL path is owned by a
// registered module route. Core routes are protected separately by the shared
// reserved-prefix policy because the module registry does not register them.
type PublicRouteChecker interface {
	OwnsPublicPath(path string) bool
}

// ActivationGuard is an optional interface a module may implement to refuse
// activation when its runtime security posture is not satisfied.
//
// Production posture audits run once at startup, so a module that was inactive
// then could previously be switched on through the admin UI without any of
// those checks re-running — the policy held only until someone toggled it.
type ActivationGuard interface {
	// CheckActivation returns an error explaining why the module must not be
	// activated right now. It runs before Init and before the row is marked
	// active, so the module's own context is not wired yet — the registry
	// context is passed in instead. Reading m.ctx here would observe nil for
	// precisely the module this guard exists to stop: one that was inactive at
	// startup.
	CheckActivation(ctx *Context) error
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

// EnvironmentChecker is an optional interface modules can implement to
// restrict which environments they can run in. When a module is first
// registered in the database, if it implements this interface and the
// current environment is not in the allowed list, it will be inserted
// as inactive (is_active=0).
type EnvironmentChecker interface {
	AllowedEnvs() []string
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
