// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package dbmanager

import (
	"context"
	"database/sql"
	"testing"

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

func TestModuleNew(t *testing.T) {
	m := New()

	if m.Name() != "dbmanager" {
		t.Errorf("Name() = %q, want dbmanager", m.Name())
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

	if m.AdminURL() != "/admin/dbmanager" {
		t.Errorf("AdminURL() = %q, want /admin/dbmanager", m.AdminURL())
	}
}

func TestModuleSidebarLabel(t *testing.T) {
	m := New()

	if m.SidebarLabel() != "DB Manager" {
		t.Errorf("SidebarLabel() = %q, want 'DB Manager'", m.SidebarLabel())
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
}

func TestModuleAllowedEnvs(t *testing.T) {
	m := New()

	envs := m.AllowedEnvs()
	if len(envs) != 1 || envs[0] != "development" {
		t.Errorf("AllowedEnvs() = %v, want [development]", envs)
	}
}

func TestModuleInit(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	_ = testModule(t, db)
	// If we get here without error, init succeeded
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

func TestMigrationDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	moduleutil.RunMigrationsDown(t, db, m.Migrations())

	moduleutil.AssertTableNotExists(t, db, "dbmanager_query_history")
}

func TestDependencies(t *testing.T) {
	m := New()

	deps := m.Dependencies()
	if len(deps) != 0 {
		t.Errorf("Dependencies() = %v, want nil or empty", deps)
	}
}

func TestIsSelectQuery(t *testing.T) {
	tests := []struct {
		query    string
		expected bool
	}{
		{"SELECT * FROM users", true},
		{"  SELECT id FROM pages", true},
		{"select name from tags", true},
		{"PRAGMA table_info(users)", true},
		{"EXPLAIN QUERY PLAN SELECT * FROM users", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO users (name) VALUES ('test')", false},
		{"UPDATE users SET name = 'test'", false},
		{"DELETE FROM users WHERE id = 1", false},
		{"CREATE TABLE test (id INTEGER)", false},
		{"DROP TABLE test", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := isSelectQuery(tt.query)
			if got != tt.expected {
				t.Errorf("isSelectQuery(%q) = %v, want %v", tt.query, got, tt.expected)
			}
		})
	}
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{"nil", nil, "NULL"},
		{"string", "hello", "hello"},
		{"bytes", []byte("world"), "world"},
		{"int", 42, "42"},
		{"int64", int64(123456), "123456"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatValue(tt.value)
			if got != tt.expected {
				t.Errorf("formatValue(%v) = %q, want %q", tt.value, got, tt.expected)
			}
		})
	}
}

func TestExecuteSelectQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Execute a simple SELECT query
	result := m.executeQuery(context.Background(), "SELECT 1 AS value", 1)

	if result.Error != "" {
		t.Errorf("executeQuery error: %s", result.Error)
	}
	if !result.IsSelect {
		t.Error("IsSelect should be true")
	}
	if len(result.Columns) != 1 || result.Columns[0] != "value" {
		t.Errorf("Columns = %v, want [value]", result.Columns)
	}
	if len(result.Rows) != 1 || result.Rows[0][0] != "1" {
		t.Errorf("Rows = %v, want [[1]]", result.Rows)
	}
}

func TestExecuteInvalidQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Execute an invalid query
	result := m.executeQuery(context.Background(), "SELECT * FROM nonexistent_table", 1)

	if result.Error == "" {
		t.Error("Expected error for nonexistent table")
	}
}

func TestExecuteInsertQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Create a temp table first
	_, err := db.Exec("CREATE TABLE temp_test (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("Failed to create temp table: %v", err)
	}

	// Execute INSERT query
	result := m.executeQuery(context.Background(), "INSERT INTO temp_test (name) VALUES ('test')", 1)

	if result.Error != "" {
		t.Errorf("executeQuery error: %s", result.Error)
	}
	if result.IsSelect {
		t.Error("IsSelect should be false for INSERT")
	}
	if result.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", result.RowsAffected)
	}
}

func TestQueryHistory(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Execute a query to add to history
	_ = m.executeQuery(context.Background(), "SELECT 1", 1)

	// Get history
	history, err := m.getQueryHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("getQueryHistory: %v", err)
	}

	if len(history) < 1 {
		t.Error("Expected at least 1 query in history")
	}

	// Verify the query is in history
	found := false
	for _, h := range history {
		if h.Query == "SELECT 1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Query 'SELECT 1' not found in history")
	}
}

func TestIntToString(t *testing.T) {
	tests := []struct {
		value    int64
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{123, "123"},
		{-456, "-456"},
		{1234567890, "1234567890"},
	}

	for _, tt := range tests {
		got := intToString(tt.value)
		if got != tt.expected {
			t.Errorf("intToString(%d) = %q, want %q", tt.value, got, tt.expected)
		}
	}
}

func TestUintToString(t *testing.T) {
	tests := []struct {
		value    uint64
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{123, "123"},
		{1234567890, "1234567890"},
	}

	for _, tt := range tests {
		got := uintToString(tt.value)
		if got != tt.expected {
			t.Errorf("uintToString(%d) = %q, want %q", tt.value, got, tt.expected)
		}
	}
}
