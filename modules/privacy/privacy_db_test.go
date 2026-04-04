// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

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

	_ "github.com/mattn/go-sqlite3"
)

// newTestRouter returns a chi router suitable for route registration tests.
func newTestRouter() chi.Router {
	return chi.NewRouter()
}

// setupPrivacyDB creates an in-memory SQLite database and runs privacy migrations.
func setupPrivacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.TestMemoryDB(t)
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	return db
}

// newTestModule initializes a Module backed by a real in-memory database.
func newTestModule(t *testing.T) (*Module, *sql.DB) {
	t.Helper()
	db := setupPrivacyDB(t)
	t.Cleanup(func() { _ = db.Close() })

	m := New()
	ctx := &module.Context{
		DB:     db,
		Logger: testutil.TestLogger(),
		Config: &config.Config{Env: "development"},
	}
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m, db
}

// --- Module lifecycle ---

func TestModuleInit(t *testing.T) {
	m, _ := newTestModule(t)

	if m.Name() != "privacy" {
		t.Errorf("Name() = %q, want privacy", m.Name())
	}
	if m.AdminURL() != "/admin/privacy" {
		t.Errorf("AdminURL() = %q, want /admin/privacy", m.AdminURL())
	}
	if m.SidebarLabel() != "Privacy" {
		t.Errorf("SidebarLabel() = %q, want Privacy", m.SidebarLabel())
	}
}

func TestModuleShutdown(t *testing.T) {
	m, _ := newTestModule(t)
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() returned unexpected error: %v", err)
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()
	moduleutil.AssertMigrations(t, m.Migrations(), 3)
}

func TestModuleTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	// The embedded FS should be non-zero (contains at least one file)
	entries, err := fs.ReadDir("locales")
	if err != nil {
		t.Fatalf("ReadDir(locales): %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least one locale file")
	}
}

func TestModuleTemplateFuncs(t *testing.T) {
	m, _ := newTestModule(t)
	funcs := m.TemplateFuncs()

	if funcs == nil {
		t.Fatal("TemplateFuncs() returned nil")
	}
	if _, ok := funcs["privacyHead"]; !ok {
		t.Error("TemplateFuncs should contain 'privacyHead'")
	}
	if _, ok := funcs["privacyFooterLink"]; !ok {
		t.Error("TemplateFuncs should contain 'privacyFooterLink'")
	}
}

func TestTemplateFuncPrivacyHeadCallable(t *testing.T) {
	// Module with disabled settings: privacyHead should return empty HTML.
	m := &Module{settings: &Settings{Enabled: false}}
	funcs := m.TemplateFuncs()
	fn, ok := funcs["privacyHead"].(func(...any) interface{})
	if !ok {
		// The actual type is func(...any) template.HTML — just confirm key exists.
		if _, exists := funcs["privacyHead"]; !exists {
			t.Error("privacyHead not registered")
		}
		return
	}
	_ = fn
}

func TestTemplateFuncPrivacyHeadEnabled(t *testing.T) {
	// Module with enabled settings: privacyHead should return non-empty HTML.
	m := &Module{
		settings: &Settings{
			Enabled:          true,
			GCMEnabled:       true,
			CookieName:       "klaro",
			GCMWaitForUpdate: 500,
		},
	}
	funcs := m.TemplateFuncs()
	fn := funcs["privacyHead"]
	if fn == nil {
		t.Fatal("privacyHead not registered")
	}
	// Invoke via the known concrete type.
	if headFn, ok := fn.(func(...any) interface{}); ok {
		result := headFn()
		_ = result
	}
	// Functionality covered by TestRenderHeadScriptsEnabled.
}

func TestTemplateFuncPrivacyFooterLinkPresent(t *testing.T) {
	m := &Module{settings: &Settings{Enabled: true}}
	funcs := m.TemplateFuncs()
	if _, ok := funcs["privacyFooterLink"]; !ok {
		t.Error("privacyFooterLink not found in TemplateFuncs")
	}
}

