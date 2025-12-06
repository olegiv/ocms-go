package module

import (
	"database/sql"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/i18n"
)

// Registry manages module registration and lifecycle.
type Registry struct {
	modules      map[string]Module
	order        []string // initialization order
	activeStatus map[string]bool
	ctx          *ModuleContext
	logger       *slog.Logger
	mu           sync.RWMutex
}

// NewRegistry creates a new module registry.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		modules:      make(map[string]Module),
		order:        make([]string, 0),
		activeStatus: make(map[string]bool),
		logger:       logger,
	}
}

// Register adds a module to the registry.
// Modules are registered in the order they are added.
func (r *Registry) Register(m Module) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := m.Name()
	if _, exists := r.modules[name]; exists {
		return fmt.Errorf("module %q already registered", name)
	}

	r.modules[name] = m
	r.order = append(r.order, name)
	r.logger.Info("module registered", "name", name, "version", m.Version())

	return nil
}

// Get returns a module by name.
func (r *Registry) Get(name string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	m, ok := r.modules[name]
	return m, ok
}

// List returns all registered modules in registration order.
func (r *Registry) List() []Module {
	r.mu.RLock()
	defer r.mu.RUnlock()

	modules := make([]Module, 0, len(r.order))
	for _, name := range r.order {
		if m, ok := r.modules[name]; ok {
			modules = append(modules, m)
		}
	}
	return modules
}

// InitAll initializes all registered modules.
// Modules are initialized in registration order.
// Dependencies are checked before initialization.
func (r *Registry) InitAll(ctx *ModuleContext) error {
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()

	// First, verify all dependencies are met
	if err := r.checkDependencies(); err != nil {
		return err
	}

	// Then, run migrations for all modules
	if err := r.runAllMigrations(ctx.DB); err != nil {
		return err
	}

	// Load active status for all modules from database
	if err := r.loadActiveStatus(ctx.DB); err != nil {
		return fmt.Errorf("loading module active status: %w", err)
	}

	// Finally, initialize modules in order
	for _, name := range r.order {
		m := r.modules[name]
		r.logger.Info("initializing module", "name", name, "active", r.activeStatus[name])

		if err := m.Init(ctx); err != nil {
			return fmt.Errorf("initializing module %q: %w", name, err)
		}

		// Load module translations
		if err := r.loadModuleTranslations(m); err != nil {
			r.logger.Warn("failed to load module translations", "module", name, "error", err)
		}

		r.logger.Info("module initialized", "name", name)
	}

	return nil
}

// checkDependencies verifies that all module dependencies are registered.
func (r *Registry) checkDependencies() error {
	for _, name := range r.order {
		m := r.modules[name]
		for _, dep := range m.Dependencies() {
			if _, ok := r.modules[dep]; !ok {
				return fmt.Errorf("module %q depends on %q which is not registered", name, dep)
			}
		}
	}
	return nil
}

// runAllMigrations runs migrations for all modules.
func (r *Registry) runAllMigrations(db *sql.DB) error {
	// First, ensure the module_migrations table exists
	if err := r.ensureMigrationsTable(db); err != nil {
		return fmt.Errorf("ensuring migrations table: %w", err)
	}

	// Run migrations for each module
	for _, name := range r.order {
		m := r.modules[name]
		migrations := m.Migrations()
		if len(migrations) == 0 {
			continue
		}

		r.logger.Info("running module migrations", "module", name, "count", len(migrations))

		for _, mig := range migrations {
			applied, err := r.isMigrationApplied(db, name, mig.Version)
			if err != nil {
				return fmt.Errorf("checking migration status for %s v%d: %w", name, mig.Version, err)
			}

			if applied {
				continue
			}

			r.logger.Info("applying migration", "module", name, "version", mig.Version, "description", mig.Description)

			if err := mig.Up(db); err != nil {
				return fmt.Errorf("running migration %s v%d: %w", name, mig.Version, err)
			}

			if err := r.recordMigration(db, name, mig.Version); err != nil {
				return fmt.Errorf("recording migration %s v%d: %w", name, mig.Version, err)
			}
		}
	}

	return nil
}

