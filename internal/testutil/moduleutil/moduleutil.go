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

// AssertMigrations verifies that migrations have the expected count and valid structure.
// Checks that the first migration has version 1 and all migrations have descriptions.
func AssertMigrations(t *testing.T, migrations []module.Migration, expectedCount int) {
	t.Helper()

	if len(migrations) != expectedCount {
		t.Errorf("len(migrations) = %d, want %d", len(migrations), expectedCount)
	}

	if len(migrations) == 0 {
		return
	}

	if migrations[0].Version != 1 {
		t.Errorf("first migration version = %d, want 1", migrations[0].Version)
	}

	for i, mig := range migrations {
		if mig.Description == "" {
			t.Errorf("migration %d: description should not be empty", i+1)
		}
	}
}

// AssertTableNotExists verifies that a table does not exist in the database.
// Useful for testing migration rollbacks.
func AssertTableNotExists(t *testing.T, db *sql.DB, tableName string) {
	t.Helper()

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&count)
	if err != nil {
		t.Fatalf("query table existence: %v", err)
	}
	if count != 0 {
		t.Errorf("table %q should not exist", tableName)
	}
}
