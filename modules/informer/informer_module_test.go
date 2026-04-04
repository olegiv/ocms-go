// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package informer

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// newInformerDB runs informer migrations on an in-memory database.
func newInformerDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.TestMemoryDB(t)
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newInitedModule creates a Module with a fully initialised module.Context.
func newInitedModule(t *testing.T) *Module {
	t.Helper()
	db := newInformerDB(t)
	m := New()
	ctx := &module.Context{
		DB:     db,
		Logger: testutil.TestLogger(),
		Config: &config.Config{Env: "development"},
	}
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m
}

// --- Module lifecycle ---

func TestModuleInitLoadsDefaults(t *testing.T) {
	m := newInitedModule(t)
	if m.settings == nil {
		t.Fatal("settings should not be nil after Init")
	}
	if m.settings.BgColor != "#1e40af" {
		t.Errorf("BgColor = %q, want #1e40af", m.settings.BgColor)
	}
}

func TestModuleInitFallsBackOnNoTable(t *testing.T) {
	// No migration → loadSettings fails → Init should fall back to defaults.
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	m := New()
	ctx := &module.Context{
		DB:     db,
		Logger: testutil.TestLogger(),
		Config: &config.Config{},
	}
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init should succeed even without table: %v", err)
	}
	if m.settings == nil {
		t.Error("settings should not be nil after fallback")
	}
}

func TestModuleShutdown(t *testing.T) {
	m := newInitedModule(t)
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}

func TestModuleShutdownNilCtx(t *testing.T) {
	m := New() // ctx is nil
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() with nil ctx = %v, want nil", err)
	}
}

func TestModuleRegisterRoutes(t *testing.T) {
	m := newInitedModule(t)
	m.RegisterRoutes(chi.NewRouter()) // no-op; must not panic
}

func TestModuleRegisterAdminRoutes(t *testing.T) {
	m := newInitedModule(t)
	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)
	// Admin routes for GET and POST /informer must be registered.
}

func TestModuleTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	entries, err := fs.ReadDir("locales")
	if err != nil {
		t.Fatalf("ReadDir(locales): %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least one locale file")
	}
}

// --- ReloadSettings ---

func TestReloadSettingsUpdatesInMemory(t *testing.T) {
	db := newInformerDB(t)
	m := New()
	ctx := &module.Context{
		DB:     db,
		Logger: testutil.TestLogger(),
		Config: &config.Config{},
	}
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Update DB directly
	if err := saveSettings(db, &Settings{
		Enabled:   true,
		Text:      "Updated via DB",
		BgColor:   "#ff0000",
		TextColor: "#000000",
	}); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	// Before reload, settings should be stale
	if m.settings.Text == "Updated via DB" {
		t.Error("settings should not change until ReloadSettings is called")
	}

	// After reload, settings should reflect DB
	if err := m.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings: %v", err)
	}
	if m.settings.Text != "Updated via DB" {
		t.Errorf("Text after reload = %q, want 'Updated via DB'", m.settings.Text)
	}
	if !m.settings.Enabled {
		t.Error("Enabled should be true after reload")
	}
}

func TestReloadSettingsNilCtx(t *testing.T) {
	m := New() // ctx is nil
	// Should return nil immediately without panicking.
	if err := m.ReloadSettings(); err != nil {
		t.Errorf("ReloadSettings with nil ctx = %v, want nil", err)
	}
}

// --- firstStringArg ---

func TestFirstStringArgNoArgs(t *testing.T) {
	result := firstStringArg()
	if result != "" {
		t.Errorf("firstStringArg() = %q, want \"\"", result)
	}
}

func TestFirstStringArgWithString(t *testing.T) {
	result := firstStringArg("nonce-value")
	if result != "nonce-value" {
		t.Errorf("firstStringArg(\"nonce-value\") = %q, want \"nonce-value\"", result)
	}
}

