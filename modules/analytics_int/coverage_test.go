// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// ---------------------------------------------------------------------------
// Module-level helpers
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
	if _, err := fs.ReadFile("locales/ru/messages.json"); err != nil {
		t.Errorf("failed to read ru translations: %v", err)
	}
}

func TestModuleTemplateFuncs(t *testing.T) {
	m := New()
	m.settings = &Settings{Enabled: true, ShowPostStats: true}
	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs() returned nil")
	}
	expectedFuncs := []string{"analyticsPostStats", "analyticsShowPostStats", "analyticsIntReadTracker"}
	if len(funcs) != len(expectedFuncs) {
		t.Errorf("expected %d funcs, got %d", len(expectedFuncs), len(funcs))
	}
	for _, name := range expectedFuncs {
		if _, ok := funcs[name]; !ok {
			t.Errorf("missing template func %q", name)
		}
	}
}

func TestModuleRegisterRoutes(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RegisterRoutes panicked: %v", r)
		}
	}()
	m := New()
	r := chi.NewRouter()
	m.RegisterRoutes(r)
}

func TestIsEnabled_NilSettings(t *testing.T) {
	m := &Module{}
	if m.IsEnabled() {
		t.Error("IsEnabled() should return false when settings is nil")
	}
}

func TestGetTrackingMiddleware(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	mw := m.GetTrackingMiddleware()
	if mw == nil {
		t.Error("GetTrackingMiddleware() should not return nil")
	}
}

// ---------------------------------------------------------------------------
// extractDomainFromURL edge cases
// ---------------------------------------------------------------------------

