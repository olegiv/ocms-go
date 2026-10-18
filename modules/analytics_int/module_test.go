// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/geoip"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
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

// TestInitKeepsStoredSettingsWhenLoadFails checks that a settings load error
// at startup leaves tracking off and the stored row untouched. The fallback
// used to enable tracking and save its defaults with a fresh salt, so one
// failed read switched tracking back on for good.
func TestInitKeepsStoredSettingsWhenLoadFails(t *testing.T) {
	m, db, _ := initWithUnreadableSettings(t, time.Nanosecond)

	if m.IsEnabled() {
		t.Error("tracking is on after the settings failed to load")
	}
	var enabled, retention int
	var salt, created string
	if err := db.QueryRow(`SELECT enabled, retention_days, current_salt, salt_created_at
		FROM page_analytics_settings WHERE id = 1`).Scan(&enabled, &retention, &salt, &created); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || retention != 30 || salt != "" || created != "not a time" {
		t.Errorf("stored settings changed after a failed load: enabled=%d retention=%d salt=%q created=%q",
			enabled, retention, salt, created)
	}

	// Nothing acts on the fallback values: expiry cleanup would delete this
	// 400-day-old row by the fallback 365-day retention, and saving the form
	// would write the fallback values over the stored row.
	oldDate := time.Now().AddDate(0, 0, -400).Format(dateFormat)
	if _, err := db.Exec(`INSERT INTO page_analytics_daily (date, path, views) VALUES (?, '/old', 1)`, oldDate); err != nil {
		t.Fatal(err)
	}
	if err := m.cleanupExpiredData(context.Background()); !errors.Is(err, errSettingsNotLoaded) {
		t.Fatalf("cleanupExpiredData = %v, want errSettingsNotLoaded so the scheduler shows the skip", err)
	}
	countDaily := func() int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM page_analytics_daily WHERE path = '/old'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if countDaily() != 1 {
		t.Error("expiry cleanup ran on fallback settings")
	}
	admin := store.User{ID: 1, Email: "admin@test.com", Role: model.RoleAdmin}
	dashboard := func() string {
		req := httptest.NewRequest(http.MethodGet, "/admin/internal-analytics", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
		rr := httptest.NewRecorder()
		m.handleDashboard(rr, req)
		return rr.Body.String()
	}
	if page := dashboard(); !strings.Contains(page, `name="from_fallback"`) || !strings.Contains(page, `role="alert"`) {
		t.Error("the dashboard does not warn or mark its settings form while showing fallback values")
	}
	form := url.Values{"enabled": {"1"}, "retention_days": {"365"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/internal-analytics/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
	m.handleSaveSettings(httptest.NewRecorder(), req)
	if err := db.QueryRow(`SELECT enabled, retention_days FROM page_analytics_settings WHERE id = 1`).Scan(&enabled, &retention); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || retention != 30 {
		t.Errorf("the settings form saved over unread settings: enabled=%d retention=%d", enabled, retention)
	}

	// Once the row can be read, the hourly job picks the stored settings up
	// and the stored 30-day retention applies.
	if _, err := db.Exec(`UPDATE page_analytics_settings SET enabled = 1, salt_created_at = CURRENT_TIMESTAMP WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	// A form rendered from fallback values is refused even though the row can
	// be read again now.
	form.Set("from_fallback", "1")
	req = httptest.NewRequest(http.MethodPost, "/admin/internal-analytics/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
	m.handleSaveSettings(httptest.NewRecorder(), req)
	if err := db.QueryRow(`SELECT retention_days FROM page_analytics_settings WHERE id = 1`).Scan(&retention); err != nil {
		t.Fatal(err)
	}
	if retention != 30 {
		t.Errorf("retention = %d: a form rendered from fallback values replaced the recovered settings", retention)
	}
	time.Sleep(time.Millisecond) // past the retry interval
	if err := m.aggregateHourly(context.Background()); err != nil {
		t.Fatalf("aggregateHourly: %v", err)
	}
	if !m.IsEnabled() || m.settingsSnapshot().RetentionDays != 30 {
		t.Errorf("after the retry: enabled=%v retention=%d, want the stored settings", m.IsEnabled(), m.settingsSnapshot().RetentionDays)
	}
	if err := m.cleanupExpiredData(context.Background()); err != nil {
		t.Fatalf("cleanupExpiredData: %v", err)
	}
	if countDaily() != 0 {
		t.Error("expiry cleanup did not apply the stored retention once the settings loaded")
	}
}

// initWithUnreadableSettings initializes the module over stored settings it
// cannot read (tracking off, 30-day retention, a timestamp that does not
// scan), retrying the load at most once per retry interval. It returns the
// buffer the module logs to.
func initWithUnreadableSettings(t *testing.T, retry time.Duration) (*Module, *sql.DB, *bytes.Buffer) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	m := New()
	m.reload.RetryInterval = retry
	moduleutil.RunMigrations(t, db, m.Migrations())
	if _, err := db.Exec(`UPDATE page_analytics_settings
		SET enabled = 0, retention_days = 30, current_salt = '', salt_created_at = 'not a time'
		WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	ctx, _ := moduleutil.TestModuleContextWithStore(t, db)
	logs := &bytes.Buffer{}
	ctx.Logger = slog.New(slog.NewTextHandler(logs, nil))
	m.ctx = ctx
	if _, err := m.loadSettings(); err == nil {
		t.Fatal("loadSettings accepted a malformed timestamp; the test no longer exercises a failed load")
	}
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown() })
	return m, db, logs
}

// TestSettingsRetriesFollowTheirInterval checks which callers wait out the
// retry interval for unreadable settings. The dashboard and the hourly job
// retry at most once per interval, as hCaptcha and Privacy do; the dashboard
// used to reload on every view, querying the database and logging a warning
// each time. The weekly expiry cleanup reloads at once, because it starts in
// the same scheduler tick as the hourly job and would otherwise find that
// job's retry and skip a week. A save reloads at once too.
//
// Bug state: make handleDashboard or aggregateHourly call
// ensureSettingsLoaded, or cleanupExpiredData or handleSaveSettings call
// retrySettingsLoad, and this fails.
func TestSettingsRetriesFollowTheirInterval(t *testing.T) {
	m, db, logs := initWithUnreadableSettings(t, time.Hour)
	failures := func() int { return strings.Count(logs.String(), "analytics_int settings still cannot be read") }
	admin := store.User{ID: 1, Email: "admin@test.com", Role: model.RoleAdmin}
	for range 3 {
		req := httptest.NewRequest(http.MethodGet, "/admin/internal-analytics", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
		rr := httptest.NewRecorder()
		m.handleDashboard(rr, req)
		if !strings.Contains(rr.Body.String(), `name="from_fallback"`) {
			t.Fatal("the dashboard does not mark its settings form while the settings are unreadable")
		}
	}
	if n := failures(); n != 1 {
		t.Errorf("three dashboard views logged %d failed reloads, want 1 within the retry interval", n)
	}
	if err := m.aggregateHourly(context.Background()); err != nil {
		t.Fatalf("aggregateHourly: %v", err)
	}
	if n := failures(); n != 1 {
		t.Errorf("the hourly job after the views brought the failed reloads to %d, want it to wait out the interval", n)
	}

	oldDate := time.Now().AddDate(0, 0, -400).Format(dateFormat)
	if _, err := db.Exec(`INSERT INTO page_analytics_daily (date, path, views) VALUES (?, '/old', 1)`, oldDate); err != nil {
		t.Fatal(err)
	}
	if err := m.cleanupExpiredData(context.Background()); !errors.Is(err, errSettingsNotLoaded) {
		t.Errorf("cleanupExpiredData = %v, want errSettingsNotLoaded", err)
	}
	if n := failures(); n != 2 {
		t.Errorf("the cleanup brought the failed reloads to %d, want 2: it must reload at once", n)
	}

	// Repaired within the interval: the save reloads at once and goes through.
	if _, err := db.Exec(`UPDATE page_analytics_settings SET salt_created_at = CURRENT_TIMESTAMP WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"retention_days": {"60"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/internal-analytics/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
	m.handleSaveSettings(httptest.NewRecorder(), req)
	var retention int
	if err := db.QueryRow(`SELECT retention_days FROM page_analytics_settings WHERE id = 1`).Scan(&retention); err != nil {
		t.Fatal(err)
	}
	if retention != 60 {
		t.Errorf("retention = %d after a save once the row was repaired, want 60: the save must reload at once", retention)
	}
}

// TestSaltIsNotSavedOverUnreadSettings checks that a salt rotation never
// writes over stored settings the module could not read, and saves once
// they load. Saving the salt overwrites current_salt and salt_created_at,
// which can overwrite the very value that made the row unreadable before
// the administrator repairs it.
//
// Bug state: drop the m.reload.Loaded() check in getCurrentSalt and the
// stored row changes while the settings are unreadable.
func TestSaltIsNotSavedOverUnreadSettings(t *testing.T) {
	m, db, _ := initWithUnreadableSettings(t, time.Hour)
	stored := func() (salt, created string) {
		t.Helper()
		if err := db.QueryRow(`SELECT current_salt, salt_created_at FROM page_analytics_settings WHERE id = 1`).Scan(&salt, &created); err != nil {
			t.Fatal(err)
		}
		return salt, created
	}
	// Init gives the fallback a fresh salt; expire it so the next request
	// rotates it.
	m.settingsMu.Lock()
	fallbackSalt := m.settings.CurrentSalt
	m.settings.SaltCreatedAt = time.Now().Add(-48 * time.Hour)
	m.settingsMu.Unlock()
	if salt := m.getCurrentSalt(); salt == "" || salt == fallbackSalt {
		t.Fatalf("getCurrentSalt = %q on an expired fallback salt, want a new salt", salt)
	}
	if salt, created := stored(); salt != "" || created != "not a time" {
		t.Errorf("a rotation on fallback settings wrote salt=%q created=%q over the unread row", salt, created)
	}

	// Repaired with an expired salt: once loaded, the rotation is saved.
	if _, err := db.Exec(`UPDATE page_analytics_settings SET current_salt = 'old', salt_created_at = '2000-01-01 00:00:00' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureSettingsLoaded(); err != nil {
		t.Fatalf("ensureSettingsLoaded after the repair: %v", err)
	}
	salt := m.getCurrentSalt()
	if got, _ := stored(); got != salt || got == "old" {
		t.Errorf("stored salt = %q after a rotation on loaded settings, want the new salt %q", got, salt)
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

// TestGetRealTimeVisitorCount counts the visitors of the last minutes. The
// clock runs at -07:00, so the views are stored with a wall clock that is not
// UTC on any host, and a cutoff compared in UTC misses them (review of the
// 2026-10-17 audit fixes).
//
// Bug state: pass cutoff as a time.Time in GetRealTimeVisitorCount and this
// fails.
func TestGetRealTimeVisitorCount(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	now := time.Now().In(time.FixedZone("PDT", -7*3600))
	previous := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = previous })

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Initially should be 0
	count := m.GetRealTimeVisitorCount(5)
	if count != 0 {
		t.Errorf("expected 0 visitors initially, got %d", count)
	}

	// Insert some page views
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
	if err := m.saveSettings(m.settings); err != nil {
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
	if err := m.saveSettings(m.settings); err != nil {
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
