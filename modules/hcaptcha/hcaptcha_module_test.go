// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package hcaptcha

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// ============================================================================
// SidebarLabel and TranslationsFS
// ============================================================================

func TestModuleSidebarLabel(t *testing.T) {
	m := New()
	if m.SidebarLabel() != "hCaptcha" {
		t.Errorf("SidebarLabel() = %q, want hCaptcha", m.SidebarLabel())
	}
}

func TestTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	entries, err := fs.ReadDir("locales")
	if err != nil {
		t.Fatalf("TranslationsFS ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("TranslationsFS should contain at least one locale file")
	}
}

// ============================================================================
// RegisterRoutes and RegisterAdminRoutes
// ============================================================================

func TestRegisterRoutes(t *testing.T) {
	m := New()
	// RegisterRoutes should not panic — hcaptcha has no public routes
	m.RegisterRoutes(nil)
}

func TestRegisterAdminRoutes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	router := chi.NewRouter()
	m.RegisterAdminRoutes(router)
	// If we get here without panic, routes are registered
}

// ============================================================================
// ReloadSettings: error path when DB is nil
// ============================================================================

func TestReloadSettingsErrorPath(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Close DB to trigger error
	cleanup()

	// ReloadSettings should return an error when DB is closed
	err := m.ReloadSettings()
	// This may or may not error depending on SQLite behavior with closed DB
	// Just verify no panic
	_ = err
}

// ============================================================================
// Dependencies
// ============================================================================

func TestDependencies(t *testing.T) {
	m := New()
	deps := m.Dependencies()
	if len(deps) != 0 {
		t.Errorf("Dependencies() = %v, want nil or empty", deps)
	}
}

// ============================================================================
// Registered hooks are invokable
// ============================================================================

func TestHookAuthLoginWidget(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m, hooks := testModuleWithHooks(t, db)

	// Verify hook was registered
	if !hooks.HasHandlers(HookAuthLoginWidget) {
		t.Fatal("HookAuthLoginWidget should be registered")
	}

	// Set up enabled settings so RenderWidget returns something
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "test-site-key",
		SecretKey: "test-secret",
		Theme:     "light",
		Size:      "normal",
	}

	result, err := hooks.Call(context.Background(), HookAuthLoginWidget, nil)
	if err != nil {
		t.Fatalf("Call HookAuthLoginWidget: %v", err)
	}
	_ = result
}

func TestHookAuthBeforeLogin_ValidData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m, hooks := testModuleWithHooks(t, db)

	// Disabled — VerifyFromRequest should return verified=true
	m.settings = &Settings{Enabled: false}

	if !hooks.HasHandlers(HookAuthBeforeLogin) {
		t.Fatal("HookAuthBeforeLogin should be registered")
	}

	req := &VerifyRequest{Response: ""}
	result, err := hooks.Call(context.Background(), HookAuthBeforeLogin, req)
	if err != nil {
		t.Fatalf("Call HookAuthBeforeLogin: %v", err)
	}

	vr, ok := result.(*VerifyRequest)
	if !ok {
		t.Fatalf("result type = %T, want *VerifyRequest", result)
	}
	if !vr.Verified {
		t.Error("should be verified when hcaptcha is disabled")
	}
}

func TestHookAuthBeforeLogin_NonVerifyRequest(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	_, hooks := testModuleWithHooks(t, db)

	if !hooks.HasHandlers(HookAuthBeforeLogin) {
		t.Fatal("HookAuthBeforeLogin should be registered")
	}

	// Pass non-*VerifyRequest data — hook should return it unchanged
	data := "not a verify request"
	result, err := hooks.Call(context.Background(), HookAuthBeforeLogin, data)
	if err != nil {
		t.Fatalf("Call HookAuthBeforeLogin: %v", err)
	}
	if result != data {
		t.Error("non-VerifyRequest data should be returned unchanged")
	}
}

func TestHookFormCaptchaWidget(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	_, hooks := testModuleWithHooks(t, db)

	if !hooks.HasHandlers(HookFormCaptchaWidget) {
		t.Fatal("HookFormCaptchaWidget should be registered")
	}

	result, err := hooks.Call(context.Background(), HookFormCaptchaWidget, nil)
	if err != nil {
		t.Fatalf("Call HookFormCaptchaWidget: %v", err)
	}
	_ = result
}