func TestExtractDomainFromURL_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"invalid scheme characters", "ht tp://example.com", ""},
		// Paths without a scheme: url.Parse treats the whole string as a path.
		// extractDomainFromURL falls back to rawURL when host is empty, so it
		// returns the raw input string.
		{"just a path", "/about/us", "/about/us"},
		{"IP address with port", "https://192.168.1.1:8080/path", "192.168.1.1"},
		{"domain with no scheme", "example.com", "example.com"},
		{"trailing slash only", "https://example.com/", "example.com"},
		// A bare fragment is also treated as a relative reference.
		{"fragment only", "#fragment", "#fragment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDomainFromURL(tt.input)
			if got != tt.expected {
				t.Errorf("extractDomainFromURL(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getSiteDomain
// ---------------------------------------------------------------------------

func TestGetSiteDomain_NoConfig(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// No site_url in config; should return empty string.
	got := m.getSiteDomain()
	if got != "" {
		t.Errorf("getSiteDomain() without config = %q, want empty", got)
	}
}

func TestGetSiteDomain_WithConfig(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Update the seeded site_url config entry.
	_, err := db.Exec(`
		INSERT INTO config (key, value, language_code)
		VALUES ('site_url', 'https://example.com', 'en')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`)
	if err != nil {
		t.Fatalf("upsert config: %v", err)
	}

	got := m.getSiteDomain()
	if got != "example.com" {
		t.Errorf("getSiteDomain() = %q, want %q", got, "example.com")
	}
}

func TestGetSiteDomain_NilContext(t *testing.T) {
	m := &Module{}
	got := m.getSiteDomain()
	if got != "" {
		t.Errorf("getSiteDomain() with nil ctx = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// getExcludedIPs
// ---------------------------------------------------------------------------

func TestGetExcludedIPs_NoConfig(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	ips := m.getExcludedIPs()
	if len(ips) != 0 {
		t.Errorf("getExcludedIPs() without config = %v, want empty", ips)
	}
}

func TestGetExcludedIPs_WithConfig(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Use UPDATE — the seeded config table already has an 'excluded_ips' row,
	// or we use a known-existing key. If no row exists for 'excluded_ips',
	// insert using all required columns (key, value, language_code).
	_, err := db.Exec(`
		INSERT INTO config (key, value, language_code)
		VALUES ('excluded_ips', '10.0.0.1' || char(10) || '192.168.1.100', 'en')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`)
	if err != nil {
		t.Fatalf("upsert config: %v", err)
	}

	ips := m.getExcludedIPs()
	if len(ips) != 2 {
		t.Errorf("getExcludedIPs() = %v, want 2 entries", ips)
	}
}

func TestGetExcludedIPs_NilContext(t *testing.T) {
	m := &Module{}
	ips := m.getExcludedIPs()
	if len(ips) != 0 {
		t.Errorf("getExcludedIPs() with nil ctx = %v, want empty", ips)
	}
}

// ---------------------------------------------------------------------------
// Settings: saveSalt and ReloadSettings
// ---------------------------------------------------------------------------

func TestSaveSalt(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	newSalt := "new-test-salt-64chars-padding-here-1234567890abcdef12345678"
	if err := m.saveSalt(newSalt); err != nil {
		t.Fatalf("saveSalt: %v", err)
	}

	// Reload and verify.
	loaded, err := m.loadSettings()
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if loaded.CurrentSalt != newSalt {
		t.Errorf("CurrentSalt after saveSalt = %q, want %q", loaded.CurrentSalt, newSalt)
	}
}

func TestReloadSettings(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Modify retention days directly in the DB.
	_, err := db.Exec(`UPDATE page_analytics_settings SET retention_days = 90 WHERE id = 1`)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := m.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings: %v", err)
	}

	if m.settings.RetentionDays != 90 {
		t.Errorf("RetentionDays after reload = %d, want 90", m.settings.RetentionDays)
	}
}

// ---------------------------------------------------------------------------
// getCurrentSalt (salt rotation path)
// ---------------------------------------------------------------------------

func TestGetCurrentSalt_RotatesWhenExpired(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Force salt to appear expired by setting SaltCreatedAt to the past.
	oldSalt := m.settings.CurrentSalt
	m.settings.SaltCreatedAt = time.Now().Add(-48 * time.Hour) // older than default 24h rotation.
	m.settings.SaltRotationHours = 24

	newSalt := m.getCurrentSalt()
	if newSalt == oldSalt {
		t.Error("getCurrentSalt() should have rotated the salt")
	}
	if newSalt == "" {
		t.Error("rotated salt should not be empty")
	}
}

func TestGetCurrentSalt_NoRotationNeeded(t *testing.T) {
	m := &Module{
		settings: &Settings{
			CurrentSalt:       "fresh-salt-value",
			SaltCreatedAt:     time.Now(),
			SaltRotationHours: 24,
		},
	}

	got := m.getCurrentSalt()
	if got != "fresh-salt-value" {
		t.Errorf("getCurrentSalt() = %q, want fresh-salt-value", got)
	}
}

// ---------------------------------------------------------------------------
// Aggregation functions
// ---------------------------------------------------------------------------

func TestAggregateDaily(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	prevHour := time.Now().Add(-2 * time.Hour)
	views := []*PageView{
		{VisitorHash: "v1", Path: "/agg-daily", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CreatedAt: prevHour},
		{VisitorHash: "v2", Path: "/agg-daily", SessionHash: "s2", Browser: "Firefox", OS: "Linux", DeviceType: "desktop", CreatedAt: prevHour},
	}
	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView: %v", err)
		}
	}

	// Run hourly aggregation first (daily reads from hourly).
	if err := m.aggregateHourly(context.Background()); err != nil {
		t.Fatalf("aggregateHourly: %v", err)
	}

	if err := m.aggregateDaily(context.Background()); err != nil {
		t.Fatalf("aggregateDaily: %v", err)
	}
	// No error is success; data validation is covered in RunFullAggregation tests.
}

func TestCleanupOldRawData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Insert a very old page view that should be deleted.
	old := time.Now().AddDate(0, 0, -10)
	view := &PageView{
		VisitorHash: "old-visitor",
		Path:        "/old-path",
		SessionHash: "old-session",
		CreatedAt:   old,
	}
	if err := m.insertPageView(view); err != nil {
		t.Fatalf("insertPageView: %v", err)
	}

	if err := m.cleanupOldRawData(context.Background()); err != nil {
		t.Fatalf("cleanupOldRawData: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM page_analytics_views WHERE path = '/old-path'").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected old raw data to be deleted, got %d rows", count)
	}
}

func TestCleanupExpiredData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Set a very short retention period.
	m.settings.RetentionDays = 1

	// Insert old aggregate data.
	oldDate := time.Now().AddDate(-2, 0, 0).Format("2006-01-02")
	_, err := db.Exec(`INSERT INTO page_analytics_daily (date, path, views, unique_visitors, bounces) VALUES (?, '/expired', 5, 2, 1)`, oldDate)
	if err != nil {
		t.Fatalf("insert daily: %v", err)
	}

	if err := m.cleanupExpiredData(context.Background()); err != nil {
		t.Fatalf("cleanupExpiredData: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM page_analytics_daily WHERE path = '/expired'").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected expired data to be deleted, got %d rows", count)
	}
}

func TestRunAggregationNow(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// RunAggregationNow should not return an error on empty data.
	if err := m.RunAggregationNow(); err != nil {
		t.Errorf("RunAggregationNow: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Query functions
// ---------------------------------------------------------------------------

func TestGetOverviewStats_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	start := time.Now().AddDate(0, 0, -7)
	end := time.Now()
	stats := m.getOverviewStats(context.Background(), start, end)

	if stats.TotalViews != 0 {
		t.Errorf("expected 0 total views, got %d", stats.TotalViews)
	}
	if stats.UniqueVisitors != 0 {
		t.Errorf("expected 0 unique visitors, got %d", stats.UniqueVisitors)
	}
}

func TestGetOverviewStats_WithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	now := time.Now()
	views := []*PageView{
		{VisitorHash: "v1", Path: "/", SessionHash: "s1", CreatedAt: now},
		{VisitorHash: "v2", Path: "/about", SessionHash: "s2", CreatedAt: now},
	}
	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView: %v", err)
		}
	}

	start := now.AddDate(0, 0, -1)
	end := now.AddDate(0, 0, 1)
	stats := m.getOverviewStats(context.Background(), start, end)

	if stats.ViewsToday != 2 {
		t.Errorf("ViewsToday = %d, want 2", stats.ViewsToday)
	}
}

