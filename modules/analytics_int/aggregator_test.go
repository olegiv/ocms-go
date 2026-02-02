// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestRunFullAggregation_NoData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Run aggregation with no data
	count, err := m.RunFullAggregation(context.Background())
	if err != nil {
		t.Fatalf("RunFullAggregation failed: %v", err)
	}

	if count != 0 {
		t.Errorf("expected 0 dates processed, got %d", count)
	}
}

func TestRunFullAggregation_WithHistoricalData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Insert page views for past dates
	yesterday := time.Now().AddDate(0, 0, -1)
	twoDaysAgo := time.Now().AddDate(0, 0, -2)

	views := []*PageView{
		// Yesterday's views
		{VisitorHash: "v1", Path: "/page1", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", ReferrerDomain: "google.com", CountryCode: "US", CreatedAt: yesterday},
		{VisitorHash: "v2", Path: "/page1", SessionHash: "s2", Browser: "Firefox", OS: "Linux", DeviceType: "desktop", ReferrerDomain: "", CountryCode: "DE", CreatedAt: yesterday},
		{VisitorHash: "v1", Path: "/page2", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", ReferrerDomain: "", CountryCode: "US", CreatedAt: yesterday},
		// Two days ago views
		{VisitorHash: "v3", Path: "/page1", SessionHash: "s3", Browser: "Safari", OS: "macOS", DeviceType: "mobile", ReferrerDomain: "twitter.com", CountryCode: "GB", CreatedAt: twoDaysAgo},
	}

	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Run aggregation
	count, err := m.RunFullAggregation(context.Background())
	if err != nil {
		t.Fatalf("RunFullAggregation failed: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 dates processed, got %d", count)
	}

	// Verify page_analytics_daily was populated
	var dailyCount int
	err = db.QueryRow("SELECT COUNT(*) FROM page_analytics_daily").Scan(&dailyCount)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if dailyCount == 0 {
		t.Error("expected page_analytics_daily to have data")
	}

	// Verify page_analytics_geo was populated
	var geoCount int
	err = db.QueryRow("SELECT COUNT(*) FROM page_analytics_geo").Scan(&geoCount)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if geoCount == 0 {
		t.Error("expected page_analytics_geo to have data")
	}

	// Verify page_analytics_tech was populated
	var techCount int
	err = db.QueryRow("SELECT COUNT(*) FROM page_analytics_tech").Scan(&techCount)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if techCount == 0 {
		t.Error("expected page_analytics_tech to have data")
	}

	// Verify page_analytics_referrers was populated (only non-empty referrers)
	var refCount int
	err = db.QueryRow("SELECT COUNT(*) FROM page_analytics_referrers").Scan(&refCount)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if refCount == 0 {
		t.Error("expected page_analytics_referrers to have data")
	}
}

func TestRunFullAggregation_ExcludesToday(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Insert page views for today only
	now := time.Now()
	views := []*PageView{
		{VisitorHash: "v1", Path: "/today", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CreatedAt: now},
	}

	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Run aggregation - should not process today's data
	count, err := m.RunFullAggregation(context.Background())
	if err != nil {
		t.Fatalf("RunFullAggregation failed: %v", err)
	}

	if count != 0 {
		t.Errorf("expected 0 dates processed (today excluded), got %d", count)
	}

	// Verify page_analytics_daily is empty
	var dailyCount int
	err = db.QueryRow("SELECT COUNT(*) FROM page_analytics_daily").Scan(&dailyCount)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if dailyCount != 0 {
		t.Errorf("expected page_analytics_daily to be empty, got %d rows", dailyCount)
	}
}

func TestRunFullAggregation_CorrectCounts(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	yesterday := time.Now().AddDate(0, 0, -1)
	yesterdayStr := yesterday.Format("2006-01-02")

	// Insert 3 views from 2 unique visitors on the same path
	views := []*PageView{
		{VisitorHash: "visitor1", Path: "/test", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CountryCode: "US", CreatedAt: yesterday},
		{VisitorHash: "visitor1", Path: "/test", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CountryCode: "US", CreatedAt: yesterday},
		{VisitorHash: "visitor2", Path: "/test", SessionHash: "s2", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CountryCode: "US", CreatedAt: yesterday},
	}

	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Run aggregation
	_, err := m.RunFullAggregation(context.Background())
	if err != nil {
		t.Fatalf("RunFullAggregation failed: %v", err)
	}

	// Verify counts in page_analytics_daily
	var totalViews, uniqueVisitors int
	err = db.QueryRow(`
		SELECT views, unique_visitors FROM page_analytics_daily
		WHERE date = ? AND path = ?
	`, yesterdayStr, "/test").Scan(&totalViews, &uniqueVisitors)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if totalViews != 3 {
		t.Errorf("expected 3 total views, got %d", totalViews)
	}
	if uniqueVisitors != 2 {
		t.Errorf("expected 2 unique visitors, got %d", uniqueVisitors)
	}
}

func TestRunFullAggregation_Idempotent(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	yesterday := time.Now().AddDate(0, 0, -1)

	views := []*PageView{
		{VisitorHash: "v1", Path: "/page", SessionHash: "s1", Browser: "Chrome", OS: "Windows", DeviceType: "desktop", CreatedAt: yesterday},
	}

	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Run aggregation twice
	_, err := m.RunFullAggregation(context.Background())
	if err != nil {
		t.Fatalf("first RunFullAggregation failed: %v", err)
	}

	_, err = m.RunFullAggregation(context.Background())
	if err != nil {
		t.Fatalf("second RunFullAggregation failed: %v", err)
	}

	// Verify only one row exists (INSERT OR REPLACE should not duplicate)
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM page_analytics_daily").Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after idempotent runs, got %d", count)
	}
}

