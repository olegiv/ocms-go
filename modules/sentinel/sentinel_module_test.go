// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sentinel

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// testModule creates a fully initialized Sentinel module for testing.
func testModule(t *testing.T, db *sql.DB) *Module {
	t.Helper()
	m := New()
	ctx, _ := moduleutil.TestModuleContext(t, db)
	moduleutil.RunMigrations(t, db, m.Migrations())
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m
}

// ============================================================================
// Module identity and lifecycle
// ============================================================================

func TestModuleNew(t *testing.T) {
	m := New()
	if m.Name() != "sentinel" {
		t.Errorf("Name() = %q, want sentinel", m.Name())
	}
	if m.Version() == "" {
		t.Error("Version() should not be empty")
	}
	if m.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestModuleAdminURL(t *testing.T) {
	m := New()
	if m.AdminURL() != "/admin/sentinel" {
		t.Errorf("AdminURL() = %q, want /admin/sentinel", m.AdminURL())
	}
}

func TestModuleSidebarLabel(t *testing.T) {
	m := New()
	if m.SidebarLabel() != "Sentinel" {
		t.Errorf("SidebarLabel() = %q, want Sentinel", m.SidebarLabel())
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()
	moduleutil.AssertMigrations(t, m.Migrations(), 7)
}

func TestModuleTemplateFuncs(t *testing.T) {
	m := New()
	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs() returned nil")
	}

	expectedFuncs := []string{
		"sentinelVersion",
		"countryName",
		"sentinelIsActive",
		"sentinelIsIPBanned",
		"sentinelIsIPWhitelisted",
	}
	for _, name := range expectedFuncs {
		if _, ok := funcs[name]; !ok {
			t.Errorf("TemplateFuncs() missing %q", name)
		}
	}
}

func TestModuleInit(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	// Verify the module initialized with default settings
	if !m.IsBanCheckEnabled() {
		t.Error("IsBanCheckEnabled() should be true after init with default settings")
	}
	if !m.IsAutoBanEnabled() {
		t.Error("IsAutoBanEnabled() should be true after init with default settings")
	}
}

func TestModuleShutdown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() error = %v, want nil", err)
	}
}

func TestMigrationDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	// Run migrations down in reverse order so data-cleaning migrations (like
	// migration 6 which deletes seeded rows) run before schema-dropping
	// migrations (like migration 2 which drops the table).
	migrations := m.Migrations()
	for i := len(migrations) - 1; i >= 0; i-- {
		if err := migrations[i].Down(db); err != nil {
			t.Fatalf("migration %d down: %v", migrations[i].Version, err)
		}
	}

	moduleutil.AssertTableNotExists(t, db, "sentinel_banned_ips")
	moduleutil.AssertTableNotExists(t, db, "sentinel_autoban_paths")
	moduleutil.AssertTableNotExists(t, db, "sentinel_whitelist")
	moduleutil.AssertTableNotExists(t, db, "sentinel_settings")
}

// ============================================================================
// Settings accessors: IsBanCheckEnabled / IsAutoBanEnabled
// ============================================================================

func TestSettingsDefaults(t *testing.T) {
	// New() sets defaults before Init
	m := New()
	if !m.IsBanCheckEnabled() {
		t.Error("IsBanCheckEnabled() default should be true")
	}
	if !m.IsAutoBanEnabled() {
		t.Error("IsAutoBanEnabled() default should be true")
	}
	if !m.IsHoneypotAutoBanEnabled() {
		t.Error("IsHoneypotAutoBanEnabled() default should be true")
	}
}