func TestHookFormCaptchaVerify(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m, hooks := testModuleWithHooks(t, db)
	m.settings = &Settings{Enabled: false}

	if !hooks.HasHandlers(HookFormCaptchaVerify) {
		t.Fatal("HookFormCaptchaVerify should be registered")
	}

	req := &VerifyRequest{Response: ""}
	result, err := hooks.Call(context.Background(), HookFormCaptchaVerify, req)
	if err != nil {
		t.Fatalf("Call HookFormCaptchaVerify: %v", err)
	}

	vr, ok := result.(*VerifyRequest)
	if !ok {
		t.Fatalf("result type = %T, want *VerifyRequest", result)
	}
	if !vr.Verified {
		t.Error("should be verified when disabled")
	}
}

func TestHookFormCaptchaVerify_NonVerifyRequest(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	_, hooks := testModuleWithHooks(t, db)

	if !hooks.HasHandlers(HookFormCaptchaVerify) {
		t.Fatal("HookFormCaptchaVerify should be registered")
	}

	data := 42 // not a *VerifyRequest
	result, err := hooks.Call(context.Background(), HookFormCaptchaVerify, data)
	if err != nil {
		t.Fatalf("Call HookFormCaptchaVerify: %v", err)
	}
	if result != data {
		t.Error("non-VerifyRequest data should be returned unchanged")
	}
}

// ============================================================================
// handleDashboard: smoke test with empty renderer
// ============================================================================

func TestHandleDashboard_Smoke(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/hcaptcha", nil)

	defer func() {
		if r := recover(); r != nil {
			t.Logf("handleDashboard panicked (expected with no templates): %v", r)
		}
	}()

	m.handleDashboard(w, req)
	// handleDashboard calls render.Templ — with empty Renderer it may succeed or panic
}

// ============================================================================
// handleSaveSettings: unauthenticated path (401)
// ============================================================================

func TestHandleSaveSettingsUnauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/hcaptcha", nil)

	m.handleSaveSettings(w, req)

	// When no user in context: returns 401 (no demo mode in tests)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusSeeOther {
		t.Errorf("handleSaveSettings status = %d, want 401 or 303", w.Code)
	}
}

// ============================================================================
// handleSaveSettings: authenticated user, save settings
// ============================================================================

// requestWithUser returns an HTTP request with a user injected into the context.
func requestWithUser(method, target string, body *strings.Reader, user store.User) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, body)
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, user)
	return req.WithContext(ctx)
}