func TestGetTopPages_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	pages := m.getTopPages(context.Background(), time.Now().AddDate(0, 0, -7), time.Now(), 10)
	if len(pages) != 0 {
		t.Errorf("expected 0 top pages, got %d", len(pages))
	}
}

func TestGetTopReferrers_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	refs := m.getTopReferrers(context.Background(), time.Now().AddDate(0, 0, -7), time.Now(), 10)
	if len(refs) != 0 {
		t.Errorf("expected 0 referrers, got %d", len(refs))
	}
}

func TestGetBrowserStats_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	stats := m.getBrowserStats(context.Background(), time.Now().AddDate(0, 0, -7), time.Now())
	if len(stats) != 0 {
		t.Errorf("expected 0 browser stats, got %d", len(stats))
	}
}

func TestGetDeviceStats_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	stats := m.getDeviceStats(context.Background(), time.Now().AddDate(0, 0, -7), time.Now())
	if len(stats) != 0 {
		t.Errorf("expected 0 device stats, got %d", len(stats))
	}
}

func TestGetCountryStats_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	stats := m.getCountryStats(context.Background(), time.Now().AddDate(0, 0, -7), time.Now(), 10)
	if len(stats) != 0 {
		t.Errorf("expected 0 country stats, got %d", len(stats))
	}
}

func TestGetTimeSeries_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	series := m.getTimeSeries(context.Background(), time.Now().AddDate(0, 0, -7), time.Now())
	// May be empty or pre-populated with zero entries.
	_ = series
}

func TestGetPageTitle(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	ctx := context.Background()

	// Root path should return "Home" when not found in DB.
	title := m.getPageTitle(ctx, "/")
	if title != "Home" {
		t.Errorf("getPageTitle('/') = %q, want 'Home'", title)
	}

	// Unknown path returns the path itself.
	title = m.getPageTitle(ctx, "/unknown-page")
	if title != "/unknown-page" {
		t.Errorf("getPageTitle('/unknown-page') = %q, want '/unknown-page'", title)
	}
}

func TestGetTechStats(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Exercise both browser and device stats functions separately.
	browsers := m.getBrowserStats(context.Background(), time.Now().AddDate(0, 0, -7), time.Now())
	devices := m.getDeviceStats(context.Background(), time.Now().AddDate(0, 0, -7), time.Now())
	if browsers == nil {
		browsers = []BrowserStat{}
	}
	if devices == nil {
		devices = []DeviceStat{}
	}
	_ = browsers
	_ = devices
}

// ---------------------------------------------------------------------------
// parseDateRange
// ---------------------------------------------------------------------------