func TestReloadSettingsFromDB(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Disable all settings in the database directly
	if err := m.updateSetting(settingBanCheckEnabled, false); err != nil {
		t.Fatalf("updateSetting ban_check_enabled: %v", err)
	}
	if err := m.updateSetting(settingAutoBanEnabled, false); err != nil {
		t.Fatalf("updateSetting autoban_enabled: %v", err)
	}
	if err := m.updateSetting(settingHoneypotAutoBanEnabled, false); err != nil {
		t.Fatalf("updateSetting honeypot_autoban_enabled: %v", err)
	}

	// Reload from DB
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}

	if m.IsBanCheckEnabled() {
		t.Error("IsBanCheckEnabled() should be false after update to false")
	}
	if m.IsAutoBanEnabled() {
		t.Error("IsAutoBanEnabled() should be false after update to false")
	}
	if m.IsHoneypotAutoBanEnabled() {
		t.Error("IsHoneypotAutoBanEnabled() should be false after update to false")
	}

	// Re-enable
	if err := m.updateSetting(settingBanCheckEnabled, true); err != nil {
		t.Fatalf("updateSetting ban_check_enabled true: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}
	if !m.IsBanCheckEnabled() {
		t.Error("IsBanCheckEnabled() should be true after re-enable")
	}
}

// ============================================================================
// IsIPBanned / IsIPWhitelisted with database-loaded patterns
// ============================================================================

func TestIsIPBannedWithDB(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Initially nothing should be banned
	if m.IsIPBanned("1.2.3.4") {
		t.Error("IsIPBanned should be false before any bans added")
	}

	// Add a ban
	if err := m.createBan("1.2.3.4", "test notes", "http://example.com", 0); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	// Now the specific IP should be banned
	if !m.IsIPBanned("1.2.3.4") {
		t.Error("IsIPBanned should be true after banning 1.2.3.4")
	}

	// A different IP should not be banned
	if m.IsIPBanned("5.6.7.8") {
		t.Error("IsIPBanned should be false for 5.6.7.8")
	}
}

func TestIsIPBannedWildcardPattern(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Add a wildcard ban
	if err := m.createBan("10.0.0.*", "block subnet", "", 0); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	if !m.IsIPBanned("10.0.0.1") {
		t.Error("IsIPBanned should be true for 10.0.0.1 matching 10.0.0.*")
	}
	if !m.IsIPBanned("10.0.0.255") {
		t.Error("IsIPBanned should be true for 10.0.0.255 matching 10.0.0.*")
	}
	if m.IsIPBanned("10.0.1.1") {
		t.Error("IsIPBanned should be false for 10.0.1.1 not matching 10.0.0.*")
	}
}

func TestIsIPWhitelistedWithDB(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Initially nothing whitelisted
	if m.IsIPWhitelisted("127.0.0.1") {
		t.Error("IsIPWhitelisted should be false before any entries added")
	}

	// Add whitelist entry
	if err := m.createWhitelistEntry("127.0.0.1", "localhost", 0); err != nil {
		t.Fatalf("createWhitelistEntry: %v", err)
	}

	if !m.IsIPWhitelisted("127.0.0.1") {
		t.Error("IsIPWhitelisted should be true for 127.0.0.1 after whitelisting")
	}
	if m.IsIPWhitelisted("192.168.1.1") {
		t.Error("IsIPWhitelisted should be false for 192.168.1.1")
	}
}

func TestIsIPWhitelistedEmpty(t *testing.T) {
	m := New()
	// No init — empty whitelist
	if m.IsIPWhitelisted("1.2.3.4") {
		t.Error("IsIPWhitelisted should return false when whitelist is empty")
	}
}

func TestIsIPBannedEmpty(t *testing.T) {
	m := New()
	// No init — empty ban list
	if m.IsIPBanned("1.2.3.4") {
		t.Error("IsIPBanned should return false when ban list is empty")
	}
}

// ============================================================================
// CheckAutoBanPath
// ============================================================================

func TestCheckAutoBanPathWithDB(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// The migration seeds default paths like /wp-admin*
	matched := m.CheckAutoBanPath("/wp-admin")
	if matched == "" {
		t.Error("CheckAutoBanPath should match /wp-admin (seeded by migration)")
	}

	// A normal path should not match
	matched = m.CheckAutoBanPath("/api/v2/pages")
	if matched != "" {
		t.Errorf("CheckAutoBanPath should return empty for /api/v2/pages, got %q", matched)
	}
}

func TestCheckAutoBanPathCustom(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Add a custom path pattern
	if err := m.createAutoBanPath("/test-path*", "custom test pattern", 0); err != nil {
		t.Fatalf("createAutoBanPath: %v", err)
	}

	matched := m.CheckAutoBanPath("/test-path/anything")
	if matched == "" {
		t.Error("CheckAutoBanPath should match /test-path/anything")
	}
	if matched != "/test-path*" {
		t.Errorf("CheckAutoBanPath matched pattern = %q, want /test-path*", matched)
	}

	// A non-matching path
	matched = m.CheckAutoBanPath("/safe-path")
	// May match another seeded pattern — only test the custom one doesn't falsely match safe paths
	// that aren't in any pattern
	_ = matched // result depends on seeded patterns; basic smoke test
}

func TestCheckAutoBanPathEmpty(t *testing.T) {
	m := New()
	// No init — empty paths list
	matched := m.CheckAutoBanPath("/wp-admin")
	if matched != "" {
		t.Errorf("CheckAutoBanPath should return empty when paths list is empty, got %q", matched)
	}
}

// ============================================================================
// Dependency injection: SetSessionManager / SetActiveChecker / SetEventLogger
// ============================================================================

func TestSetSessionManager(t *testing.T) {
	m := New()
	// sessionManager starts nil
	if m.sessionManager != nil {
		t.Error("sessionManager should be nil initially")
	}
	// Setting nil should not panic
	m.SetSessionManager(nil)
	if m.sessionManager != nil {
		t.Error("sessionManager should still be nil after setting nil")
	}
}

// fakeActiveChecker is a stub that always returns a fixed result.
type fakeActiveChecker struct {
	active bool
}

func (f *fakeActiveChecker) IsActive(_ string) bool { return f.active }

func TestSetActiveChecker(t *testing.T) {
	m := New()
	if m.activeChecker != nil {
		t.Error("activeChecker should be nil initially")
	}

	checker := &fakeActiveChecker{active: false}
	m.SetActiveChecker(checker)

	if m.activeChecker == nil {
		t.Error("activeChecker should be set after SetActiveChecker")
	}
}

func TestSetEventLogger(t *testing.T) {
	m := New()
	if m.eventLogger != nil {
		t.Error("eventLogger should be nil initially")
	}
	// Setting nil is valid
	m.SetEventLogger(nil)
	if m.eventLogger != nil {
		t.Error("eventLogger should still be nil")
	}
}

// ============================================================================
// Middleware smoke test via httptest
// ============================================================================

func TestGetMiddlewareReturnsFunc(t *testing.T) {
	m := New()
	mw := m.GetMiddleware()
	if mw == nil {
		t.Fatal("GetMiddleware() returned nil")
	}
}

func TestMiddlewarePassesWhenNoContext(t *testing.T) {
	// Module without Init — ctx is nil, middleware should pass through.
	m := New()
	mw := m.GetMiddleware()

	var handlerCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("next handler should be called when ctx is nil")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}
}