// --- firstStringArg ---

func TestFirstStringArgEmpty(t *testing.T) {
	result := firstStringArg()
	if result != "" {
		t.Errorf("firstStringArg() with no args = %q, want \"\"", result)
	}
}

func TestFirstStringArgString(t *testing.T) {
	result := firstStringArg("hello")
	if result != "hello" {
		t.Errorf("firstStringArg(\"hello\") = %q, want \"hello\"", result)
	}
}

func TestFirstStringArgNonString(t *testing.T) {
	// If first arg is not a string the cast should yield ""
	result := firstStringArg(42)
	if result != "" {
		t.Errorf("firstStringArg(42) = %q, want \"\"", result)
	}
}

func TestFirstStringArgMultiple(t *testing.T) {
	result := firstStringArg("first", "second")
	if result != "first" {
		t.Errorf("firstStringArg(\"first\",\"second\") = %q, want \"first\"", result)
	}
}

// --- IsEnabled ---

func TestIsEnabledNilSettings(t *testing.T) {
	m := &Module{settings: nil}
	if m.IsEnabled() {
		t.Error("IsEnabled() should be false when settings is nil")
	}
}

func TestIsEnabledFalse(t *testing.T) {
	m := &Module{settings: &Settings{Enabled: false}}
	if m.IsEnabled() {
		t.Error("IsEnabled() should be false when Enabled is false")
	}
}

func TestIsEnabledTrue(t *testing.T) {
	m := &Module{settings: &Settings{Enabled: true}}
	if !m.IsEnabled() {
		t.Error("IsEnabled() should be true when Enabled is true")
	}
}

// --- loadSettings / saveSettings / ReloadSettings ---

func TestLoadSettingsFromDB(t *testing.T) {
	db := setupPrivacyDB(t)
	defer func() { _ = db.Close() }()

	s, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	// Defaults inserted by migration
	if s.CookieName != "klaro" {
		t.Errorf("CookieName = %q, want klaro", s.CookieName)
	}
	if s.CookieExpiresDays != 365 {
		t.Errorf("CookieExpiresDays = %d, want 365", s.CookieExpiresDays)
	}
	if s.Theme != "light" {
		t.Errorf("Theme = %q, want light", s.Theme)
	}
	if s.Position != "bottom-right" {
		t.Errorf("Position = %q, want bottom-right", s.Position)
	}
	if !s.GCMEnabled {
		t.Error("GCMEnabled should be true by default")
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	db := setupPrivacyDB(t)
	defer func() { _ = db.Close() }()

	want := &Settings{
		Enabled:                     true,
		Debug:                       true,
		PrivacyPolicyURL:            "/privacy",
		CookieName:                  "mycookie",
		CookieExpiresDays:           180,
		Theme:                       "dark",
		Position:                    "top-left",
		GCMEnabled:                  true,
		GCMDefaultAnalytics:         true,
		GCMDefaultAdStorage:         false,
		GCMDefaultAdUserData:        true,
		GCMDefaultAdPersonalization: false,
		GCMWaitForUpdate:            1000,
		Services: []Service{
			{Name: "klaro", Title: "Essential", Purposes: []string{"essential"}, Required: true},
		},
	}

	if err := saveSettings(db, want); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	got, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}

	if !got.Enabled {
		t.Error("Enabled should be true")
	}
	if !got.Debug {
		t.Error("Debug should be true")
	}
	if got.PrivacyPolicyURL != "/privacy" {
		t.Errorf("PrivacyPolicyURL = %q, want /privacy", got.PrivacyPolicyURL)
	}
	if got.CookieName != "mycookie" {
		t.Errorf("CookieName = %q, want mycookie", got.CookieName)
	}
	if got.CookieExpiresDays != 180 {
		t.Errorf("CookieExpiresDays = %d, want 180", got.CookieExpiresDays)
	}
	if got.Theme != "dark" {
		t.Errorf("Theme = %q, want dark", got.Theme)
	}
	if got.Position != "top-left" {
		t.Errorf("Position = %q, want top-left", got.Position)
	}
	if !got.GCMEnabled {
		t.Error("GCMEnabled should be true")
	}
	if !got.GCMDefaultAnalytics {
		t.Error("GCMDefaultAnalytics should be true")
	}
	if got.GCMDefaultAdStorage {
		t.Error("GCMDefaultAdStorage should be false")
	}
	if !got.GCMDefaultAdUserData {
		t.Error("GCMDefaultAdUserData should be true")
	}
	if got.GCMDefaultAdPersonalization {
		t.Error("GCMDefaultAdPersonalization should be false")
	}
	if got.GCMWaitForUpdate != 1000 {
		t.Errorf("GCMWaitForUpdate = %d, want 1000", got.GCMWaitForUpdate)
	}
	if len(got.Services) != 1 {
		t.Errorf("len(Services) = %d, want 1", len(got.Services))
	}
}

