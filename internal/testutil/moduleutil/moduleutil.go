// Package moduleutil provides module-specific test helpers for the oCMS project.
package moduleutil

import (
	"database/sql"
	"testing"

	"ocms-go/internal/config"
	"ocms-go/internal/module"
	"ocms-go/internal/store"
	"ocms-go/internal/testutil"
)

// RunMigrations runs all migrations up for the given module.
func RunMigrations(t *testing.T, db *sql.DB, migrations []module.Migration) {
	t.Helper()
	for _, mig := range migrations {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration %d up: %v", mig.Version, err)
		}
	}
}

// RunMigrationsDown rolls back all migrations for the given module.
func RunMigrationsDown(t *testing.T, db *sql.DB, migrations []module.Migration) {
	t.Helper()
	for _, mig := range migrations {
		if err := mig.Down(db); err != nil {
			t.Fatalf("migration %d down: %v", mig.Version, err)
		}
	}
}

// TestModuleContext creates a module.Context for testing.
// Returns the context and the hooks registry for verifying hook behavior.
func TestModuleContext(t *testing.T, db *sql.DB) (*module.Context, *module.HookRegistry) {
	t.Helper()
	logger := testutil.TestLogger()
	hooks := module.NewHookRegistry(logger)
	return &module.Context{
		DB:     db,
		Logger: logger,
		Config: &config.Config{},
		Hooks:  hooks,
	}, hooks
}

// TestModuleContextWithStore creates a module.Context with a store.Queries instance.
// Useful for modules that require Store field to be set.
func TestModuleContextWithStore(t *testing.T, db *sql.DB) (*module.Context, *module.HookRegistry) {
	t.Helper()
	logger := testutil.TestLogger()
	hooks := module.NewHookRegistry(logger)
	return &module.Context{
		DB:     db,
		Store:  store.New(db),
		Logger: logger,
		Config: &config.Config{},
		Hooks:  hooks,
	}, hooks
}