func TestMiddlewareBlocksBannedIP(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Ban an IP
	if err := m.createBan("9.9.9.9", "test ban", "", 0); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	mw := m.GetMiddleware()
	var handlerCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if handlerCalled {
		t.Error("next handler should not be called for banned IP")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status code = %d, want 403", w.Code)
	}
}

func TestMiddlewareAllowsWhitelistedIP(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Whitelist the IP and also ban it — whitelist takes priority
	if err := m.createWhitelistEntry("8.8.8.8", "trusted", 0); err != nil {
		t.Fatalf("createWhitelistEntry: %v", err)
	}
	if err := m.createBan("8.8.8.8", "also banned", "", 0); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	mw := m.GetMiddleware()
	var handlerCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("next handler should be called for whitelisted IP even if also banned")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}
}

func TestMiddlewareSkipsWhenDeactivated(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Ban an IP but deactivate the module
	if err := m.createBan("7.7.7.7", "test", "", 0); err != nil {
		t.Fatalf("createBan: %v", err)
	}
	m.SetActiveChecker(&fakeActiveChecker{active: false})

	mw := m.GetMiddleware()
	var handlerCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "7.7.7.7:1234"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("next handler should be called when module is deactivated")
	}
}

func TestMiddlewareAutoBansIP(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Add a custom auto-ban path
	if err := m.createAutoBanPath("/exploit*", "exploit attempt", 0); err != nil {
		t.Fatalf("createAutoBanPath: %v", err)
	}

	mw := m.GetMiddleware()
	var handlerCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/exploit/attack", nil)
	req.RemoteAddr = "6.6.6.6:1234"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should be blocked and auto-banned
	if handlerCalled {
		t.Error("next handler should not be called for auto-ban path match")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status code = %d, want 403", w.Code)
	}

	// The IP should now be in the banned list
	if !m.IsIPBanned("6.6.6.6") {
		t.Error("IP 6.6.6.6 should be auto-banned after accessing exploit path")
	}
}