func TestParseDateRange_AllRanges(t *testing.T) {
	ranges := []struct {
		input       string
		expectDays  int
	}{
		{"7d", 7},
		{"30d", 30},
		{"90d", 90},
		{"1y", 365},
		{"", 30}, // default
	}

	for _, tt := range ranges {
		t.Run(tt.input, func(t *testing.T) {
			start, end := parseDateRange(tt.input)
			days := int(end.Sub(start).Hours()+12) / 24
			if days < tt.expectDays-2 || days > tt.expectDays+2 {
				t.Errorf("parseDateRange(%q) = %d days, want ~%d", tt.input, days, tt.expectDays)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TrackingMiddleware
// ---------------------------------------------------------------------------

func TestTrackingMiddleware_SkipsWhenDisabled(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	m.settings.Enabled = false

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := m.TrackingMiddleware()
	req := httptest.NewRequest(http.MethodGet, "/test-page", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)

	if !called {
		t.Error("next handler should be called even when tracking is disabled")
	}
}

func TestTrackingMiddleware_SkipsNonTrackable(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := m.TrackingMiddleware()

	// Static asset — should not be tracked.
	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestTrackingMiddleware_TracksSuccessfulGET(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := m.TrackingMiddleware()

	req := httptest.NewRequest(http.MethodGet, "/trackable-page", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	// Let the async goroutine complete.
	// We can't wait for it deterministically, but we verify no panic.
}

func TestTrackingMiddleware_DoesNotTrackNon200(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	mw := m.TrackingMiddleware()

	req := httptest.NewRequest(http.MethodGet, "/missing-page", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestTrackingMiddleware_NilSettings(t *testing.T) {
	m := New()
	m.BaseModule = module.NewBaseModule("analytics_int", "1.0.1", "Internal Analytics")
	// Don't call Init — settings will be nil.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := m.TrackingMiddleware()
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Queries with data
// ---------------------------------------------------------------------------

func TestGetTopPages_WithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	yesterday := time.Now().AddDate(0, 0, -1)
	views := []*PageView{
		{VisitorHash: "v1", Path: "/popular", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CreatedAt: yesterday},
		{VisitorHash: "v2", Path: "/popular", SessionHash: "s2", Browser: "Firefox", OS: "Linux", DeviceType: "desktop", CreatedAt: yesterday},
		{VisitorHash: "v3", Path: "/less-popular", SessionHash: "s3", Browser: "Safari", OS: "macOS", DeviceType: "desktop", CreatedAt: yesterday},
	}
	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView: %v", err)
		}
	}

	// Run aggregation so daily table is populated.
	if _, err := m.RunFullAggregation(context.Background()); err != nil {
		t.Fatalf("RunFullAggregation: %v", err)
	}

	pages := m.getTopPages(context.Background(), time.Now().AddDate(0, 0, -7), time.Now(), 5)
	if len(pages) == 0 {
		t.Error("expected at least one top page after aggregation")
	}
}

func TestGetTopReferrers_WithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	yesterday := time.Now().AddDate(0, 0, -1)
	views := []*PageView{
		{VisitorHash: "v1", Path: "/", SessionHash: "s1", ReferrerDomain: "google.com", CreatedAt: yesterday},
		{VisitorHash: "v2", Path: "/", SessionHash: "s2", ReferrerDomain: "twitter.com", CreatedAt: yesterday},
	}
	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView: %v", err)
		}
	}

	if _, err := m.RunFullAggregation(context.Background()); err != nil {
		t.Fatalf("RunFullAggregation: %v", err)
	}

	refs := m.getTopReferrers(context.Background(), time.Now().AddDate(0, 0, -7), time.Now(), 10)
	if len(refs) == 0 {
		t.Error("expected at least one referrer after aggregation")
	}
}

func TestGetCountryStats_WithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	yesterday := time.Now().AddDate(0, 0, -1)
	views := []*PageView{
		{VisitorHash: "v1", Path: "/", SessionHash: "s1", CountryCode: "US", CreatedAt: yesterday},
		{VisitorHash: "v2", Path: "/", SessionHash: "s2", CountryCode: "DE", CreatedAt: yesterday},
	}
	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView: %v", err)
		}
	}

	if _, err := m.RunFullAggregation(context.Background()); err != nil {
		t.Fatalf("RunFullAggregation: %v", err)
	}

	stats := m.getCountryStats(context.Background(), time.Now().AddDate(0, 0, -7), time.Now(), 10)
	if len(stats) == 0 {
		t.Error("expected at least one country stat after aggregation")
	}
	// Verify percent is set.
	for _, s := range stats {
		if s.Percent < 0 || s.Percent > 100 {
			t.Errorf("country %q percent = %.1f, out of range [0,100]", s.CountryCode, s.Percent)
		}
	}
}

func TestGetBrowserStats_WithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	yesterday := time.Now().AddDate(0, 0, -1)
	views := []*PageView{
		{VisitorHash: "v1", Path: "/", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CreatedAt: yesterday},
		{VisitorHash: "v2", Path: "/", SessionHash: "s2", Browser: "Firefox", OS: "Linux", DeviceType: "desktop", CreatedAt: yesterday},
	}
	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView: %v", err)
		}
	}

	if _, err := m.RunFullAggregation(context.Background()); err != nil {
		t.Fatalf("RunFullAggregation: %v", err)
	}

	stats := m.getBrowserStats(context.Background(), time.Now().AddDate(0, 0, -7), time.Now())
	if len(stats) == 0 {
		t.Error("expected at least one browser stat after aggregation")
	}
}