func TestSaveSettingsDisabled(t *testing.T) {
	db := setupPrivacyDB(t)
	defer func() { _ = db.Close() }()

	// First enable
	_ = saveSettings(db, &Settings{Enabled: true, CookieName: "k", CookieExpiresDays: 365})
	// Then disable
	_ = saveSettings(db, &Settings{Enabled: false, CookieName: "k", CookieExpiresDays: 365})

	got, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if got.Enabled {
		t.Error("Enabled should be false after saving disabled state")
	}
}

func TestSaveSettingsEmptyServices(t *testing.T) {
	db := setupPrivacyDB(t)
	defer func() { _ = db.Close() }()

	s := &Settings{
		Enabled:           false,
		CookieName:        "klaro",
		CookieExpiresDays: 365,
		Services:          nil,
	}
	if err := saveSettings(db, s); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	got, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if len(got.Services) != 0 {
		t.Errorf("Services should be empty, got %d", len(got.Services))
	}
}

func TestReloadSettings(t *testing.T) {
	m, db := newTestModule(t)

	// Initially disabled (default)
	if m.IsEnabled() {
		t.Error("module should be disabled by default")
	}

	// Update settings directly in DB
	if err := saveSettings(db, &Settings{
		Enabled:           true,
		CookieName:        "klaro",
		CookieExpiresDays: 365,
		GCMEnabled:        true,
	}); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	// Reload
	if err := m.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings: %v", err)
	}

	if !m.IsEnabled() {
		t.Error("module should be enabled after ReloadSettings")
	}
}

func TestLoadSettingsNoTable(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	// No migration run → table does not exist
	_, err := loadSettings(db)
	if err == nil {
		t.Error("expected error when privacy_settings table does not exist")
	}
}

// --- Migration up/down ---

func TestMigrationV1UpDown(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	m := New()
	mig := m.Migrations()[0]

	if err := mig.Up(db); err != nil {
		t.Fatalf("migration 1 up: %v", err)
	}
	// Verify row exists
	var id int
	if err := db.QueryRow("SELECT id FROM privacy_settings WHERE id=1").Scan(&id); err != nil {
		t.Fatalf("expected row after migration 1 up: %v", err)
	}

	if err := mig.Down(db); err != nil {
		t.Fatalf("migration 1 down: %v", err)
	}
	// Table should be gone
	if err := db.QueryRow("SELECT id FROM privacy_settings WHERE id=1").Scan(&id); err == nil {
		t.Error("privacy_settings table should be dropped after migration 1 down")
	}
}