// ============================================================================
// TemplateFuncs integration
// ============================================================================

func TestTemplateFuncSentinelIsIPBanned(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	if err := m.createBan("3.3.3.3", "func test", "", 0); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	funcs := m.TemplateFuncs()

	bannedFn, ok := funcs["sentinelIsIPBanned"].(func(string) bool)
	if !ok {
		t.Fatal("sentinelIsIPBanned not found or wrong type")
	}

	if !bannedFn("3.3.3.3") {
		t.Error("sentinelIsIPBanned should return true for banned IP")
	}
	if bannedFn("4.4.4.4") {
		t.Error("sentinelIsIPBanned should return false for non-banned IP")
	}
	if bannedFn("") {
		t.Error("sentinelIsIPBanned should return false for empty IP")
	}
}

func TestTemplateFuncSentinelIsIPWhitelisted(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	if err := m.createWhitelistEntry("5.5.5.5", "func test", 0); err != nil {
		t.Fatalf("createWhitelistEntry: %v", err)
	}

	funcs := m.TemplateFuncs()

	whitelistFn, ok := funcs["sentinelIsIPWhitelisted"].(func(string) bool)
	if !ok {
		t.Fatal("sentinelIsIPWhitelisted not found or wrong type")
	}

	if !whitelistFn("5.5.5.5") {
		t.Error("sentinelIsIPWhitelisted should return true for whitelisted IP")
	}
	if whitelistFn("1.1.1.1") {
		t.Error("sentinelIsIPWhitelisted should return false for non-whitelisted IP")
	}
	if whitelistFn("") {
		t.Error("sentinelIsIPWhitelisted should return false for empty IP")
	}
}

func TestTemplateFuncSentinelIsActive(t *testing.T) {
	m := New()
	funcs := m.TemplateFuncs()

	isActiveFn, ok := funcs["sentinelIsActive"].(func() bool)
	if !ok {
		t.Fatal("sentinelIsActive not found or wrong type")
	}
	if !isActiveFn() {
		t.Error("sentinelIsActive should return true")
	}
}

func TestTemplateFuncSentinelVersion(t *testing.T) {
	m := New()
	funcs := m.TemplateFuncs()

	versionFn, ok := funcs["sentinelVersion"].(func() string)
	if !ok {
		t.Fatal("sentinelVersion not found or wrong type")
	}
	if versionFn() == "" {
		t.Error("sentinelVersion should return a non-empty version string")
	}
}

// ============================================================================
// LookupCountry smoke test (no GeoIP DB — should return "")
// ============================================================================

func TestLookupCountryNoGeoIP(t *testing.T) {
	m := New()
	// No GeoIP database configured; should return empty without panic
	result := m.LookupCountry("8.8.8.8")
	_ = result // may be "" — just check no panic
}

// ============================================================================
// isAdminOrEditor: nil session manager should return false safely
// ============================================================================

func TestIsAdminOrEditorNilSessionManager(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	// sessionManager is nil by default
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if m.isAdminOrEditor(req) {
		t.Error("isAdminOrEditor should return false when sessionManager is nil")
	}
}

// ============================================================================
// Database operations: countBannedIPs, listBannedIPs, getBanByID, deleteBan
// ============================================================================