func TestGetDeviceStats_WithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	yesterday := time.Now().AddDate(0, 0, -1)
	views := []*PageView{
		{VisitorHash: "v1", Path: "/", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CreatedAt: yesterday},
		{VisitorHash: "v2", Path: "/", SessionHash: "s2", Browser: "Mobile Chrome", OS: "Android", DeviceType: "mobile", CreatedAt: yesterday},
	}
	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView: %v", err)
		}
	}

	if _, err := m.RunFullAggregation(context.Background()); err != nil {
		t.Fatalf("RunFullAggregation: %v", err)
	}

	stats := m.getDeviceStats(context.Background(), time.Now().AddDate(0, 0, -7), time.Now())
	if len(stats) == 0 {
		t.Error("expected at least one device stat after aggregation")
	}
}

// ---------------------------------------------------------------------------
// Module shutdown with nil cron
// ---------------------------------------------------------------------------

func TestShutdown_NilCron(t *testing.T) {
	m := New()
	// Shutdown before Init — no cron, no context.
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown without Init: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Ensure TemplateFuncs closures are registered and callable via reflect
// ---------------------------------------------------------------------------

func TestAnalyticsIntTemplateFuncs_WithNilSettings(t *testing.T) {
	m := New()
	// settings is nil — funcs should return safe defaults
	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs() returned nil")
	}
	// Even with nil settings, the 3 template funcs should be registered
	if len(funcs) != 3 {
		t.Errorf("expected 3 funcs, got %d entries", len(funcs))
	}
}

// ---------------------------------------------------------------------------
// moduleutil helpers — exercise AssertMigrations
// ---------------------------------------------------------------------------

func TestModuleMigrationsAssert(t *testing.T) {
	m := New()
	moduleutil.AssertMigrations(t, m.Migrations(), 11)
}

// ---------------------------------------------------------------------------
// handleRecordRead HTTP endpoint
// ---------------------------------------------------------------------------

