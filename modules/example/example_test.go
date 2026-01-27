// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package example

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// testModule creates a test Module with database access.
func testModule(t *testing.T, db *sql.DB) *Module {
	t.Helper()
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m
}

// testModuleWithHooks creates a test Module and returns the hooks registry.
func testModuleWithHooks(t *testing.T, db *sql.DB) *module.HookRegistry {
	t.Helper()
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, hooks := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return hooks
}

func TestModuleNew(t *testing.T) {
	m := New()

	if m.Name() != "example" {
		t.Errorf("Name() = %q, want example", m.Name())
	}
	if m.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want 1.0.0", m.Version())
	}
	if m.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestModuleAdminURL(t *testing.T) {
	m := New()

	if m.AdminURL() != "/admin/example" {
		t.Errorf("AdminURL() = %q, want /admin/example", m.AdminURL())
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()
	moduleutil.AssertMigrations(t, m.Migrations(), 1)
}

func TestModuleTemplateFuncs(t *testing.T) {
	m := New()

	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs() returned nil")
	}

	// Check exampleFunc exists and returns expected value
	if fn, ok := funcs["exampleFunc"]; !ok {
		t.Error("exampleFunc not found")
	} else {
		result := fn.(func() string)()
		if result != "Hello from example module" {
			t.Errorf("exampleFunc() = %q, want 'Hello from example module'", result)
		}
	}

	// Check exampleVersion exists and returns version
	if fn, ok := funcs["exampleVersion"]; !ok {
		t.Error("exampleVersion not found")
	} else {
		result := fn.(func() string)()
		if result != "1.0.0" {
			t.Errorf("exampleVersion() = %q, want '1.0.0'", result)
		}
	}
}

func TestModuleInit(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	hooks := testModuleWithHooks(t, db)

	if !hooks.HasHandlers(module.HookPageAfterSave) {
		t.Error("HookPageAfterSave handler not registered")
	}
	if !hooks.HasHandlers(module.HookPageBeforeRender) {
		t.Error("HookPageBeforeRender handler not registered")
	}
}

func TestModuleShutdown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Shutdown should not error
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestCreateItem(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Create an item
	item, err := m.createItem("Test Item", "Test description")
	if err != nil {
		t.Fatalf("createItem: %v", err)
	}

	// Verify item
	if item.ID == 0 {
		t.Error("item ID should not be 0")
	}
	if item.Name != "Test Item" {
		t.Errorf("item.Name = %q, want 'Test Item'", item.Name)
	}
	if item.Description != "Test description" {
		t.Errorf("item.Description = %q, want 'Test description'", item.Description)
	}
	if item.CreatedAt.IsZero() {
		t.Error("item.CreatedAt should not be zero")
	}
}

func TestListItems(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// List empty
	items, err := m.listItems()
	if err != nil {
		t.Fatalf("listItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0", len(items))
	}

	// Create some items
	_, err = m.createItem("Item 1", "Desc 1")
	if err != nil {
		t.Fatalf("createItem 1: %v", err)
	}
	_, err = m.createItem("Item 2", "Desc 2")
	if err != nil {
		t.Fatalf("createItem 2: %v", err)
	}

	// List again
	items, err = m.listItems()
	if err != nil {
		t.Fatalf("listItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	// Items should be sorted by created_at DESC (most recent first)
	if items[0].Name != "Item 2" {
		t.Errorf("first item should be 'Item 2', got %q", items[0].Name)
	}
}

func TestDeleteItem(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Create an item
	item, err := m.createItem("To Delete", "Will be deleted")
	if err != nil {
		t.Fatalf("createItem: %v", err)
	}

	// Delete it
	if err := m.deleteItem(item.ID); err != nil {
		t.Fatalf("deleteItem: %v", err)
	}

	// List should be empty
	items, err := m.listItems()
	if err != nil {
		t.Fatalf("listItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0 after delete", len(items))
	}
}

func TestDeleteItemNotFound(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Try to delete non-existent item
	err := m.deleteItem(99999)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("deleteItem(99999) = %v, want sql.ErrNoRows", err)
	}
}

func TestMigrationDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	moduleutil.RunMigrationsDown(t, db, m.Migrations())

	moduleutil.AssertTableNotExists(t, db, "example_items")
}

func TestHookRegistration(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	hooks := testModuleWithHooks(t, db)

	result, err := hooks.Call(context.Background(), module.HookPageAfterSave, "test data")
	if err != nil {
		t.Errorf("HookPageAfterSave: %v", err)
	}
	if result != "test data" {
		t.Errorf("HookPageAfterSave result = %v, want 'test data'", result)
	}

	result, err = hooks.Call(context.Background(), module.HookPageBeforeRender, "render data")
	if err != nil {
		t.Errorf("HookPageBeforeRender: %v", err)
	}
	if result != "render data" {
		t.Errorf("HookPageBeforeRender result = %v, want 'render data'", result)
	}
}

func TestHookHandlerInfo(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	hooks := testModuleWithHooks(t, db)

	afterSaveCount := hooks.HandlerCount(module.HookPageAfterSave)
	if afterSaveCount != 1 {
		t.Errorf("HookPageAfterSave handler count = %d, want 1", afterSaveCount)
	}

	beforeRenderCount := hooks.HandlerCount(module.HookPageBeforeRender)
	if beforeRenderCount != 1 {
		t.Errorf("HookPageBeforeRender handler count = %d, want 1", beforeRenderCount)
	}
}

func TestCreateItemEmptyDescription(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Create item with empty description
	item, err := m.createItem("No Description", "")
	if err != nil {
		t.Fatalf("createItem: %v", err)
	}

	if item.Description != "" {
		t.Errorf("item.Description = %q, want empty string", item.Description)
	}
}

func TestMultipleItemsCRUD(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Create multiple items
	itemCount := 5
	var createdIDs []int64
	for i := 1; i <= itemCount; i++ {
		item, err := m.createItem("Item", "Description")
		if err != nil {
			t.Fatalf("createItem %d: %v", i, err)
		}
		createdIDs = append(createdIDs, item.ID)
	}

	// List all
	items, err := m.listItems()
	if err != nil {
		t.Fatalf("listItems: %v", err)
	}
	if len(items) != itemCount {
		t.Errorf("len(items) = %d, want %d", len(items), itemCount)
	}

	// Delete every other item
	for i := 0; i < len(createdIDs); i += 2 {
		if err := m.deleteItem(createdIDs[i]); err != nil {
			t.Errorf("deleteItem(%d): %v", createdIDs[i], err)
		}
	}

	// Check remaining count
	items, err = m.listItems()
	if err != nil {
		t.Fatalf("listItems after delete: %v", err)
	}
	expectedRemaining := itemCount / 2
	if len(items) != expectedRemaining {
		t.Errorf("len(items) after delete = %d, want %d", len(items), expectedRemaining)
	}
}

func TestDependencies(t *testing.T) {
	m := New()

	deps := m.Dependencies()
	if len(deps) != 0 {
		t.Errorf("Dependencies() = %v, want nil or empty", deps)
	}
}