func TestCountBannedIPs(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Initially no bans
	count, err := m.countBannedIPs()
	if err != nil {
		t.Fatalf("countBannedIPs: %v", err)
	}
	if count != 0 {
		t.Errorf("countBannedIPs = %d, want 0 initially", count)
	}

	// Add bans
	if err := m.createBan("1.1.1.1", "", "", 0); err != nil {
		t.Fatalf("createBan 1: %v", err)
	}
	if err := m.createBan("2.2.2.2", "", "", 0); err != nil {
		t.Fatalf("createBan 2: %v", err)
	}

	count, err = m.countBannedIPs()
	if err != nil {
		t.Fatalf("countBannedIPs after inserts: %v", err)
	}
	if count != 2 {
		t.Errorf("countBannedIPs = %d, want 2", count)
	}
}

func TestListBannedIPs(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Empty list
	bans, err := m.listBannedIPs(10, 0)
	if err != nil {
		t.Fatalf("listBannedIPs: %v", err)
	}
	if len(bans) != 0 {
		t.Errorf("listBannedIPs initial len = %d, want 0", len(bans))
	}

	// Add and list
	if err := m.createBan("3.3.3.3", "notes", "http://x.com", 1); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	bans, err = m.listBannedIPs(10, 0)
	if err != nil {
		t.Fatalf("listBannedIPs after insert: %v", err)
	}
	if len(bans) != 1 {
		t.Fatalf("listBannedIPs len = %d, want 1", len(bans))
	}
	if bans[0].IPPattern != "3.3.3.3" {
		t.Errorf("bans[0].IPPattern = %q, want 3.3.3.3", bans[0].IPPattern)
	}
	if bans[0].Notes != "notes" {
		t.Errorf("bans[0].Notes = %q, want notes", bans[0].Notes)
	}
}

func TestGetBanByID(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if err := m.createBan("11.22.33.44", "test ban", "http://test.com", 2); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	bans, err := m.listBannedIPs(1, 0)
	if err != nil || len(bans) == 0 {
		t.Fatalf("listBannedIPs: %v, len=%d", err, len(bans))
	}

	ban, err := m.getBanByID(bans[0].ID)
	if err != nil {
		t.Fatalf("getBanByID: %v", err)
	}
	if ban.IPPattern != "11.22.33.44" {
		t.Errorf("ban.IPPattern = %q, want 11.22.33.44", ban.IPPattern)
	}
}

func TestDeleteBan(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if err := m.createBan("55.66.77.88", "", "", 0); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	bans, err := m.listBannedIPs(1, 0)
	if err != nil || len(bans) == 0 {
		t.Fatalf("listBannedIPs: %v, len=%d", err, len(bans))
	}
	banID := bans[0].ID

	if err := m.deleteBan(banID); err != nil {
		t.Fatalf("deleteBan: %v", err)
	}

	if m.IsIPBanned("55.66.77.88") {
		t.Error("IP should not be banned after deletion")
	}
}

// ============================================================================
// Database operations: listAutoBanPaths, getPathByID, deleteAutoBanPath
// ============================================================================

func TestListAutoBanPaths(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Migration 6 seeds default paths
	paths, err := m.listAutoBanPaths()
	if err != nil {
		t.Fatalf("listAutoBanPaths: %v", err)
	}
	if len(paths) == 0 {
		t.Error("listAutoBanPaths should return seeded default paths")
	}
}

func TestGetPathByID(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if err := m.createAutoBanPath("/test-get*", "get by id test", 0); err != nil {
		t.Fatalf("createAutoBanPath: %v", err)
	}

	paths, err := m.listAutoBanPaths()
	if err != nil || len(paths) == 0 {
		t.Fatalf("listAutoBanPaths: %v, len=%d", err, len(paths))
	}

	// Find our newly added path
	var pathID int64
	for _, p := range paths {
		if p.PathPattern == "/test-get*" {
			pathID = p.ID
			break
		}
	}
	if pathID == 0 {
		t.Fatal("could not find created path")
	}

	path, err := m.getPathByID(pathID)
	if err != nil {
		t.Fatalf("getPathByID: %v", err)
	}
	if path.PathPattern != "/test-get*" {
		t.Errorf("path.PathPattern = %q, want /test-get*", path.PathPattern)
	}
}

