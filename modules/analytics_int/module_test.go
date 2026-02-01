// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// testModule creates a module instance with test database.
func testModule(t *testing.T, db *sql.DB) *Module {
	t.Helper()
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContextWithStore(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m
}

func TestModuleNew(t *testing.T) {
	m := New()

	if m.Name() != "analytics_int" {
		t.Errorf("Name() = %q, want %q", m.Name(), "analytics_int")
	}

	if m.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want %q", m.Version(), "1.0.0")
	}

	if m.AdminURL() != "/admin/internal-analytics" {
		t.Errorf("AdminURL() = %q, want %q", m.AdminURL(), "/admin/internal-analytics")
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()
	migrations := m.Migrations()

	if len(migrations) != 7 {
		t.Errorf("expected 7 migrations, got %d", len(migrations))
	}

	// Verify migration versions are sequential
	for i, mig := range migrations {
		expectedVersion := int64(i + 1)
		if mig.Version != expectedVersion {
			t.Errorf("migration %d has version %d, want %d", i, mig.Version, expectedVersion)
		}
	}
}

func TestModuleMigrationUp(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	// Verify tables exist
	tables := []string{
		"page_analytics_views",
		"page_analytics_hourly",
		"page_analytics_daily",
		"page_analytics_referrers",
		"page_analytics_tech",
		"page_analytics_geo",
		"page_analytics_settings",
	}

	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}
}

func TestModuleMigrationDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	migrations := m.Migrations()

	// Run all migrations up
	for _, mig := range migrations {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration %d Up failed: %v", mig.Version, err)
		}
	}

	// Run all migrations down in reverse
	for i := len(migrations) - 1; i >= 0; i-- {
		if err := migrations[i].Down(db); err != nil {
			t.Fatalf("migration %d Down failed: %v", migrations[i].Version, err)
		}
	}

	// Verify tables are dropped
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name LIKE 'page_analytics_%'
	`).Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 page_analytics tables after Down, got %d", count)
	}
}

func TestModuleInit(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Verify settings are loaded
	if m.settings == nil {
		t.Fatal("settings should be loaded after Init")
	}

	if !m.settings.Enabled {
		t.Error("module should be enabled by default")
	}

	if m.settings.RetentionDays != 365 {
		t.Errorf("RetentionDays = %d, want 365", m.settings.RetentionDays)
	}

	// Verify salt is generated
	if m.settings.CurrentSalt == "" {
		t.Error("CurrentSalt should be generated after Init")
	}
}

func TestModuleIsEnabled(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	if !m.IsEnabled() {
		t.Error("IsEnabled() should return true by default")
	}

	// Disable and check
	m.settings.Enabled = false
	if m.IsEnabled() {
		t.Error("IsEnabled() should return false when disabled")
	}
}

func TestInsertPageView(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	view := &PageView{
		VisitorHash:    "abc123def456",
		Path:           "/test-page",
		ReferrerDomain: "google.com",
		CountryCode:    "US",
		Browser:        "Chrome",
		OS:             "Windows",
		DeviceType:     "desktop",
		Language:       "en",
		SessionHash:    "session123456",
		CreatedAt:      time.Now(),
	}

	if err := m.insertPageView(view); err != nil {
		t.Fatalf("insertPageView failed: %v", err)
	}

	// Verify it was inserted
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM page_analytics_views WHERE path = ?", "/test-page").Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	// Verify data
	var browser, deviceType, referrerDomain string
	err = db.QueryRow(`
		SELECT browser, device_type, referrer_domain
		FROM page_analytics_views WHERE path = ?
	`, "/test-page").Scan(&browser, &deviceType, &referrerDomain)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if browser != "Chrome" {
		t.Errorf("browser = %q, want %q", browser, "Chrome")
	}
	if deviceType != "desktop" {
		t.Errorf("deviceType = %q, want %q", deviceType, "desktop")
	}
	if referrerDomain != "google.com" {
		t.Errorf("referrerDomain = %q, want %q", referrerDomain, "google.com")
	}
}

func TestGetRealTimeVisitorCount(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Initially should be 0
	count := m.GetRealTimeVisitorCount(5)
	if count != 0 {
		t.Errorf("expected 0 visitors initially, got %d", count)
	}

	// Insert some page views
	now := time.Now()
	views := []*PageView{
		{VisitorHash: "visitor1", Path: "/", SessionHash: "s1", CreatedAt: now},
		{VisitorHash: "visitor1", Path: "/about", SessionHash: "s1", CreatedAt: now}, // same visitor
		{VisitorHash: "visitor2", Path: "/", SessionHash: "s2", CreatedAt: now},
		{VisitorHash: "visitor3", Path: "/", SessionHash: "s3", CreatedAt: now.Add(-10 * time.Minute)}, // outside window
	}

	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Should count unique visitors in last 5 minutes
	count = m.GetRealTimeVisitorCount(5)
	if count != 2 {
		t.Errorf("expected 2 unique visitors, got %d", count)
	}
}

func TestGeoIPLookup(t *testing.T) {
	g := NewGeoIPLookup()

	// Test without database (graceful degradation)
	if err := g.Init(""); err != nil {
		t.Fatalf("GeoIPLookup.Init failed: %v", err)
	}

	// Without database, only private IPs should return LOCAL
	// Test private IP detection
	result := g.LookupCountry("192.168.1.1")
	if result != "LOCAL" {
		t.Errorf("LookupCountry(192.168.1.1) = %q, want %q", result, "LOCAL")
	}

	result = g.LookupCountry("10.0.0.1")
	if result != "LOCAL" {
		t.Errorf("LookupCountry(10.0.0.1) = %q, want %q", result, "LOCAL")
	}

	// Test loopback
	result = g.LookupCountry("127.0.0.1")
	if result != "LOCAL" {
		t.Errorf("LookupCountry(127.0.0.1) = %q, want %q", result, "LOCAL")
	}

	// Test invalid IP
	result = g.LookupCountry("invalid")
	if result != "" {
		t.Errorf("LookupCountry(invalid) = %q, want empty", result)
	}

	// Test public IP without database - should return empty
	result = g.LookupCountry("8.8.8.8")
	if result != "" {
		t.Errorf("LookupCountry(8.8.8.8) without DB = %q, want empty", result)
	}

	// Test IsEnabled
	if g.IsEnabled() {
		t.Error("GeoIP should not be enabled without database path")
	}
}

func TestGeoIPLookup_InvalidPath(t *testing.T) {
	g := NewGeoIPLookup()

	err := g.Init("/nonexistent/path/GeoLite2-Country.mmdb")
	if err == nil {
		t.Error("Init with invalid path should return error")
	}

	if g.IsEnabled() {
		t.Error("GeoIP should not be enabled with invalid path")
	}

	// Lookups should still work (graceful degradation)
	result := g.LookupCountry("192.168.1.1")
	if result != "LOCAL" {
		t.Errorf("LookupCountry(192.168.1.1) = %q, want LOCAL", result)
	}
}

func TestCountryName(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{"US", "United States"},
		{"GB", "United Kingdom"},
		{"DE", "Germany"},
		{"LOCAL", "Local Network"},
		{"", "Unknown"},
		{"XX", "XX"}, // Unknown code returns itself
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			result := CountryName(tt.code)
			if result != tt.expected {
				t.Errorf("CountryName(%q) = %q, want %q", tt.code, result, tt.expected)
			}
		})
	}
}

func TestParseDateRange(t *testing.T) {
	tests := []struct {
		rangeStr     string
		expectedDays int
	}{
		{"7d", 7},
		{"30d", 30},
		{"90d", 90},
		{"1y", 365},
		{"invalid", 30}, // default
	}

	for _, tt := range tests {
		t.Run(tt.rangeStr, func(t *testing.T) {
			start, end := parseDateRange(tt.rangeStr)
			diff := end.Sub(start)
			actualDays := int(diff.Hours() / 24)

			// Allow 1 day tolerance for edge cases
			if actualDays < tt.expectedDays-1 || actualDays > tt.expectedDays+1 {
				t.Errorf("parseDateRange(%q) gave %d days, want ~%d", tt.rangeStr, actualDays, tt.expectedDays)
			}
		})
	}
}

func TestSettingsSaveLoad(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Modify settings
	m.settings.Enabled = false
	m.settings.RetentionDays = 180
	m.settings.ExcludePaths = []string{"/private", "/api"}

	// Save
	if err := m.saveSettings(); err != nil {
		t.Fatalf("saveSettings failed: %v", err)
	}

	// Load fresh
	loaded, err := m.loadSettings()
	if err != nil {
		t.Fatalf("loadSettings failed: %v", err)
	}

	if loaded.Enabled != false {
		t.Error("Enabled should be false")
	}
	if loaded.RetentionDays != 180 {
		t.Errorf("RetentionDays = %d, want 180", loaded.RetentionDays)
	}
	if len(loaded.ExcludePaths) != 2 {
		t.Errorf("ExcludePaths length = %d, want 2", len(loaded.ExcludePaths))
	}
}

func TestTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()

	// Try to read English translations
	_, err := fs.ReadFile("locales/en/messages.json")
	if err != nil {
		t.Errorf("failed to read English translations: %v", err)
	}

	// Try to read Russian translations
	_, err = fs.ReadFile("locales/ru/messages.json")
	if err != nil {
		t.Errorf("failed to read Russian translations: %v", err)
	}
}
