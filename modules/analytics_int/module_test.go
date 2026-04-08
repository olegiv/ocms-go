// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/geoip"
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

	if m.Version() != "1.0.1" {
		t.Errorf("Version() = %q, want %q", m.Version(), "1.0.1")
	}

	if m.AdminURL() != "/admin/internal-analytics" {
		t.Errorf("AdminURL() = %q, want %q", m.AdminURL(), "/admin/internal-analytics")
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()
	migrations := m.Migrations()

	if len(migrations) != 11 {
		t.Errorf("expected 11 migrations, got %d", len(migrations))
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
		"page_analytics_reads",
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
	g := geoip.NewLookup()

	// Test without database (graceful degradation)
	if err := g.Init(""); err != nil {
		t.Fatalf("geoip.Lookup.Init failed: %v", err)
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
	g := geoip.NewLookup()

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
			result := geoip.CountryName(tt.code)
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
			// Use calendar day difference to avoid DST hour shifts
			actualDays := int(end.Sub(start).Hours()+12) / 24

			// Allow 2 day tolerance for DST transitions and inclusive counting
			if actualDays < tt.expectedDays-2 || actualDays > tt.expectedDays+2 {
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

func TestInsertPageRead(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	read := &PageRead{
		VisitorHash: "visitor123",
		Path:        "/test-post",
		SessionHash: "session123",
		ScrollDepth: 75,
		TimeOnPage:  45,
		CreatedAt:   time.Now(),
	}

	if err := m.insertPageRead(read); err != nil {
		t.Fatalf("insertPageRead failed: %v", err)
	}

	// Verify it was inserted
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM page_analytics_reads WHERE path = ?", "/test-post").Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	// Verify data
	var scrollDepth, timeOnPage int
	err = db.QueryRow(`
		SELECT scroll_depth, time_on_page
		FROM page_analytics_reads WHERE path = ?
	`, "/test-post").Scan(&scrollDepth, &timeOnPage)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if scrollDepth != 75 {
		t.Errorf("scroll_depth = %d, want 75", scrollDepth)
	}
	if timeOnPage != 45 {
		t.Errorf("time_on_page = %d, want 45", timeOnPage)
	}
}

func TestInsertPageRead_Deduplication(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	read := &PageRead{
		VisitorHash: "visitor123",
		Path:        "/dedup-post",
		SessionHash: "session-dedup",
		ScrollDepth: 70,
		TimeOnPage:  35,
		CreatedAt:   time.Now(),
	}

	// First insert should succeed
	if err := m.insertPageRead(read); err != nil {
		t.Fatalf("first insertPageRead failed: %v", err)
	}

	// Second insert with same session_hash + path should fail (UNIQUE constraint)
	err := m.insertPageRead(read)
	if err == nil {
		t.Error("expected UNIQUE constraint error for duplicate session+path, got nil")
	}

	// Verify only 1 row exists
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM page_analytics_reads WHERE path = ?", "/dedup-post").Scan(&count); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after dedup, got %d", count)
	}
}

func TestGetPageStats(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	now := time.Now()

	// Insert views
	views := []*PageView{
		{VisitorHash: "v1", Path: "/stats-post", SessionHash: "s1", CreatedAt: now},
		{VisitorHash: "v2", Path: "/stats-post", SessionHash: "s2", CreatedAt: now},
		{VisitorHash: "v3", Path: "/stats-post", SessionHash: "s3", CreatedAt: now},
	}
	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Insert reads
	reads := []*PageRead{
		{VisitorHash: "v1", Path: "/stats-post", SessionHash: "s1", ScrollDepth: 80, TimeOnPage: 60, CreatedAt: now},
		{VisitorHash: "v2", Path: "/stats-post", SessionHash: "s2", ScrollDepth: 65, TimeOnPage: 40, CreatedAt: now},
	}
	for _, r := range reads {
		if err := m.insertPageRead(r); err != nil {
			t.Fatalf("insertPageRead failed: %v", err)
		}
	}

	stats := m.getPageStats(context.Background(), "/stats-post")
	if stats.Views != 3 {
		t.Errorf("Views = %d, want 3", stats.Views)
	}
	if stats.Reads != 2 {
		t.Errorf("Reads = %d, want 2", stats.Reads)
	}
}

func TestGetPageStats_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	stats := m.getPageStats(context.Background(), "/nonexistent")
	if stats.Views != 0 {
		t.Errorf("Views = %d, want 0", stats.Views)
	}
	if stats.Reads != 0 {
		t.Errorf("Reads = %d, want 0", stats.Reads)
	}
}

func TestGetPageStatsReport(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	now := time.Now()

	// Insert views for two paths
	viewData := []*PageView{
		{VisitorHash: "v1", Path: "/post-a", SessionHash: "s1", CreatedAt: now},
		{VisitorHash: "v2", Path: "/post-a", SessionHash: "s2", CreatedAt: now},
		{VisitorHash: "v3", Path: "/post-b", SessionHash: "s3", CreatedAt: now},
	}
	for _, v := range viewData {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Insert a read
	read := &PageRead{
		VisitorHash: "v1", Path: "/post-a", SessionHash: "s1",
		ScrollDepth: 80, TimeOnPage: 60, CreatedAt: now,
	}
	if err := m.insertPageRead(read); err != nil {
		t.Fatalf("insertPageRead failed: %v", err)
	}

	// Use parseDateRange("30d") to match real handler behavior.
	start, end := parseDateRange("30d")
	rows := m.getPageStatsReport(context.Background(), start, end, 10, 0)
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 report rows, got %d", len(rows))
	}

	// First row should be /post-a (most views)
	if rows[0].Path != "/post-a" {
		t.Errorf("first row path = %q, want /post-a", rows[0].Path)
	}
	if rows[0].Views != 2 {
		t.Errorf("first row views = %d, want 2", rows[0].Views)
	}
	if rows[0].Reads != 1 {
		t.Errorf("first row reads = %d, want 1", rows[0].Reads)
	}
	if rows[0].ReadRate < 49 || rows[0].ReadRate > 51 {
		t.Errorf("first row read rate = %.1f%%, want ~50%%", rows[0].ReadRate)
	}
}

func TestGetPageStatsReportCount(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	now := time.Now()

	// Insert views for 3 distinct paths
	for _, path := range []string{"/a", "/b", "/c"} {
		v := &PageView{VisitorHash: "v1", Path: path, SessionHash: "s" + path, CreatedAt: now}
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	start, end := parseDateRange("30d")
	count := m.getPageStatsReportCount(context.Background(), start, end)
	if count != 3 {
		t.Errorf("report count = %d, want 3", count)
	}
}

func TestSettingsShowPostStats(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Default should be true
	if !m.settings.ShowPostStats {
		t.Error("ShowPostStats should default to true")
	}

	// Disable and save
	m.settings.ShowPostStats = false
	if err := m.saveSettings(); err != nil {
		t.Fatalf("saveSettings failed: %v", err)
	}

	// Reload and verify
	loaded, err := m.loadSettings()
	if err != nil {
		t.Fatalf("loadSettings failed: %v", err)
	}
	if loaded.ShowPostStats {
		t.Error("ShowPostStats should be false after save")
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