func TestDeleteAutoBanPath(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if err := m.createAutoBanPath("/delete-test*", "", 0); err != nil {
		t.Fatalf("createAutoBanPath: %v", err)
	}

	paths, err := m.listAutoBanPaths()
	if err != nil {
		t.Fatalf("listAutoBanPaths: %v", err)
	}

	var pathID int64
	for _, p := range paths {
		if p.PathPattern == "/delete-test*" {
			pathID = p.ID
			break
		}
	}
	if pathID == 0 {
		t.Fatal("could not find created path")
	}

	if err := m.deleteAutoBanPath(pathID); err != nil {
		t.Fatalf("deleteAutoBanPath: %v", err)
	}

	// Verify it no longer matches
	matched := m.CheckAutoBanPath("/delete-test/anything")
	if matched == "/delete-test*" {
		t.Error("deleted path should no longer match")
	}
}

// ============================================================================
// Database operations: listWhitelist, getWhitelistByID, deleteWhitelistEntry
// ============================================================================

func TestListWhitelist(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Initially empty
	entries, err := m.listWhitelist()
	if err != nil {
		t.Fatalf("listWhitelist: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("listWhitelist initial len = %d, want 0", len(entries))
	}

	if err := m.createWhitelistEntry("192.168.1.1", "local", 0); err != nil {
		t.Fatalf("createWhitelistEntry: %v", err)
	}

	entries, err = m.listWhitelist()
	if err != nil {
		t.Fatalf("listWhitelist after insert: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("listWhitelist len = %d, want 1", len(entries))
	}
	if entries[0].IPPattern != "192.168.1.1" {
		t.Errorf("entries[0].IPPattern = %q, want 192.168.1.1", entries[0].IPPattern)
	}
}

func TestGetWhitelistByID(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if err := m.createWhitelistEntry("10.20.30.40", "get by id", 0); err != nil {
		t.Fatalf("createWhitelistEntry: %v", err)
	}

	entries, err := m.listWhitelist()
	if err != nil || len(entries) == 0 {
		t.Fatalf("listWhitelist: %v, len=%d", err, len(entries))
	}

	entry, err := m.getWhitelistByID(entries[0].ID)
	if err != nil {
		t.Fatalf("getWhitelistByID: %v", err)
	}
	if entry.IPPattern != "10.20.30.40" {
		t.Errorf("entry.IPPattern = %q, want 10.20.30.40", entry.IPPattern)
	}
}

func TestDeleteWhitelistEntry(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if err := m.createWhitelistEntry("172.16.0.1", "delete test", 0); err != nil {
		t.Fatalf("createWhitelistEntry: %v", err)
	}

	entries, err := m.listWhitelist()
	if err != nil || len(entries) == 0 {
		t.Fatalf("listWhitelist: %v, len=%d", err, len(entries))
	}

	if err := m.deleteWhitelistEntry(entries[0].ID); err != nil {
		t.Fatalf("deleteWhitelistEntry: %v", err)
	}

	if m.IsIPWhitelisted("172.16.0.1") {
		t.Error("IP should not be whitelisted after deletion")
	}
}

// ============================================================================
// Settings: updateSetting + reloadSettings
// ============================================================================

func TestUpdateSettingAndReload(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Both should be true after init
	if !m.IsBanCheckEnabled() || !m.IsAutoBanEnabled() {
		t.Fatal("both settings should be enabled after init")
	}

	// Disable ban check
	if err := m.updateSetting(settingBanCheckEnabled, false); err != nil {
		t.Fatalf("updateSetting: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}
	if m.IsBanCheckEnabled() {
		t.Error("IsBanCheckEnabled should be false after disabling")
	}

	// Re-enable
	if err := m.updateSetting(settingBanCheckEnabled, true); err != nil {
		t.Fatalf("updateSetting: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}
	if !m.IsBanCheckEnabled() {
		t.Error("IsBanCheckEnabled should be true after re-enabling")
	}
}

// ============================================================================
// RegisterRoutes smoke test
// ============================================================================

func TestRegisterRoutes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	// RegisterRoutes should not panic — sentinel has no public routes
	m.RegisterRoutes(nil)
}

// ============================================================================
// writeJSONError
// ============================================================================

func TestWriteJSONError(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONError(w, "something went wrong", http.StatusBadRequest)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want 400", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("body should not be empty")
	}
}

// ============================================================================
// Additional middleware edge cases
// ============================================================================

func TestMiddlewareBanCheckDisabled(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Ban an IP then disable ban checking
	if err := m.createBan("2.2.2.2", "ban check test", "", 0); err != nil {
		t.Fatalf("createBan: %v", err)
	}

	// Disable ban check via settings
	if err := m.updateSetting(settingBanCheckEnabled, false); err != nil {
		t.Fatalf("updateSetting: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}

	mw := m.GetMiddleware()
	var handlerCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "2.2.2.2:1234"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("next handler should be called when ban check is disabled")
	}
}

// ============================================================================
// requireUser: unauthenticated returns 401
// ============================================================================

func TestRequireUserUnauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/sentinel", nil)
	// No user in context — GetUser returns nil
	user := requireUser(w, req)
	if user != nil {
		t.Error("requireUser should return nil when no user in context")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d, want 401", w.Code)
	}
}

// ============================================================================
// Handler smoke tests via direct call (unauthenticated path)
// ============================================================================

func TestHandleAdminListUnauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/sentinel", nil)

	m.handleAdminList(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("handleAdminList status = %d, want 401", w.Code)
	}
}

func TestHandleCreateUnauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/sentinel", nil)

	m.handleCreate(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("handleCreate status = %d, want 401", w.Code)
	}
}

func TestHandleBanAjaxUnauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/sentinel/ban", nil)

	m.handleBanAjax(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("handleBanAjax status = %d, want 401", w.Code)
	}
}

func TestHandleCreatePathUnauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/sentinel/paths", nil)

	m.handleCreatePath(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("handleCreatePath status = %d, want 401", w.Code)
	}
}

func TestHandleCreateWhitelistUnauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/sentinel/whitelist", nil)

	m.handleCreateWhitelist(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("handleCreateWhitelist status = %d, want 401", w.Code)
	}
}

// ============================================================================
// Dependencies: module has no dependencies
// ============================================================================

func TestDependencies(t *testing.T) {
	m := New()
	deps := m.Dependencies()
	if len(deps) != 0 {
		t.Errorf("Dependencies() = %v, want nil or empty", deps)
	}
}

// ============================================================================
// Additional reloadSettings edge cases
// ============================================================================

func TestReloadSettingsNoTable(t *testing.T) {
	// Use an in-memory DB without the sentinel_settings table
	db := testutil.TestMemoryDB(t)
	defer func() { _ = db.Close() }()

	m := New()
	ctx, _ := moduleutil.TestModuleContext(t, db)
	m.ctx = ctx

	// reloadSettings should use defaults when table doesn't exist
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings without table: %v", err)
	}

	// Should fall back to defaults (true)
	if !m.IsBanCheckEnabled() {
		t.Error("IsBanCheckEnabled should default to true when table missing")
	}
	if !m.IsAutoBanEnabled() {
		t.Error("IsAutoBanEnabled should default to true when table missing")
	}
}

// ============================================================================
// handleUpdateSettings: requires authenticated user
// ============================================================================

func TestHandleUpdateSettingsUnauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/sentinel/settings", nil)

	m.handleUpdateSettings(w, req)

	// Either 401 (no user) or 303 redirect (demo mode check comes first)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusSeeOther {
		t.Errorf("handleUpdateSettings status = %d, want 401 or 303", w.Code)
	}
}

// ============================================================================
// handleDeleteEntry: invalid ID path
// ============================================================================

