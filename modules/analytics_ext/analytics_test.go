package analytics_ext

import (
	"database/sql"
	"log/slog"
	"os"
	"strings"
	"testing"

	"ocms-go/internal/config"
	"ocms-go/internal/module"
	"ocms-go/internal/store"
)

// testDB creates a temporary test database.
func testDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	// Create temp file for test database
	f, err := os.CreateTemp("", "ocms-analytics-test-*.db")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	// Open database
	db, err := store.NewDB(dbPath)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("NewDB: %v", err)
	}

	// Run core migrations
	if err := store.Migrate(db); err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Migrate: %v", err)
	}

	// Return cleanup function
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return db, cleanup
}

// runModuleMigrations runs all migrations for the given module.
func runModuleMigrations(t *testing.T, m *Module, db *sql.DB) {
	t.Helper()
	for _, mig := range m.Migrations() {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration up: %v", err)
		}
	}
}

// runModuleMigrationsDown rolls back all migrations for the given module.
func runModuleMigrationsDown(t *testing.T, m *Module, db *sql.DB) {
	t.Helper()
	for _, mig := range m.Migrations() {
		if err := mig.Down(db); err != nil {
			t.Fatalf("migration down: %v", err)
		}
	}
}

// testLogger creates a test logger that only outputs warnings and above.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
}

// testModuleContext creates a module.Context for testing.
func testModuleContext(db *sql.DB) *module.Context {
	logger := testLogger()
	return &module.Context{
		DB:     db,
		Logger: logger,
		Config: &config.Config{},
		Hooks:  module.NewHookRegistry(logger),
	}
}

// testModule creates a test Module with database access
func testModule(t *testing.T, db *sql.DB) *Module {
	t.Helper()

	m := New()
	runModuleMigrations(t, m, db)

	if err := m.Init(testModuleContext(db)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	return m
}

func TestModuleNew(t *testing.T) {
	m := New()

	if m.Name() != "analytics_ext" {
		t.Errorf("Name() = %q, want analytics_ext", m.Name())
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

	if m.AdminURL() != "/admin/external-analytics" {
		t.Errorf("AdminURL() = %q, want /admin/external-analytics", m.AdminURL())
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()

	migrations := m.Migrations()
	if len(migrations) != 1 {
		t.Errorf("len(migrations) = %d, want 1", len(migrations))
	}

	if migrations[0].Version != 1 {
		t.Errorf("migration version = %d, want 1", migrations[0].Version)
	}
	if migrations[0].Description == "" {
		t.Error("migration description should not be empty")
	}
}

func TestModuleTemplateFuncs(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := testModule(t, db)

	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs() returned nil")
	}

	// Check analyticsExtHead function exists
	if _, ok := funcs["analyticsExtHead"]; !ok {
		t.Error("analyticsExtHead function not found")
	}

	// Check analyticsExtBody function exists
	if _, ok := funcs["analyticsExtBody"]; !ok {
		t.Error("analyticsExtBody function not found")
	}
}

func TestLoadSettings(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := New()
	runModuleMigrations(t, m, db)

	// Load default settings
	settings, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}

	// Default values should be false/empty
	if settings.GA4Enabled {
		t.Error("GA4Enabled should be false by default")
	}
	if settings.GTMEnabled {
		t.Error("GTMEnabled should be false by default")
	}
	if settings.MatomoEnabled {
		t.Error("MatomoEnabled should be false by default")
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := New()
	runModuleMigrations(t, m, db)

	// Create settings
	settings := &Settings{
		GA4Enabled:       true,
		GA4MeasurementID: "G-TESTTEST01",
		GTMEnabled:       true,
		GTMContainerID:   "GTM-TEST001",
		MatomoEnabled:    true,
		MatomoURL:        "https://matomo.example.com",
		MatomoSiteID:     "42",
	}

	// Save settings
	if err := saveSettings(db, settings); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	// Load settings back
	loaded, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}

	// Verify all fields
	if loaded.GA4Enabled != settings.GA4Enabled {
		t.Errorf("GA4Enabled = %v, want %v", loaded.GA4Enabled, settings.GA4Enabled)
	}
	if loaded.GA4MeasurementID != settings.GA4MeasurementID {
		t.Errorf("GA4MeasurementID = %q, want %q", loaded.GA4MeasurementID, settings.GA4MeasurementID)
	}
	if loaded.GTMEnabled != settings.GTMEnabled {
		t.Errorf("GTMEnabled = %v, want %v", loaded.GTMEnabled, settings.GTMEnabled)
	}
	if loaded.GTMContainerID != settings.GTMContainerID {
		t.Errorf("GTMContainerID = %q, want %q", loaded.GTMContainerID, settings.GTMContainerID)
	}
	if loaded.MatomoEnabled != settings.MatomoEnabled {
		t.Errorf("MatomoEnabled = %v, want %v", loaded.MatomoEnabled, settings.MatomoEnabled)
	}
	if loaded.MatomoURL != settings.MatomoURL {
		t.Errorf("MatomoURL = %q, want %q", loaded.MatomoURL, settings.MatomoURL)
	}
	if loaded.MatomoSiteID != settings.MatomoSiteID {
		t.Errorf("MatomoSiteID = %q, want %q", loaded.MatomoSiteID, settings.MatomoSiteID)
	}
}

func TestRenderHeadScripts_NilSettings(t *testing.T) {
	m := New()
	m.settings = nil

	result := m.renderHeadScripts()
	if result != "" {
		t.Errorf("renderHeadScripts with nil settings should return empty, got %q", result)
	}
}

func TestRenderHeadScripts_GA4Only(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GA4Enabled:       true,
		GA4MeasurementID: "G-TESTID123",
		GTMEnabled:       false,
		MatomoEnabled:    false,
	}

	result := string(m.renderHeadScripts())

	// Should contain GA4 script
	if !strings.Contains(result, "G-TESTID123") {
		t.Error("GA4 measurement ID not found in output")
	}
	if !strings.Contains(result, "googletagmanager.com/gtag/js") {
		t.Error("GA4 script URL not found")
	}
	if !strings.Contains(result, "Google Analytics 4") {
		t.Error("GA4 comment not found")
	}

	// Should NOT contain GTM
	if strings.Contains(result, "gtm.js") {
		t.Error("GTM script should not be present when GA4 only")
	}
}