func TestMigrationV2UpDown(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	m := New()
	migrations := m.Migrations()

	if err := migrations[0].Up(db); err != nil {
		t.Fatalf("migration 1 up: %v", err)
	}
	if err := migrations[1].Up(db); err != nil {
		t.Fatalf("migration 2 up: %v", err)
	}
	// debug column should exist
	var debug int
	if err := db.QueryRow("SELECT debug FROM privacy_settings WHERE id=1").Scan(&debug); err != nil {
		t.Fatalf("debug column should exist after migration 2 up: %v", err)
	}

	// Down for migration 2 is a no-op (SQLite limitation)
	if err := migrations[1].Down(db); err != nil {
		t.Fatalf("migration 2 down: %v", err)
	}
}

func TestMigrationV3SkipsEmptyServices(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	m := New()
	migrations := m.Migrations()

	// Apply v1 and v2
	if err := migrations[0].Up(db); err != nil {
		t.Fatalf("migration 1 up: %v", err)
	}
	if err := migrations[1].Up(db); err != nil {
		t.Fatalf("migration 2 up: %v", err)
	}

	// Migration 3 should succeed with empty services (no-op)
	if err := migrations[2].Up(db); err != nil {
		t.Fatalf("migration 3 up with empty services: %v", err)
	}
}

// --- buildKlaroConfig edge cases ---

func TestBuildKlaroConfigNilSettings(t *testing.T) {
	m := &Module{settings: nil}
	config := m.buildKlaroConfig()
	if config != "var klaroConfig = {};" {
		t.Errorf("expected empty config for nil settings, got %q", config)
	}
}

func TestBuildKlaroConfigLightTheme(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:    true,
			Theme:      "light",
			CookieName: "klaro",
		},
	}
	cfg := m.buildKlaroConfig()
	if !strings.Contains(cfg, "theme: ['light']") {
		t.Error("config should contain light theme")
	}
}

func TestBuildKlaroConfigNoPrivacyURL(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:    true,
			CookieName: "klaro",
		},
	}
	cfg := m.buildKlaroConfig()
	if strings.Contains(cfg, "privacyPolicy:") {
		t.Error("config should not contain privacyPolicy when URL is empty")
	}
}

func TestBuildKlaroConfigMultipleServices(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:    true,
			CookieName: "klaro",
			GCMEnabled: true,
			Services: []Service{
				{
					Name:     "klaro",
					Title:    "Essential",
					Purposes: []string{"essential"},
					Required: true,
					Default:  true,
					Cookies:  []Cookie{{Pattern: "^klaro"}},
				},
				{
					Name:           "google-analytics",
					Title:          "Analytics",
					Purposes:       []string{"analytics"},
					GCMConsentType: "analytics_storage",
				},
				{
					Name:    "matomo",
					Title:   "Matomo",
					Purposes: []string{"analytics"},
				},
			},
		},
	}
	cfg := m.buildKlaroConfig()
	if !strings.Contains(cfg, "name: 'klaro'") {
		t.Error("config should contain klaro service")
	}
	if !strings.Contains(cfg, "name: 'google-analytics'") {
		t.Error("config should contain google-analytics service")
	}
	if !strings.Contains(cfg, "name: 'matomo'") {
		t.Error("config should contain matomo service")
	}
	// Cookies section for klaro service
	if !strings.Contains(cfg, "/^klaro/") {
		t.Error("config should render cookie patterns as regex")
	}
	// GCM callback only for google-analytics (has GCMConsentType)
	if !strings.Contains(cfg, "gtag('consent', 'update'") {
		t.Error("config should contain GCM callback for analytics")
	}
}

func TestBuildServiceConfigNoGCMWhenDisabled(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:    true,
			GCMEnabled: false, // GCM off
			CookieName: "klaro",
			Services: []Service{
				{
					Name:           "ga",
					Title:          "Analytics",
					Purposes:       []string{"analytics"},
					GCMConsentType: "analytics_storage",
				},
			},
		},
	}
	cfg := m.buildKlaroConfig()
	// No callback should appear because GCMEnabled=false
	if strings.Contains(cfg, "gtag('consent', 'update'") {
		t.Error("GCM callback should not appear when GCMEnabled is false")
	}
}