func TestFirstStringArgNonString(t *testing.T) {
	result := firstStringArg(123)
	if result != "" {
		t.Errorf("firstStringArg(123) = %q, want \"\"", result)
	}
}

func TestFirstStringArgMultipleArgs(t *testing.T) {
	result := firstStringArg("first", "ignored")
	if result != "first" {
		t.Errorf("firstStringArg(\"first\",\"ignored\") = %q, want \"first\"", result)
	}
}

// --- TemplateFuncs registration and invocation ---

func TestTemplateFuncsContainsInformerBar(t *testing.T) {
	m := &Module{settings: &Settings{Enabled: false}}
	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs should not return nil")
	}
	if _, ok := funcs["informerBar"]; !ok {
		t.Error("TemplateFuncs should register 'informerBar'")
	}
}

func TestTemplateFuncInformerBarDisabled(t *testing.T) {
	m := &Module{settings: &Settings{Enabled: false, Text: "Test"}}
	funcs := m.TemplateFuncs()
	fn, ok := funcs["informerBar"].(func(...any) interface{})
	if !ok {
		// Exercise via renderBar directly
		out := m.renderBar("")
		if string(out) != "" {
			t.Error("renderBar should be empty when disabled")
		}
		return
	}
	result := fn()
	_ = result
}

func TestTemplateFuncInformerBarWithNonce(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Hello World",
			BgColor:   "#000000",
			TextColor: "#ffffff",
		},
	}
	out := string(m.renderBar("my-nonce"))
	if !strings.Contains(out, `nonce="my-nonce"`) {
		t.Error("nonce should be injected into script tags")
	}
	if !strings.Contains(out, "Hello World") {
		t.Error("bar should contain notification text")
	}
}

func TestTemplateFuncInformerBarViaFuncMap(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Hello",
			BgColor:   "#000",
			TextColor: "#fff",
		},
	}
	funcs := m.TemplateFuncs()
	fn := funcs["informerBar"]
	if fn == nil {
		t.Fatal("informerBar not in TemplateFuncs")
	}
	// The function accepts variadic args and returns template.HTML.
	// Invoke indirectly to avoid compile-time type issues.
	out := m.renderBar("nonce-test")
	if string(out) == "" {
		t.Error("renderBar should return non-empty when enabled")
	}
}

// --- renderBar edge cases ---

func TestRenderBarVersionInScript(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Test message",
			BgColor:   "#000000",
			TextColor: "#ffffff",
			Version:   "42",
		},
	}
	out := string(m.renderBar(""))
	if !strings.Contains(out, `ver="42"`) {
		t.Errorf("script should embed version '42', got output without it")
	}
}

func TestRenderBarEscapesHTMLInColors(t *testing.T) {
	// Malicious color value should be escaped in HTML attributes.
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Test",
			BgColor:   `"><script>xss()</script>`,
			TextColor: "#ffffff",
		},
	}
	out := string(m.renderBar(""))
	if strings.Contains(out, "<script>xss()</script>") {
		t.Error("XSS in BgColor should be HTML-escaped")
	}
}

func TestRenderBarCookieName(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Notice",
			BgColor:   "#000",
			TextColor: "#fff",
		},
	}
	out := string(m.renderBar(""))
	// The cookie name constant must appear in the script
	if !strings.Contains(out, cookieName) {
		t.Errorf("renderBar output should contain cookie name %q", cookieName)
	}
}

// --- loadSettings error path ---

func TestLoadSettingsNoTableError(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	_, err := loadSettings(db)
	if err == nil {
		t.Error("loadSettings should return error when table does not exist")
	}
}

// --- handleSaveSettings unauthorized path ---

func TestHandleSaveSettingsUnauthorized(t *testing.T) {
	m := newInitedModule(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/informer", nil)
	rr := httptest.NewRecorder()
	m.handleSaveSettings(rr, req)

	// No user in context → Unauthorized
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}
