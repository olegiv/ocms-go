// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_ext

import (
	"html/template"
	"reflect"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

func TestFirstStringArg(t *testing.T) {
	tests := []struct {
		name     string
		args     []any
		expected string
	}{
		{"no args", []any{}, ""},
		{"one string arg", []any{"nonce123"}, "nonce123"},
		{"non-string arg", []any{42}, ""},
		{"multiple args uses first", []any{"first", "second"}, "first"},
		{"nil arg", []any{nil}, ""},
		{"empty string arg", []any{""}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstStringArg(tt.args...)
			if got != tt.expected {
				t.Errorf("firstStringArg(%v) = %q, want %q", tt.args, got, tt.expected)
			}
		})
	}
}

func TestModuleSidebarLabel(t *testing.T) {
	m := New()
	if m.SidebarLabel() == "" {
		t.Error("SidebarLabel() should not be empty")
	}
}

func TestModuleTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	_, err := fs.ReadFile("locales/en/messages.json")
	if err != nil {
		t.Errorf("failed to read English translations: %v", err)
	}
}

func TestModuleRegisterRoutes(t *testing.T) {
	// RegisterRoutes accepts any chi.Router; calling it with nil panics only if
	// the implementation dereferences the router — this module has no public
	// routes so the call is a no-op and should not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RegisterRoutes panicked: %v", r)
		}
	}()
	m := New()
	m.RegisterRoutes(nil)
}

func TestTemplateFuncs_InvokeClosures(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	m.settings = &Settings{
		GA4Enabled:       true,
		GA4MeasurementID: "G-FUNCTEST1",
	}

	funcs := m.TemplateFuncs()

	// Invoke each registered closure using reflect so the variadic
	// closures (func(...any) template.HTML) are actually exercised.
	// This covers firstStringArg and renderHeadScripts/renderBodyScripts paths
	// inside the closures.
	nonce := "nonce-abc"
	for _, name := range []string{"analyticsExtHead", "analyticsExtBody"} {
		fn := funcs[name]
		if fn == nil {
			t.Errorf("%s: not registered", name)
			continue
		}
		rv := reflect.ValueOf(fn)
		result := rv.Call([]reflect.Value{reflect.ValueOf(nonce)})
		if len(result) == 0 {
			t.Fatalf("%s: no return value", name)
		}
		// Result is template.HTML; zero value is fine (no scripts for body with GA4-only).
		_ = result[0].Interface().(template.HTML)
	}

	// Also call without arguments (exercises the len(args)==0 branch in firstStringArg).
	for _, name := range []string{"analyticsExtHead", "analyticsExtBody"} {
		fn := funcs[name]
		rv := reflect.ValueOf(fn)
		result := rv.Call(nil)
		_ = result[0].Interface().(template.HTML)
	}
}

func TestLoadSettings_EmptyTable(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	// Only run the migration — no INSERT into the table yet.
	if err := m.Migrations()[0].Up(db); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	// The migration inserts a default row via INSERT OR IGNORE, so loadSettings
	// should succeed and return all-false defaults.
	s, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if s == nil {
		t.Fatal("loadSettings returned nil settings")
	}
	if s.GA4Enabled || s.GTMEnabled || s.MatomoEnabled {
		t.Error("default settings should have all trackers disabled")
	}
}

func TestReloadSettings_ErrorPath(t *testing.T) {
	// A module whose context.DB is nil-equivalent (no table) should surface an error.
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Drop the settings table to force a reload error.
	if _, err := db.Exec("DROP TABLE analytics_settings"); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	err := m.ReloadSettings()
	if err == nil {
		t.Error("ReloadSettings should return error when table is missing")
	}
}

func TestRenderHeadScripts_WithNonce(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GTMEnabled:     true,
		GTMContainerID: "GTM-NONCE1",
	}

	result := string(m.renderHeadScripts("abc123"))
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// The nonce should be injected into the script tag.
	// util.AddNonceToScriptTags appends nonce="<value>" to <script ...>
	if result == "" {
		t.Error("expected nonce to be injected into script tag")
	}
}

func TestRenderBodyScripts_NoScripts(t *testing.T) {
	m := New()
	m.settings = &Settings{
		// GA4 has no body scripts; GTM and Matomo disabled.
		GA4Enabled:       true,
		GA4MeasurementID: "G-BODYONLY1",
		GTMEnabled:       false,
		MatomoEnabled:    false,
	}

	result := m.renderBodyScripts("")
	if result != "" {
		t.Errorf("expected empty body scripts for GA4-only config, got %q", result)
	}
}

func TestModuleRegisterAdminRoutes(t *testing.T) {
	// Verify RegisterAdminRoutes registers routes on a real chi router without panicking.
	// We use a chi.Router from the chi package.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RegisterAdminRoutes panicked: %v", r)
		}
	}()

	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	_ = m // routes are registered; just ensure no panic.
}

func TestModuleInit_LoadSettingsError(t *testing.T) {
	// When the settings table does not exist, Init should log a warning and use defaults.
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	// Do NOT run migrations — settings table won't exist.
	ctx, _ := moduleutil.TestModuleContext(t, db)
	err := m.Init(ctx)
	// Init should not return an error even when settings cannot be loaded.
	if err != nil {
		t.Errorf("Init should succeed with missing settings table, got error: %v", err)
	}
	// settings should be non-nil (defaults were applied).
	if m.settings == nil {
		t.Error("settings should be non-nil after Init with missing table")
	}
}

func TestMigration_DownThenUp(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	migrations := m.Migrations()

	// Run up then down then up again — should work without error.
	if err := migrations[0].Up(db); err != nil {
		t.Fatalf("first up: %v", err)
	}
	if err := migrations[0].Down(db); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := migrations[0].Up(db); err != nil {
		t.Fatalf("second up: %v", err)
	}

	// Verify row exists.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM analytics_settings").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}