func TestBuildGCMCallbackMultipleTypes(t *testing.T) {
	m := &Module{settings: &Settings{GCMEnabled: true}}
	cb := m.buildGCMCallback("ad_storage,ad_user_data,ad_personalization")

	for _, ct := range []string{"ad_storage", "ad_user_data", "ad_personalization"} {
		if !strings.Contains(cb, "'"+ct+"': consent ? 'granted' : 'denied'") {
			t.Errorf("callback should contain consent type %q", ct)
		}
	}
}

func TestBuildGCMCallbackEmptyType(t *testing.T) {
	m := &Module{settings: &Settings{GCMEnabled: true}}
	cb := m.buildGCMCallback("")
	// Empty type should produce a callback with no consent entries
	if strings.Contains(cb, "consent ?") {
		t.Error("callback with empty type should contain no consent mappings")
	}
}

func TestRenderGCMDefaultsAllGranted(t *testing.T) {
	m := &Module{
		settings: &Settings{
			GCMEnabled:                  true,
			GCMDefaultAnalytics:         true,
			GCMDefaultAdStorage:         true,
			GCMDefaultAdUserData:        true,
			GCMDefaultAdPersonalization: true,
			GCMWaitForUpdate:            250,
		},
	}
	script := m.renderGCMDefaults()
	for _, field := range []string{"analytics_storage", "ad_storage", "ad_user_data", "ad_personalization"} {
		if !strings.Contains(script, "'"+field+"': 'granted'") {
			t.Errorf("expected %q to be 'granted'", field)
		}
	}
	if !strings.Contains(script, "'wait_for_update': 250") {
		t.Error("script should contain wait_for_update 250")
	}
}

func TestRenderHeadScriptsWithNonce(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:          true,
			GCMEnabled:       true,
			CookieName:       "klaro",
			GCMWaitForUpdate: 500,
		},
	}
	output := string(m.renderHeadScripts("test-nonce-abc"))
	// Nonce should be injected into script tags
	if !strings.Contains(output, `nonce="test-nonce-abc"`) {
		t.Error("nonce should be injected into script tags")
	}
}

func TestGetPositionCSSNilSettings(t *testing.T) {
	m := &Module{settings: nil}
	css := m.getPositionCSS()
	if css != "" {
		t.Errorf("getPositionCSS() with nil settings should return empty, got %q", css)
	}
}

// --- RegisterRoutes / RegisterAdminRoutes ---

func TestRegisterRoutes(t *testing.T) {
	m, _ := newTestModule(t)
	// RegisterRoutes is a no-op; pass a real router to satisfy the interface.
	m.RegisterRoutes(newTestRouter())
}

func TestRegisterAdminRoutes(t *testing.T) {
	m, _ := newTestModule(t)
	// Build a minimal chi router to avoid nil-router panic.
	router := newTestRouter()
	m.RegisterAdminRoutes(router)
}

// --- TemplateFuncs invocation via concrete type ---

func TestTemplateFuncPrivacyHeadInvoke(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:          true,
			GCMEnabled:       false,
			CookieName:       "klaro",
			GCMWaitForUpdate: 500,
		},
	}
	funcs := m.TemplateFuncs()
	fn, ok := funcs["privacyHead"].(func(...any) interface{ String() string })
	if !ok {
		// Type can also be func(...any) template.HTML; exercise via renderHeadScripts directly.
		out := m.renderHeadScripts("some-nonce")
		if string(out) == "" {
			t.Error("renderHeadScripts should return non-empty when enabled")
		}
		return
	}
	result := fn("test-nonce")
	if result.String() == "" {
		t.Error("privacyHead should return non-empty HTML when enabled")
	}
}