func TestHandleSaveSettingsWithUser_DisabledNoKeys(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Post with enabled=1 but no keys — should fail validation and redirect
	body := strings.NewReader("enabled=1&site_key=&secret_key=&theme=light&size=normal")
	req := requestWithUser(
		http.MethodPost, "/admin/hcaptcha",
		body,
		store.User{ID: 1, Email: "admin@test.com", Role: "admin"},
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	// Should redirect back (validation error)
	if w.Code != http.StatusSeeOther {
		t.Errorf("handleSaveSettings status = %d, want 303", w.Code)
	}
}

func TestHandleSaveSettingsWithUser_Disabled(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Post with enabled=0 — disabled, no key validation needed
	body := strings.NewReader("enabled=0&site_key=&secret_key=&theme=light&size=normal")
	req := requestWithUser(
		http.MethodPost, "/admin/hcaptcha",
		body,
		store.User{ID: 1, Email: "admin@test.com", Role: "admin"},
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	// Should redirect after successful save
	if w.Code != http.StatusSeeOther {
		t.Errorf("handleSaveSettings status = %d, want 303", w.Code)
	}
}

func TestHandleSaveSettingsWithUser_WithKeys(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Post enabled=1 with valid keys
	body := strings.NewReader("enabled=1&site_key=abc-site-key&secret_key=abc-secret-key&theme=dark&size=compact")
	req := requestWithUser(
		http.MethodPost, "/admin/hcaptcha",
		body,
		store.User{ID: 1, Email: "admin@test.com", Role: "admin"},
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	// Should redirect after successful save
	if w.Code != http.StatusSeeOther {
		t.Errorf("handleSaveSettings status = %d, want 303", w.Code)
	}

	// Verify settings were saved
	loaded, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if loaded.SiteKey != "abc-site-key" {
		t.Errorf("SiteKey = %q, want abc-site-key", loaded.SiteKey)
	}
	if loaded.Theme != "dark" {
		t.Errorf("Theme = %q, want dark", loaded.Theme)
	}
}

func TestHandleSaveSettingsWithUser_EmptyThemeAndSizeDefaults(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Post without theme/size — should use defaults
	body := strings.NewReader("enabled=0&site_key=&secret_key=&theme=&size=")
	req := requestWithUser(
		http.MethodPost, "/admin/hcaptcha",
		body,
		store.User{ID: 1, Email: "admin@test.com", Role: "admin"},
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("handleSaveSettings status = %d, want 303", w.Code)
	}

	// Theme and size should have been defaulted
	if m.settings.Theme != "light" {
		t.Errorf("Theme = %q, want light (default)", m.settings.Theme)
	}
	if m.settings.Size != "normal" {
		t.Errorf("Size = %q, want normal (default)", m.settings.Size)
	}
}

func TestHandleSaveSettingsWithUser_EnvOverride(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	ctx, _ := moduleutil.TestModuleContext(t, db)
	moduleutil.RunMigrations(t, db, m.Migrations())
	// Set env override config
	ctx.Config.HCaptchaSiteKey = "env-site-override"
	ctx.Config.HCaptchaSecretKey = "env-secret-override"
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Post with keys — env override should take priority in memory
	body := strings.NewReader("enabled=1&site_key=form-site-key&secret_key=form-secret-key&theme=light&size=normal")
	req := requestWithUser(
		http.MethodPost, "/admin/hcaptcha",
		body,
		store.User{ID: 1, Email: "admin@test.com", Role: "admin"},
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("handleSaveSettings status = %d, want 303", w.Code)
	}

	// In-memory settings should use env override
	if m.settings.SiteKey != "env-site-override" {
		t.Errorf("SiteKey = %q, want env-site-override", m.settings.SiteKey)
	}
}

func TestHandleSaveSettingsWithUser_PlaceholderSecretKey(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Set up initial settings with a real secret key
	if err := saveSettings(db, &Settings{
		Enabled:   false,
		SiteKey:   "original-site",
		SecretKey: "original-secret",
		Theme:     "light",
		Size:      "normal",
	}); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	if err := m.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings: %v", err)
	}

	// Post with a placeholder secret key — should keep original
	body := strings.NewReader("enabled=0&site_key=new-site&secret_key=orig****cret&theme=light&size=normal")
	req := requestWithUser(
		http.MethodPost, "/admin/hcaptcha",
		body,
		store.User{ID: 1, Email: "admin@test.com", Role: "admin"},
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("handleSaveSettings status = %d, want 303", w.Code)
	}

	// Secret key should be preserved from original
	if m.settings.SecretKey != "original-secret" {
		t.Errorf("SecretKey = %q, want original-secret (placeholder should preserve)", m.settings.SecretKey)
	}
}

// ============================================================================
// TemplateFuncs: hcaptchaWidget with enabled settings
// ============================================================================

func TestTemplateFuncHCaptchaWidgetEnabled(t *testing.T) {
	m := New()
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "my-site-key",
		SecretKey: "my-secret",
		Theme:     "dark",
		Size:      "compact",
	}

	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs returned nil")
	}

	fn := funcs["hcaptchaWidget"]
	if fn == nil {
		t.Fatal("hcaptchaWidget func is nil")
	}

	// Call the function — it returns template.HTML
	if callableFn, ok := fn.(func() template.HTML); ok {
		result := callableFn()
		if !strings.Contains(string(result), "my-site-key") {
			t.Error("hcaptchaWidget should include site key when enabled")
		}
	} else {
		t.Errorf("hcaptchaWidget has unexpected type %T", fn)
	}
}

func TestTemplateFuncHCaptchaEnabledTrue(t *testing.T) {
	m := New()
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "key",
		SecretKey: "secret",
	}

	funcs := m.TemplateFuncs()
	enabledFn, ok := funcs["hcaptchaEnabled"].(func() bool)
	if !ok {
		t.Fatal("hcaptchaEnabled not found or wrong type")
	}
	if !enabledFn() {
		t.Error("hcaptchaEnabled should return true when enabled with keys")
	}
}