func TestRenderHeadScripts_GTMOnly(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GA4Enabled:     false,
		GTMEnabled:     true,
		GTMContainerID: "GTM-TESTID1",
		MatomoEnabled:  false,
	}

	result := string(m.renderHeadScripts())

	// Should contain GTM script
	if !strings.Contains(result, "GTM-TESTID1") {
		t.Error("GTM container ID not found in output")
	}
	if !strings.Contains(result, "googletagmanager.com/gtm.js") {
		t.Error("GTM script URL not found")
	}
	if !strings.Contains(result, "Google Tag Manager") {
		t.Error("GTM comment not found")
	}
}

func TestRenderHeadScripts_GTMOverridesGA4(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GA4Enabled:       true,
		GA4MeasurementID: "G-TESTID123",
		GTMEnabled:       true,
		GTMContainerID:   "GTM-TESTID1",
		MatomoEnabled:    false,
	}

	result := string(m.renderHeadScripts())

	// Should contain GTM
	if !strings.Contains(result, "GTM-TESTID1") {
		t.Error("GTM container ID not found")
	}

	// Should NOT contain standalone GA4 (since GTM is enabled)
	if strings.Contains(result, "G-TESTID123") {
		t.Error("GA4 should not render when GTM is enabled")
	}
}

func TestRenderHeadScripts_MatomoOnly(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GA4Enabled:    false,
		GTMEnabled:    false,
		MatomoEnabled: true,
		MatomoURL:     "https://analytics.example.com/",
		MatomoSiteID:  "123",
	}

	result := string(m.renderHeadScripts())

	// Should contain Matomo script
	if !strings.Contains(result, "analytics.example.com") {
		t.Error("Matomo URL not found in output")
	}
	if !strings.Contains(result, "setSiteId") {
		t.Error("Matomo setSiteId not found")
	}
	if !strings.Contains(result, "'123'") {
		t.Error("Matomo site ID not found")
	}
	if !strings.Contains(result, "Matomo") {
		t.Error("Matomo comment not found")
	}
}

func TestRenderHeadScripts_MatomoURLTrailingSlash(t *testing.T) {
	m := New()
	m.settings = &Settings{
		MatomoEnabled: true,
		MatomoURL:     "https://analytics.example.com/",
		MatomoSiteID:  "1",
	}

	result := string(m.renderHeadScripts())

	// Trailing slash should be removed and not doubled
	if strings.Contains(result, "example.com//") {
		t.Error("Double slashes found - trailing slash not properly handled")
	}
}

func TestRenderBodyScripts_NilSettings(t *testing.T) {
	m := New()
	m.settings = nil

	result := m.renderBodyScripts()
	if result != "" {
		t.Errorf("renderBodyScripts with nil settings should return empty, got %q", result)
	}
}

func TestRenderBodyScripts_GTMNoscript(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GTMEnabled:     true,
		GTMContainerID: "GTM-BODYTEST",
	}

	result := string(m.renderBodyScripts())

	// Should contain GTM noscript fallback
	if !strings.Contains(result, "GTM-BODYTEST") {
		t.Error("GTM container ID not found in body scripts")
	}
	if !strings.Contains(result, "<noscript>") {
		t.Error("noscript tag not found")
	}
	if !strings.Contains(result, "ns.html") {
		t.Error("GTM noscript URL not found")
	}
}