func TestAggregateHourly(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Insert views in the previous hour
	now := time.Now()
	prevHour := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()-1, 30, 0, 0, now.Location())

	views := []*PageView{
		{VisitorHash: "v1", Path: "/hourly-test", SessionHash: "s1", CreatedAt: prevHour},
		{VisitorHash: "v2", Path: "/hourly-test", SessionHash: "s2", CreatedAt: prevHour},
	}

	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Run hourly aggregation
	err := m.aggregateHourly(context.Background())
	if err != nil {
		t.Fatalf("aggregateHourly failed: %v", err)
	}

	// Verify hourly stats
	var hourlyViews, hourlyUnique int
	err = db.QueryRow(`
		SELECT views, unique_visitors FROM page_analytics_hourly
		WHERE path = ?
	`, "/hourly-test").Scan(&hourlyViews, &hourlyUnique)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if hourlyViews != 2 {
		t.Errorf("expected 2 hourly views, got %d", hourlyViews)
	}
	if hourlyUnique != 2 {
		t.Errorf("expected 2 hourly unique visitors, got %d", hourlyUnique)
	}
}

func TestAggregateGeoStats(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	yesterday := time.Now().AddDate(0, 0, -1)
	dateStr := yesterday.Format("2006-01-02")

	views := []*PageView{
		{VisitorHash: "v1", Path: "/", CountryCode: "US", SessionHash: "s1", CreatedAt: yesterday},
		{VisitorHash: "v2", Path: "/", CountryCode: "US", SessionHash: "s2", CreatedAt: yesterday},
		{VisitorHash: "v3", Path: "/", CountryCode: "DE", SessionHash: "s3", CreatedAt: yesterday},
		{VisitorHash: "v4", Path: "/", CountryCode: "", SessionHash: "s4", CreatedAt: yesterday}, // Unknown
		{VisitorHash: "v5", Path: "/", CountryCode: "LOCAL", SessionHash: "s5", CreatedAt: yesterday},
	}

	for _, v := range views {
		if err := m.insertPageView(v); err != nil {
			t.Fatalf("insertPageView failed: %v", err)
		}
	}

	// Run geo aggregation
	err := m.aggregateGeoStats(context.Background(), dateStr)
	if err != nil {
		t.Fatalf("aggregateGeoStats failed: %v", err)
	}

	// Verify US stats
	var usViews int
	err = db.QueryRow(`
		SELECT views FROM page_analytics_geo
		WHERE date = ? AND country_code = 'US'
	`, dateStr).Scan(&usViews)
	if err != nil {
		t.Fatalf("query US failed: %v", err)
	}
	if usViews != 2 {
		t.Errorf("expected 2 US views, got %d", usViews)
	}

	// Verify Unknown stats (empty country_code becomes 'Unknown')
	var unknownViews int
	err = db.QueryRow(`
		SELECT views FROM page_analytics_geo
		WHERE date = ? AND country_code = 'Unknown'
	`, dateStr).Scan(&unknownViews)
	if err != nil {
		t.Fatalf("query Unknown failed: %v", err)
	}
	if unknownViews != 1 {
		t.Errorf("expected 1 Unknown view, got %d", unknownViews)
	}

	// Verify LOCAL stats
	var localViews int
	err = db.QueryRow(`
		SELECT views FROM page_analytics_geo
		WHERE date = ? AND country_code = 'LOCAL'
	`, dateStr).Scan(&localViews)
	if err != nil {
		t.Fatalf("query LOCAL failed: %v", err)
	}
	if localViews != 1 {
		t.Errorf("expected 1 LOCAL view, got %d", localViews)
	}
}