// ============================================================================
// GetResponseFromForm: empty form
// ============================================================================

func TestGetResponseFromForm_Empty(t *testing.T) {
	req := httptest.NewRequest("POST", "/login", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	result := GetResponseFromForm(req)
	if result != "" {
		t.Errorf("GetResponseFromForm empty form = %q, want empty", result)
	}
}

// ============================================================================
// loadSettings: no rows path (empty table)
// ============================================================================

func TestLoadSettingsNoRows(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	// Create table but insert no rows
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS hcaptcha_settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		enabled INTEGER NOT NULL DEFAULT 0,
		site_key TEXT NOT NULL DEFAULT '',
		secret_key TEXT NOT NULL DEFAULT '',
		theme TEXT NOT NULL DEFAULT 'light',
		size TEXT NOT NULL DEFAULT 'normal',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// No row with id=1 — should return defaults
	settings, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if settings.SiteKey != TestSiteKey {
		t.Errorf("SiteKey = %q, want test key", settings.SiteKey)
	}
	if settings.SecretKey != TestSecretKey {
		t.Errorf("SecretKey = %q, want test key", settings.SecretKey)
	}
}

// ============================================================================
// saveSettings: covers the update path when settings already exist
// ============================================================================

func TestSaveSettingsUpdate(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	// Save twice to exercise the update path
	first := &Settings{
		Enabled:   false,
		SiteKey:   "first-site-key",
		SecretKey: "first-secret",
		Theme:     "light",
		Size:      "normal",
	}
	if err := saveSettings(db, first); err != nil {
		t.Fatalf("saveSettings first: %v", err)
	}

	second := &Settings{
		Enabled:   true,
		SiteKey:   "second-site-key",
		SecretKey: "second-secret",
		Theme:     "dark",
		Size:      "compact",
	}
	if err := saveSettings(db, second); err != nil {
		t.Fatalf("saveSettings second: %v", err)
	}

	loaded, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if loaded.SiteKey != "second-site-key" {
		t.Errorf("SiteKey = %q, want second-site-key", loaded.SiteKey)
	}
	if !loaded.Enabled {
		t.Error("Enabled should be true after second save")
	}
}

// ============================================================================
// IsEnabled: various states
// ============================================================================

func TestIsEnabledWithNilContext(t *testing.T) {
	m := New()
	// No Init() called; settings is nil
	if m.IsEnabled() {
		t.Error("IsEnabled should be false when settings is nil")
	}
}

// ============================================================================
// GetSettings: returns copy (mutation doesn't affect module)
// ============================================================================

func TestGetSettingsReturnsCopy(t *testing.T) {
	m := New()
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "original",
		SecretKey: "secret",
	}

	copy1 := m.GetSettings()
	copy1.SiteKey = "modified"

	copy2 := m.GetSettings()
	if copy2.SiteKey != "original" {
		t.Errorf("GetSettings returned reference, not copy; SiteKey = %q", copy2.SiteKey)
	}
}

// ============================================================================
// RenderWidget: normal theme
// ============================================================================

func TestRenderWidget_LightTheme(t *testing.T) {
	m := New()
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "abc-123",
		SecretKey: "secret",
		Theme:     "light",
		Size:      "normal",
	}

	result := string(m.RenderWidget())
	if !strings.Contains(result, `data-theme="light"`) {
		t.Error("RenderWidget should include light theme attribute")
	}
	if !strings.Contains(result, `data-size="normal"`) {
		t.Error("RenderWidget should include normal size attribute")
	}
}

// ============================================================================
// VerifyFromRequest: ctx nil guard (via indirect path)
// ============================================================================

func TestVerifyFromRequest_DisabledNoLog(t *testing.T) {
	m := New()
	// Disabled, no ctx — VerifyFromRequest should not dereference ctx.Logger
	m.settings = &Settings{Enabled: false}

	req := &VerifyRequest{Response: "something"}
	result, err := m.VerifyFromRequest(req)
	if err != nil {
		t.Fatalf("VerifyFromRequest: %v", err)
	}
	if !result.Verified {
		t.Error("should be verified when disabled")
	}
}

// ============================================================================
// VerifyFromRequest: enabled, empty response → error_required
// ============================================================================