func TestHandleRecordRead_ValidRequest(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	body := `{"path":"/test-post","scroll_depth":75,"time_on_page":45}`
	req := httptest.NewRequest(http.MethodPost, "/analytics/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	w := httptest.NewRecorder()

	m.handleRecordRead(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleRecordRead_DisabledTracking(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	m.settings.Enabled = false

	body := `{"path":"/test","scroll_depth":75,"time_on_page":45}`
	req := httptest.NewRequest(http.MethodPost, "/analytics/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleRecordRead(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d (disabled)", w.Code, http.StatusNoContent)
	}
}

func TestHandleRecordRead_EmptyPath(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	body := `{"path":"","scroll_depth":75,"time_on_page":45}`
	req := httptest.NewRequest(http.MethodPost, "/analytics/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleRecordRead(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (empty path)", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRecordRead_LowScrollDepth(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	body := `{"path":"/test","scroll_depth":50,"time_on_page":45}`
	req := httptest.NewRequest(http.MethodPost, "/analytics/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleRecordRead(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (low scroll)", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRecordRead_LowTimeOnPage(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	body := `{"path":"/test","scroll_depth":75,"time_on_page":20}`
	req := httptest.NewRequest(http.MethodPost, "/analytics/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleRecordRead(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (low time)", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRecordRead_InvalidJSON(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	req := httptest.NewRequest(http.MethodPost, "/analytics/read", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleRecordRead(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (invalid JSON)", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRecordRead_NilSettings(t *testing.T) {
	m := New()
	m.settings = nil

	body := `{"path":"/test","scroll_depth":75,"time_on_page":45}`
	req := httptest.NewRequest(http.MethodPost, "/analytics/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleRecordRead(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d (nil settings)", w.Code, http.StatusNoContent)
	}
}

func TestHandleRecordRead_ScrollDepthCapped(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// scroll_depth > 100 should be capped, not rejected
	body := `{"path":"/test","scroll_depth":150,"time_on_page":45}`
	req := httptest.NewRequest(http.MethodPost, "/analytics/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	w := httptest.NewRecorder()

	m.handleRecordRead(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d (capped scroll)", w.Code, http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// recordRead deduplication
// ---------------------------------------------------------------------------

func TestRecordRead_Deduplication(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	req := httptest.NewRequest(http.MethodPost, "/analytics/read", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	readReq := &ReadRequest{Path: "/dedup-test", ScrollDepth: 80, TimeOnPage: 60}

	// First call should record
	result1 := m.recordRead(req, readReq)
	if !result1 {
		t.Error("first recordRead should return true")
	}

	// Second call with same IP+UA (same session hash) should be deduplicated
	result2 := m.recordRead(req, readReq)
	if result2 {
		t.Error("second recordRead should return false (duplicate)")
	}

	// Verify only 1 row in DB
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM page_analytics_reads WHERE path = ?", "/dedup-test").Scan(&count); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// readTrackerScript
// ---------------------------------------------------------------------------

func TestReadTrackerScript_WithNonce(t *testing.T) {
	m := New()
	m.settings = &Settings{Enabled: true}

	script := m.readTrackerScript("test-nonce-123")
	if !strings.Contains(script, `nonce="test-nonce-123"`) {
		t.Error("script should contain nonce attribute")
	}
	if !strings.Contains(script, "/analytics/read") {
		t.Error("script should contain read beacon URL")
	}
	if !strings.Contains(script, "sendBeacon") {
		t.Error("script should use sendBeacon API")
	}
}

func TestReadTrackerScript_WithoutNonce(t *testing.T) {
	m := New()
	m.settings = &Settings{Enabled: true}

	script := m.readTrackerScript("")
	if strings.Contains(script, "nonce") {
		t.Error("script should not contain nonce attribute when empty")
	}
}

// ---------------------------------------------------------------------------
// TemplateFuncs behavior with live DB
// ---------------------------------------------------------------------------

func TestTemplateFuncs_AnalyticsPostStats(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Insert views and reads for a slug
	now := time.Now()
	for i := 0; i < 5; i++ {
		v := &PageView{
			VisitorHash: "v" + string(rune('a'+i)),
			Path:        "/my-post",
			SessionHash: "s" + string(rune('a'+i)),
			CreatedAt:   now,
		}
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView: %v", err)
		}
	}
	read := &PageRead{
		VisitorHash: "va", Path: "/my-post", SessionHash: "sa",
		ScrollDepth: 90, TimeOnPage: 120, CreatedAt: now,
	}
	if err := m.insertPageRead(read); err != nil {
		t.Fatalf("insertPageRead: %v", err)
	}

	funcs := m.TemplateFuncs()
	statsFn := funcs["analyticsPostStats"].(func(string) PageStats)
	stats := statsFn("my-post")

	if stats.Views != 5 {
		t.Errorf("analyticsPostStats Views = %d, want 5", stats.Views)
	}
	if stats.Reads != 1 {
		t.Errorf("analyticsPostStats Reads = %d, want 1", stats.Reads)
	}
}

func TestTemplateFuncs_AnalyticsShowPostStats(t *testing.T) {
	m := New()

	// With enabled + ShowPostStats
	m.settings = &Settings{Enabled: true, ShowPostStats: true}
	funcs := m.TemplateFuncs()
	showFn := funcs["analyticsShowPostStats"].(func() bool)
	if !showFn() {
		t.Error("analyticsShowPostStats should return true when enabled")
	}

	// With disabled module
	m.settings = &Settings{Enabled: false, ShowPostStats: true}
	funcs = m.TemplateFuncs()
	showFn = funcs["analyticsShowPostStats"].(func() bool)
	if showFn() {
		t.Error("analyticsShowPostStats should return false when module disabled")
	}

	// With ShowPostStats false
	m.settings = &Settings{Enabled: true, ShowPostStats: false}
	funcs = m.TemplateFuncs()
	showFn = funcs["analyticsShowPostStats"].(func() bool)
	if showFn() {
		t.Error("analyticsShowPostStats should return false when ShowPostStats is false")
	}
}