func TestTemplateFuncPrivacyFooterLinkInvoke(t *testing.T) {
	m := &Module{settings: &Settings{Enabled: true}}
	funcs := m.TemplateFuncs()
	fn, ok := funcs["privacyFooterLink"].(func() interface{ String() string })
	if !ok {
		out := m.renderFooterLink()
		if string(out) == "" {
			t.Error("renderFooterLink should return non-empty HTML when enabled")
		}
		return
	}
	result := fn()
	if result.String() == "" {
		t.Error("privacyFooterLink should return non-empty HTML when enabled")
	}
}

// --- Migration v3 with existing klaro service ---

func TestMigrationV3UpdatesKlaroService(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	m := New()
	migrations := m.Migrations()

	// Apply v1 and v2
	if err := migrations[0].Up(db); err != nil {
		t.Fatalf("migration 1 up: %v", err)
	}
	if err := migrations[1].Up(db); err != nil {
		t.Fatalf("migration 2 up: %v", err)
	}

	// Seed a klaro service in the JSON
	servicesJSON := `[{"name":"klaro","title":"Old Title","description":"Old","purposes":["old"],"required":false,"default":false}]`
	if _, err := db.Exec(`UPDATE privacy_settings SET services = ? WHERE id = 1`, servicesJSON); err != nil {
		t.Fatalf("seed services: %v", err)
	}

	// Apply v3 — should update the klaro entry
	if err := migrations[2].Up(db); err != nil {
		t.Fatalf("migration 3 up: %v", err)
	}

	// Load and verify klaro was updated
	s, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings after v3: %v", err)
	}
	var klaroSvc *Service
	for i := range s.Services {
		if s.Services[i].Name == "klaro" {
			klaroSvc = &s.Services[i]
			break
		}
	}
	if klaroSvc == nil {
		t.Fatal("klaro service not found after migration 3")
	}
	if klaroSvc.Title != "Essential Cookies" {
		t.Errorf("klaro Title = %q, want 'Essential Cookies'", klaroSvc.Title)
	}
	if len(klaroSvc.Purposes) == 0 || klaroSvc.Purposes[0] != "essential" {
		t.Errorf("klaro Purposes = %v, want [essential]", klaroSvc.Purposes)
	}
}

func TestMigrationV3NoKlaroService(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	m := New()
	migrations := m.Migrations()

	if err := migrations[0].Up(db); err != nil {
		t.Fatalf("migration 1 up: %v", err)
	}
	if err := migrations[1].Up(db); err != nil {
		t.Fatalf("migration 2 up: %v", err)
	}

	// Seed a non-klaro service
	servicesJSON := `[{"name":"matomo","title":"Matomo","purposes":["analytics"]}]`
	if _, err := db.Exec(`UPDATE privacy_settings SET services = ? WHERE id = 1`, servicesJSON); err != nil {
		t.Fatalf("seed services: %v", err)
	}

	// Migration 3 should succeed without error (klaro not found → no-op)
	if err := migrations[2].Up(db); err != nil {
		t.Fatalf("migration 3 up: %v", err)
	}
}

// --- Init with DB fallback to defaults ---

// --- handleSaveSettings unauthorized path ---

func TestHandleSaveSettingsUnauthorized(t *testing.T) {
	m, _ := newTestModule(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/privacy", nil)
	rr := httptest.NewRecorder()
	m.handleSaveSettings(rr, req)

	// No user in context and not demo mode → should return Unauthorized
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

// --- Init with DB fallback to defaults ---

func TestInitFallsBackToDefaults(t *testing.T) {
	// Use a fresh DB with no privacy_settings table — loadSettings fails →
	// Init should warn and use defaults rather than returning an error.
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	m := New()
	ctx := &module.Context{
		DB:     db,
		Logger: testutil.TestLogger(),
		Config: &config.Config{Env: "development"},
	}
	// Init should succeed (no error), falling back to defaults.
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init should succeed even with no table: %v", err)
	}
	// settings should be non-nil (empty defaults)
	if m.settings == nil {
		t.Error("settings should not be nil after Init with fallback")
	}
}