func TestHandleDeleteEntryInvalidID(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// handleDelete uses chi.URLParam — call handleDeleteEntry directly with invalid id
	// by building a request where the chi URL param is not available (empty string → parse error)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/sentinel/abc", nil)

	m.handleDeleteEntry(w, req, sentinelDeleteParams{
		getLabel:  func(id int64) (string, error) { return "pattern", nil },
		deleteFn:  func(id int64) error { return nil },
		getErrMsg: "get error",
		delErrMsg: "del error",
		logAction: "removed",
		logField:  "ip",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("handleDeleteEntry invalid id status = %d, want 400", w.Code)
	}
}

func TestMiddlewareAutoBanDisabled(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Disable auto-ban via settings
	if err := m.updateSetting(settingAutoBanEnabled, false); err != nil {
		t.Fatalf("updateSetting: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}

	mw := m.GetMiddleware()
	var handlerCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	// /wp-admin would normally trigger auto-ban
	req := httptest.NewRequest(http.MethodGet, "/wp-admin", nil)
	req.RemoteAddr = "5.5.5.5:1234"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("next handler should be called when auto-ban is disabled")
	}
	if m.IsIPBanned("5.5.5.5") {
		t.Error("IP should NOT be auto-banned when auto-ban is disabled")
	}
}

// ============================================================================
// TranslationsFS
// ============================================================================

func TestTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	// Just verify no panic and the fs is usable
	entries, err := fs.ReadDir("locales")
	if err != nil {
		t.Fatalf("TranslationsFS ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("TranslationsFS should contain at least one locale file")
	}
}

// ============================================================================
// Honeypot auto-ban via hook
// ============================================================================

func TestHoneypotHookBansIP(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Verify hook is registered
	if !m.ctx.Hooks.HasHandlers(module.HookSecurityHoneypotTriggered) {
		t.Fatal("HookSecurityHoneypotTriggered should be registered after Init")
	}

	// Fire the hook with honeypot data
	err := m.ctx.Hooks.CallNoResult(context.Background(), module.HookSecurityHoneypotTriggered, map[string]any{
		"ip":          "9.9.9.9",
		"form_slug":   "contact-us",
		"form_id":     int64(1),
		"request_url": "/forms/contact-us",
	})
	if err != nil {
		t.Fatalf("CallNoResult: %v", err)
	}

	if !m.IsIPBanned("9.9.9.9") {
		t.Error("IP 9.9.9.9 should be banned after honeypot trigger")
	}
}

func TestHoneypotHookSkipsWhenDisabled(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Disable honeypot auto-ban
	if err := m.updateSetting(settingHoneypotAutoBanEnabled, false); err != nil {
		t.Fatalf("updateSetting: %v", err)
	}
	if err := m.reloadSettings(); err != nil {
		t.Fatalf("reloadSettings: %v", err)
	}

	err := m.ctx.Hooks.CallNoResult(context.Background(), module.HookSecurityHoneypotTriggered, map[string]any{
		"ip":          "8.8.8.8",
		"form_slug":   "contact-us",
		"form_id":     int64(1),
		"request_url": "/forms/contact-us",
	})
	if err != nil {
		t.Fatalf("CallNoResult: %v", err)
	}

	if m.IsIPBanned("8.8.8.8") {
		t.Error("IP should NOT be banned when honeypot auto-ban is disabled")
	}
}

func TestHoneypotHookSkipsWhitelistedIP(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Whitelist the IP first
	if err := m.createWhitelistEntry("7.7.7.7", "test whitelist", 0); err != nil {
		t.Fatalf("createWhitelistEntry: %v", err)
	}

	err := m.ctx.Hooks.CallNoResult(context.Background(), module.HookSecurityHoneypotTriggered, map[string]any{
		"ip":          "7.7.7.7",
		"form_slug":   "contact-us",
		"form_id":     int64(1),
		"request_url": "/forms/contact-us",
	})
	if err != nil {
		t.Fatalf("CallNoResult: %v", err)
	}

	if m.IsIPBanned("7.7.7.7") {
		t.Error("whitelisted IP should NOT be banned on honeypot trigger")
	}
}

func TestHoneypotHookIdempotent(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	data := map[string]any{
		"ip":          "6.6.6.6",
		"form_slug":   "contact-us",
		"form_id":     int64(1),
		"request_url": "/forms/contact-us",
	}

	// Fire twice — should not error on duplicate
	for i := 0; i < 2; i++ {
		err := m.ctx.Hooks.CallNoResult(context.Background(), module.HookSecurityHoneypotTriggered, data)
		if err != nil {
			t.Fatalf("CallNoResult attempt %d: %v", i+1, err)
		}
	}

	if !m.IsIPBanned("6.6.6.6") {
		t.Error("IP should be banned after honeypot trigger")
	}
}