func TestRenderBodyScripts_MatomoNoscript(t *testing.T) {
	m := New()
	m.settings = &Settings{
		MatomoEnabled: true,
		MatomoURL:     "https://matomo.test.com",
		MatomoSiteID:  "99",
	}

	result := string(m.renderBodyScripts())

	// Should contain Matomo noscript image tracker
	if !strings.Contains(result, "matomo.test.com") {
		t.Error("Matomo URL not found in body scripts")
	}
	if !strings.Contains(result, "<noscript>") {
		t.Error("noscript tag not found")
	}
	if !strings.Contains(result, "matomo.php") {
		t.Error("Matomo tracker URL not found")
	}
	if !strings.Contains(result, "idsite=99") {
		t.Error("Matomo site ID not found in tracker URL")
	}
}

func TestRenderHeadScripts_DisabledTrackers(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GA4Enabled:       false,
		GA4MeasurementID: "G-SHOULDNOTAPPEAR",
		GTMEnabled:       false,
		GTMContainerID:   "GTM-SHOULDNOTAPPEAR",
		MatomoEnabled:    false,
		MatomoURL:        "https://shouldnotappear.com",
		MatomoSiteID:     "999",
	}

	result := string(m.renderHeadScripts())

	// Nothing should appear
	if result != "" {
		t.Errorf("Expected empty output when all trackers disabled, got: %s", result)
	}
}

func TestRenderHeadScripts_EnabledWithoutID(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GA4Enabled:       true,
		GA4MeasurementID: "", // Empty ID
		GTMEnabled:       true,
		GTMContainerID:   "", // Empty ID
	}

	result := string(m.renderHeadScripts())

	// Should be empty since IDs are not set
	if result != "" {
		t.Errorf("Expected empty output when IDs are empty, got: %s", result)
	}
}

func TestRenderScripts_HTMLEscaping(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GA4Enabled:       true,
		GA4MeasurementID: "G-<script>alert('xss')</script>",
	}

	result := string(m.renderHeadScripts())

	// Should not contain raw script tags
	if strings.Contains(result, "<script>alert") {
		t.Error("XSS vulnerability: script tag not escaped")
	}

	// Should contain escaped version
	if !strings.Contains(result, "&lt;script&gt;") {
		t.Error("Script tag not properly HTML escaped")
	}
}

func TestModuleInit(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := New()
	runModuleMigrations(t, m, db)

	if err := m.Init(testModuleContext(db)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Settings should be loaded
	if m.settings == nil {
		t.Error("settings should be initialized after Init")
	}
}

func TestModuleShutdown(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Shutdown should not error
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestReloadSettings(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Save new settings directly to DB
	newSettings := &Settings{
		GA4Enabled:       true,
		GA4MeasurementID: "G-RELOADED01",
	}
	if err := saveSettings(db, newSettings); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	// Reload settings
	if err := m.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings: %v", err)
	}

	// Verify settings were reloaded
	if !m.settings.GA4Enabled {
		t.Error("GA4Enabled should be true after reload")
	}
	if m.settings.GA4MeasurementID != "G-RELOADED01" {
		t.Errorf("GA4MeasurementID = %q, want G-RELOADED01", m.settings.GA4MeasurementID)
	}
}

func TestMigrationDown(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := New()
	runModuleMigrations(t, m, db)
	runModuleMigrationsDown(t, m, db)

	// Table should not exist
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='analytics_settings'").Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Error("analytics_settings table should not exist after migration down")
	}
}

func TestAllTrackersEnabled(t *testing.T) {
	m := New()
	m.settings = &Settings{
		GA4Enabled:       true,
		GA4MeasurementID: "G-ALL12345",
		GTMEnabled:       true,
		GTMContainerID:   "GTM-ALL123",
		MatomoEnabled:    true,
		MatomoURL:        "https://all.example.com",
		MatomoSiteID:     "1",
	}

	headResult := string(m.renderHeadScripts())
	bodyResult := string(m.renderBodyScripts())

	// GTM should be present (overrides GA4)
	if !strings.Contains(headResult, "GTM-ALL123") {
		t.Error("GTM not found in head scripts")
	}

	// Matomo should be present
	if !strings.Contains(headResult, "all.example.com") {
		t.Error("Matomo not found in head scripts")
	}

	// Body should have GTM and Matomo noscripts
	if !strings.Contains(bodyResult, "GTM-ALL123") {
		t.Error("GTM noscript not found in body")
	}
	if !strings.Contains(bodyResult, "all.example.com") {
		t.Error("Matomo noscript not found in body")
	}
}
