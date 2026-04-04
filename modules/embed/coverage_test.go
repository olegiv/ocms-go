// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"html/template"
	"reflect"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
	"github.com/olegiv/ocms-go/modules/embed/providers"
)

// ---------------------------------------------------------------------------
// Module helpers
// ---------------------------------------------------------------------------

// testModule creates a fully initialized embed Module backed by a test DB.
func testModule(t *testing.T, db interface{ Exec(string, ...any) (interface{}, error) }) *Module {
	t.Helper()
	return nil // unused; use testModuleDB below
}

// testModuleDB creates a fully initialized embed Module backed by a test DB.
func testModuleDB(t *testing.T) (*Module, func()) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		cleanup()
		t.Fatalf("embed.Module.Init: %v", err)
	}
	return m, cleanup
}

// ---------------------------------------------------------------------------
// firstStringArg
// ---------------------------------------------------------------------------

func TestFirstStringArg(t *testing.T) {
	tests := []struct {
		name     string
		args     []any
		expected string
	}{
		{"no args", []any{}, ""},
		{"one string", []any{"abc"}, "abc"},
		{"non-string", []any{99}, ""},
		{"nil", []any{nil}, ""},
		{"multiple", []any{"first", "second"}, "first"},
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

// ---------------------------------------------------------------------------
// Module metadata
// ---------------------------------------------------------------------------

func TestModuleSidebarLabel(t *testing.T) {
	m := New()
	if m.SidebarLabel() == "" {
		t.Error("SidebarLabel() should not be empty")
	}
}

func TestModuleTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	if _, err := fs.ReadFile("locales/en/messages.json"); err != nil {
		t.Errorf("failed to read en translations: %v", err)
	}
}

func TestModuleShutdown(t *testing.T) {
	m := New()
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown before Init: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestModuleInit(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()
	defer func() { _ = m.Shutdown() }()

	if m.ctx == nil {
		t.Error("ctx should be set after Init")
	}
}

func TestModuleInit_MissingTable(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	// Do NOT run migrations — embed_settings table won't exist.
	m := New()
	ctx, _ := moduleutil.TestModuleContext(t, db)
	// Init should succeed even when settings can't be loaded (logs a warning).
	if err := m.Init(ctx); err != nil {
		t.Errorf("Init without migrations: %v", err)
	}
}

// ---------------------------------------------------------------------------
// countEnabled / reloadSettings
// ---------------------------------------------------------------------------

func TestCountEnabled_Empty(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	count := m.countEnabled()
	if count != 0 {
		t.Errorf("countEnabled() = %d, want 0 on fresh DB", count)
	}
}

func TestCountEnabled_AfterEnable(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	// Enable the dify provider directly in DB.
	ps := &ProviderSettings{
		ProviderID: "dify",
		Settings: map[string]string{
			"api_endpoint": "https://api.dify.ai/v1",
			"api_key":      "app-testkey",
		},
		IsEnabled: true,
	}
	if err := saveProviderSettings(db, ps); err != nil {
		t.Fatalf("saveProviderSettings: %v", err)
	}

	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}

	count := m.countEnabled()
	if count != 1 {
		t.Errorf("countEnabled() = %d, want 1 after enabling dify", count)
	}
}

func TestReloadSettings(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	// reloadSettings should not return error on fresh DB.
	if err := m.reloadSettings(); err != nil {
		t.Errorf("reloadSettings: %v", err)
	}
}

func TestReloadSettings_Public(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	// ReloadSettings (public method) should not return error on fresh DB.
	if err := m.ReloadSettings(); err != nil {
		t.Errorf("ReloadSettings: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Settings CRUD
// ---------------------------------------------------------------------------

func TestLoadProviderSettings_NotFound(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	ps, err := loadProviderSettings(db, "nonexistent")
	if err != nil {
		t.Fatalf("loadProviderSettings: %v", err)
	}
	if ps == nil {
		t.Fatal("expected empty ProviderSettings, got nil")
	}
	if ps.IsEnabled {
		t.Error("expected IsEnabled=false for nonexistent provider")
	}
}

func TestSaveAndLoadProviderSettings(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	ps := &ProviderSettings{
		ProviderID: "dify",
		Settings: map[string]string{
			"api_endpoint": "https://api.dify.ai/v1",
			"api_key":      "app-testkey123",
			"bot_name":     "Test Bot",
		},
		IsEnabled: true,
		Position:  1,
	}

	if err := saveProviderSettings(db, ps); err != nil {
		t.Fatalf("saveProviderSettings: %v", err)
	}

	loaded, err := loadProviderSettings(db, "dify")
	if err != nil {
		t.Fatalf("loadProviderSettings: %v", err)
	}

	if !loaded.IsEnabled {
		t.Error("expected IsEnabled=true")
	}
	if loaded.Settings["api_key"] != "app-testkey123" {
		t.Errorf("api_key = %q, want app-testkey123", loaded.Settings["api_key"])
	}
	if loaded.Settings["bot_name"] != "Test Bot" {
		t.Errorf("bot_name = %q, want 'Test Bot'", loaded.Settings["bot_name"])
	}
}

func TestLoadAllSettings(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	// Insert two providers (one enabled, one disabled).
	ps1 := &ProviderSettings{ProviderID: "dify", Settings: map[string]string{"api_endpoint": "https://api.dify.ai/v1", "api_key": "k1"}, IsEnabled: true}
	ps2 := &ProviderSettings{ProviderID: "other", Settings: map[string]string{}, IsEnabled: false}

	if err := saveProviderSettings(db, ps1); err != nil {
		t.Fatalf("save ps1: %v", err)
	}
	if err := saveProviderSettings(db, ps2); err != nil {
		t.Fatalf("save ps2: %v", err)
	}

	all, err := loadAllSettings(db)
	if err != nil {
		t.Fatalf("loadAllSettings: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("loadAllSettings() = %d rows, want 2", len(all))
	}

	enabled, err := loadAllEnabledSettings(db)
	if err != nil {
		t.Fatalf("loadAllEnabledSettings: %v", err)
	}
	if len(enabled) != 1 {
		t.Errorf("loadAllEnabledSettings() = %d rows, want 1", len(enabled))
	}
}

func TestToggleProvider(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	// Toggle a provider that doesn't exist yet — should create it.
	if err := toggleProvider(db, "dify", true); err != nil {
		t.Fatalf("toggleProvider (enable): %v", err)
	}

	ps, err := loadProviderSettings(db, "dify")
	if err != nil {
		t.Fatalf("loadProviderSettings: %v", err)
	}
	if !ps.IsEnabled {
		t.Error("expected provider to be enabled after toggle")
	}

	// Toggle off.
	if err := toggleProvider(db, "dify", false); err != nil {
		t.Fatalf("toggleProvider (disable): %v", err)
	}

	ps2, err := loadProviderSettings(db, "dify")
	if err != nil {
		t.Fatalf("loadProviderSettings: %v", err)
	}
	if ps2.IsEnabled {
		t.Error("expected provider to be disabled after toggle")
	}
}

// ---------------------------------------------------------------------------
// getProvider (internal lookup)
// ---------------------------------------------------------------------------

func TestGetProvider_Found(t *testing.T) {
	m := New()
	p := m.getProvider("dify")
	if p == nil {
		t.Fatal("getProvider('dify') should not return nil")
	}
	if p.ID() != "dify" {
		t.Errorf("getProvider('dify').ID() = %q, want 'dify'", p.ID())
	}
}

func TestGetProvider_NotFound(t *testing.T) {
	m := New()
	p := m.getProvider("nonexistent")
	if p != nil {
		t.Errorf("getProvider('nonexistent') should return nil, got %v", p)
	}
}

// ---------------------------------------------------------------------------
// getEnabledProviderSettings
// ---------------------------------------------------------------------------

func TestGetEnabledProviderSettings_NotFound(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	_, ok := m.getEnabledProviderSettings("dify")
	if ok {
		t.Error("expected ok=false when no enabled settings")
	}
}

func TestGetEnabledProviderSettings_Found(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	// Enable dify with settings.
	ps := &ProviderSettings{
		ProviderID: "dify",
		Settings:   map[string]string{"api_endpoint": "https://api.dify.ai/v1", "api_key": "app-k"},
		IsEnabled:  true,
	}
	if err := saveProviderSettings(db, ps); err != nil {
		t.Fatalf("saveProviderSettings: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}

	settings, ok := m.getEnabledProviderSettings("dify")
	if !ok {
		t.Fatal("expected ok=true for enabled dify provider")
	}
	if settings["api_key"] != "app-k" {
		t.Errorf("api_key = %q, want 'app-k'", settings["api_key"])
	}
}

// ---------------------------------------------------------------------------
// renderHead / renderBody
// ---------------------------------------------------------------------------

func TestRenderHead_NoEnabledProviders(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	result := m.renderHead("nonce123")
	if result != "" {
		t.Errorf("renderHead() with no enabled providers = %q, want empty", result)
	}
}

func TestRenderBody_NoEnabledProviders(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	result := m.renderBody("nonce123")
	if result != "" {
		t.Errorf("renderBody() with no enabled providers = %q, want empty", result)
	}
}

func TestRenderBody_WithEnabledProvider(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	// Enable dify with valid settings so RenderBody produces output.
	ps := &ProviderSettings{
		ProviderID: "dify",
		Settings: map[string]string{
			"api_endpoint": "https://api.dify.ai/v1",
			"api_key":      "app-testkey",
		},
		IsEnabled: true,
	}
	if err := saveProviderSettings(db, ps); err != nil {
		t.Fatalf("saveProviderSettings: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}

	result := string(m.renderBody(""))
	if !strings.Contains(result, "dify-chat-widget") {
		t.Error("expected dify widget HTML in renderBody output")
	}
}

func TestRenderHead_WithEnabledProvider(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	// Enable dify — dify's RenderHead returns empty string.
	ps := &ProviderSettings{
		ProviderID: "dify",
		Settings: map[string]string{
			"api_endpoint": "https://api.dify.ai/v1",
			"api_key":      "app-testkey",
		},
		IsEnabled: true,
	}
	if err := saveProviderSettings(db, ps); err != nil {
		t.Fatalf("saveProviderSettings: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}

	// Dify returns empty head. Just confirm no panic.
	result := m.renderHead("nonce-test")
	_ = result
}

func TestRenderScripts_UnknownProvider(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	// Inject a settings entry for an unknown provider ID.
	m.mu.Lock()
	m.settings = append(m.settings, &ProviderSettings{
		ProviderID: "unknown-provider",
		Settings:   map[string]string{},
		IsEnabled:  true,
	})
	m.mu.Unlock()

	// renderHead should skip unknown providers without panicking.
	result := m.renderHead("")
	if result != "" {
		t.Errorf("renderHead with unknown provider = %q, want empty", result)
	}
}

// ---------------------------------------------------------------------------
// TemplateFuncs closures
// ---------------------------------------------------------------------------

func TestTemplateFuncs_Invoke(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	funcs := m.TemplateFuncs()

	for _, name := range []string{"embedHead", "embedBody"} {
		fn := funcs[name]
		if fn == nil {
			t.Errorf("%s not registered", name)
			continue
		}
		// Invoke with a nonce arg via reflect to exercise the closures.
		rv := reflect.ValueOf(fn)
		result := rv.Call([]reflect.Value{reflect.ValueOf("nonce-test")})
		if len(result) == 0 {
			t.Errorf("%s: no return value", name)
		}
		_ = result[0].Interface().(template.HTML)

		// Also invoke without args (exercises len(args)==0 in firstStringArg).
		result2 := rv.Call(nil)
		_ = result2[0].Interface().(template.HTML)
	}
}

// ---------------------------------------------------------------------------
// Migration up/down
// ---------------------------------------------------------------------------

func TestMigrationUpDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	migrations := m.Migrations()

	for _, mig := range migrations {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration %d Up: %v", mig.Version, err)
		}
	}

	for i := len(migrations) - 1; i >= 0; i-- {
		if err := migrations[i].Down(db); err != nil {
			t.Fatalf("migration %d Down: %v", migrations[i].Version, err)
		}
	}

	moduleutil.AssertTableNotExists(t, db, "embed_settings")
}

// ---------------------------------------------------------------------------
// Provider interface coverage (providers package)
// ---------------------------------------------------------------------------

func TestDifyProvider_Description(t *testing.T) {
	p := providers.NewDify()
	if p.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestDifyProvider_SettingsSchema_Required(t *testing.T) {
	p := providers.NewDify()
	schema := p.SettingsSchema()

	for _, f := range schema {
		if f.ID == "" {
			t.Error("SettingField.ID should not be empty")
		}
		if f.Name == "" {
			t.Errorf("SettingField(%q).Name should not be empty", f.ID)
		}
		if f.Type == "" {
			t.Errorf("SettingField(%q).Type should not be empty", f.ID)
		}
	}
}