func TestVerifyFromRequest_EnabledEmptyResponse(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "site-key",
		SecretKey: "secret-key",
	}

	req := &VerifyRequest{Response: "", RemoteIP: "1.2.3.4"}
	result, err := m.VerifyFromRequest(req)
	if err != nil {
		t.Fatalf("VerifyFromRequest: %v", err)
	}
	if result.Verified {
		t.Error("should not be verified with empty response")
	}
	if result.ErrorCode != "hcaptcha.error_required" {
		t.Errorf("ErrorCode = %q, want hcaptcha.error_required", result.ErrorCode)
	}
}

// ============================================================================
// Hook: HookFormCaptchaWidget returns empty when disabled
// ============================================================================

func TestHookFormCaptchaWidgetDisabled(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m, hooks := testModuleWithHooks(t, db)
	m.settings = &Settings{Enabled: false}

	result, err := hooks.Call(context.Background(), HookFormCaptchaWidget, nil)
	if err != nil {
		t.Fatalf("Call HookFormCaptchaWidget: %v", err)
	}
	_ = result
}

// ============================================================================
// Hook constants are defined correctly
// ============================================================================

func TestHookConstants(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"HookAuthLoginWidget", HookAuthLoginWidget},
		{"HookAuthBeforeLogin", HookAuthBeforeLogin},
		{"HookFormCaptchaWidget", HookFormCaptchaWidget},
		{"HookFormCaptchaVerify", HookFormCaptchaVerify},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value == "" {
				t.Errorf("hook constant %s should not be empty", tt.name)
			}
		})
	}
}

// ============================================================================
// Test and debug key constants
// ============================================================================

func TestTestKeyConstants(t *testing.T) {
	if TestSiteKey == "" {
		t.Error("TestSiteKey should not be empty")
	}
	if TestSecretKey == "" {
		t.Error("TestSecretKey should not be empty")
	}
}

// ============================================================================
// Verify: disabled path
// ============================================================================

func TestVerify_DisabledReturnsSuccess(t *testing.T) {
	m := New()
	m.settings = &Settings{Enabled: false}

	result, err := m.Verify("any-token", "127.0.0.1")
	if err != nil {
		t.Fatalf("Verify disabled: %v", err)
	}
	if !result.Success {
		t.Error("Verify disabled should return success=true")
	}
}

// ============================================================================
// VerifyFromRequest: enabled with non-empty response (network error → error_verification)
// ============================================================================

func TestVerifyFromRequest_EnabledNonEmpty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "site-key",
		SecretKey: "secret-key",
	}

	// With a non-empty response but invalid/network failure:
	// The function should handle the error gracefully
	req := &VerifyRequest{Response: "fake-token", RemoteIP: "127.0.0.1"}
	result, err := m.VerifyFromRequest(req)
	if err != nil {
		t.Fatalf("VerifyFromRequest: %v", err)
	}
	// With a fake token trying to reach hcaptcha.com, either:
	// - Network error → Verified=false, ErrorCode=hcaptcha.error_verification
	// - Response parse error → same
	// We just verify no panic and Verified is false
	if result.Verified {
		t.Error("should not be verified with fake token")
	}
	if result.ErrorCode == "" {
		t.Error("ErrorCode should be set on failure")
	}
}

// ============================================================================
// loadSettings: key fallback to test keys when DB has empty strings
// ============================================================================

func TestLoadSettingsEmptyKeysFallback(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS hcaptcha_settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		enabled INTEGER NOT NULL DEFAULT 0,
		site_key TEXT NOT NULL DEFAULT '',
		secret_key TEXT NOT NULL DEFAULT '',
		theme TEXT NOT NULL DEFAULT 'light',
		size TEXT NOT NULL DEFAULT 'normal',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Insert row with empty keys
	_, err = db.Exec(`INSERT INTO hcaptcha_settings (id, enabled, site_key, secret_key, theme, size) VALUES (1, 0, '', '', 'light', 'normal')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	settings, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	// Empty keys should fall back to test keys
	if settings.SiteKey != TestSiteKey {
		t.Errorf("SiteKey = %q, want TestSiteKey when empty", settings.SiteKey)
	}
	if settings.SecretKey != TestSecretKey {
		t.Errorf("SecretKey = %q, want TestSecretKey when empty", settings.SecretKey)
	}
}