// ensureMigrationsTable creates the module_migrations table if it doesn't exist.
func (r *Registry) ensureMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS module_migrations (
			module TEXT NOT NULL,
			version INTEGER NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (module, version)
		)
	`)
	if err != nil {
		return err
	}

	// Also ensure modules table exists for active status tracking
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS modules (
			name TEXT PRIMARY KEY,
			is_active BOOLEAN NOT NULL DEFAULT 1,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// isMigrationApplied checks if a migration has already been applied.
func (r *Registry) isMigrationApplied(db *sql.DB, module string, version int64) (bool, error) {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM module_migrations WHERE module = ? AND version = ?",
		module, version,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// recordMigration records that a migration has been applied.
func (r *Registry) recordMigration(db *sql.DB, module string, version int64) error {
	_, err := db.Exec(
		"INSERT INTO module_migrations (module, version, applied_at) VALUES (?, ?, ?)",
		module, version, time.Now(),
	)
	return err
}

// loadActiveStatus loads the active status for all registered modules from the database.
// Modules not in the database are considered active by default and will be inserted.
func (r *Registry) loadActiveStatus(db *sql.DB) error {
	for _, name := range r.order {
		var isActive bool
		err := db.QueryRow("SELECT is_active FROM modules WHERE name = ?", name).Scan(&isActive)
		if err == sql.ErrNoRows {
			// Module not in database, insert with active=true
			_, err = db.Exec(
				"INSERT INTO modules (name, is_active, updated_at) VALUES (?, 1, CURRENT_TIMESTAMP)",
				name,
			)
			if err != nil {
				return fmt.Errorf("inserting module %s: %w", name, err)
			}
			r.activeStatus[name] = true
			r.logger.Debug("module registered in database", "module", name, "active", true)
		} else if err != nil {
			return fmt.Errorf("loading active status for module %s: %w", name, err)
		} else {
			r.activeStatus[name] = isActive
			r.logger.Debug("loaded module active status", "module", name, "active", isActive)
		}
	}
	return nil
}

// IsActive returns whether a module is active.
func (r *Registry) IsActive(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	active, exists := r.activeStatus[name]
	if !exists {
		return true // Default to active if not tracked
	}
	return active
}

// SetActive sets a module's active status and persists it to the database.
func (r *Registry) SetActive(name string, active bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.modules[name]; !exists {
		return fmt.Errorf("module %q not registered", name)
	}

	if r.ctx == nil || r.ctx.DB == nil {
		return fmt.Errorf("registry not initialized")
	}

	_, err := r.ctx.DB.Exec(
		"UPDATE modules SET is_active = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?",
		active, name,
	)
	if err != nil {
		return fmt.Errorf("updating module active status: %w", err)
	}

	r.activeStatus[name] = active
	r.logger.Info("module active status changed", "module", name, "active", active)
	return nil
}

// loadModuleTranslations loads translations from a module's embedded filesystem.
func (r *Registry) loadModuleTranslations(m Module) error {
	transFS := m.TranslationsFS()

	// Check if the module has translations by trying to read the locales directory
	_, err := fs.ReadDir(transFS, "locales")
	if err != nil {
		// No translations for this module
		return nil
	}

	if err := i18n.LoadTranslationsFromFS(transFS, ""); err != nil {
		return fmt.Errorf("loading translations for module %s: %w", m.Name(), err)
	}

	r.logger.Debug("loaded module translations", "module", m.Name())
	return nil
}

// ShutdownAll shuts down all modules in reverse order.
func (r *Registry) ShutdownAll() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var errs []error

	// Shutdown in reverse order
	for i := len(r.order) - 1; i >= 0; i-- {
		name := r.order[i]
		m := r.modules[name]

		r.logger.Info("shutting down module", "name", name)

		if err := m.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("shutting down module %q: %w", name, err))
			r.logger.Error("module shutdown error", "name", name, "error", err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%d module(s) failed to shutdown: %v", len(errs), errs)
	}

	return nil
}

// RouteAll registers all module public routes with active status middleware.
func (r *Registry) RouteAll(router chi.Router) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, name := range r.order {
		m := r.modules[name]
		moduleName := name // capture for closure

		// Create a sub-router with middleware that checks active status
		router.Group(func(subRouter chi.Router) {
			subRouter.Use(r.moduleActiveMiddleware(moduleName, false))
			m.RegisterRoutes(subRouter)
		})
	}
}

// AdminRouteAll registers all module admin routes with active status middleware.
func (r *Registry) AdminRouteAll(router chi.Router) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, name := range r.order {
		m := r.modules[name]
		moduleName := name // capture for closure

		// Create a sub-router with middleware that checks active status
		router.Group(func(subRouter chi.Router) {
			subRouter.Use(r.moduleActiveMiddleware(moduleName, true))
			m.RegisterAdminRoutes(subRouter)
		})
	}
}

// moduleActiveMiddleware returns middleware that checks if a module is active.
// If not active, returns 404 for public routes or redirects to /admin/modules for admin routes.
func (r *Registry) moduleActiveMiddleware(moduleName string, isAdmin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !r.IsActive(moduleName) {
				r.logger.Debug("blocked request to inactive module",
					"module", moduleName,
					"path", req.URL.Path,
					"isAdmin", isAdmin,
				)
				if isAdmin {
					http.Redirect(w, req, "/admin/modules", http.StatusSeeOther)
					return
				}
				http.NotFound(w, req)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// AllTemplateFuncs returns combined template functions from all active modules.
func (r *Registry) AllTemplateFuncs() template.FuncMap {
	r.mu.RLock()
	defer r.mu.RUnlock()

	funcs := make(template.FuncMap)
	for _, name := range r.order {
		// Default to active if not tracked (for testing or before InitAll)
		active, exists := r.activeStatus[name]
		if exists && !active {
			continue
		}
		m := r.modules[name]
		for k, v := range m.TemplateFuncs() {
			funcs[k] = v
		}
	}
	return funcs
}

// ModuleInfo contains information about a registered module.
type ModuleInfo struct {
	Name              string
	Version           string
	Description       string
	Initialized       bool
	Active            bool   // Whether the module is active (routes/hooks enabled)
	MigrationCount    int    // Total number of migrations defined
	MigrationsApplied int    // Number of migrations applied
	MigrationsPending int    // Number of pending migrations
	HasMigrations     bool   // Whether the module has any migrations
	AdminURL          string // URL to module's admin dashboard (empty if none)
}

// MigrationInfo contains information about a module migration.
type MigrationInfo struct {
	Version     int64
	Description string
	Applied     bool
}

// ListInfo returns information about all registered modules.
func (r *Registry) ListInfo() []ModuleInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]ModuleInfo, 0, len(r.order))
	for _, name := range r.order {
		m := r.modules[name]
		migrations := m.Migrations()
		migrationCount := len(migrations)
		appliedCount := 0

		// Count applied migrations if we have a context with DB
		if r.ctx != nil && r.ctx.DB != nil {
			for _, mig := range migrations {
				applied, err := r.isMigrationApplied(r.ctx.DB, name, mig.Version)
				if err == nil && applied {
					appliedCount++
				}
			}
		}

		// Default to active if not tracked
		active, exists := r.activeStatus[name]
		if !exists {
			active = true
		}

		infos = append(infos, ModuleInfo{
			Name:              m.Name(),
			Version:           m.Version(),
			Description:       m.Description(),
			Initialized:       r.ctx != nil,
			Active:            active,
			MigrationCount:    migrationCount,
			MigrationsApplied: appliedCount,
			MigrationsPending: migrationCount - appliedCount,
			HasMigrations:     migrationCount > 0,
			AdminURL:          m.AdminURL(),
		})
	}
	return infos
}

// GetMigrationInfo returns detailed migration information for a specific module.
func (r *Registry) GetMigrationInfo(moduleName string) ([]MigrationInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	m, ok := r.modules[moduleName]
	if !ok {
		return nil, fmt.Errorf("module %q not found", moduleName)
	}

	migrations := m.Migrations()
	infos := make([]MigrationInfo, len(migrations))

	for i, mig := range migrations {
		applied := false
		if r.ctx != nil && r.ctx.DB != nil {
			var err error
			applied, err = r.isMigrationApplied(r.ctx.DB, moduleName, mig.Version)
			if err != nil {
				r.logger.Warn("failed to check migration status", "module", moduleName, "version", mig.Version, "error", err)
			}
		}

		infos[i] = MigrationInfo{
			Version:     mig.Version,
			Description: mig.Description,
			Applied:     applied,
		}
	}

	return infos, nil
}

// Count returns the number of registered modules.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.modules)
}
