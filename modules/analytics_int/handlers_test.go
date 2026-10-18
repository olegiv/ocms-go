// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestHandleRunAggregation_Unauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	req := httptest.NewRequest(http.MethodPost, "/admin/internal-analytics/aggregate", nil)
	rr := httptest.NewRecorder()

	m.handleRunAggregation(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestHandleRunAggregation_Authorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	// Insert some historical data
	yesterday := time.Now().AddDate(0, 0, -1)
	view := &PageView{
		VisitorHash: "v1",
		Path:        "/test",
		SessionHash: "s1",
		Browser:     "Chrome",
		OS:          "Windows",
		DeviceType:  "desktop",
		CreatedAt:   yesterday,
	}
	if err := m.insertPageView(view); err != nil {
		t.Fatalf("insertPageView failed: %v", err)
	}

	// Create request with user context
	req := httptest.NewRequest(http.MethodPost, "/admin/internal-analytics/aggregate", nil)
	user := store.User{ID: 1, Email: "admin@test.com", Role: "admin"}
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	m.handleRunAggregation(rr, req)

	// Should redirect after successful aggregation
	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected status %d, got %d", http.StatusSeeOther, rr.Code)
	}

	location := rr.Header().Get("Location")
	if location != "/admin/internal-analytics" {
		t.Errorf("expected redirect to /admin/internal-analytics, got %s", location)
	}

	// Verify data was aggregated
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM page_analytics_daily").Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count == 0 {
		t.Error("expected data in page_analytics_daily after aggregation")
	}
}

// testAuthorizedJSONHandler tests that a handler returns OK with JSON content-type when authorized.
func testAuthorizedJSONHandler(t *testing.T, target string, handler http.HandlerFunc) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	user := store.User{ID: 1, Email: "admin@test.com", Role: "admin"}
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

// testUnauthorizedHandler tests that a handler returns Unauthorized when no user context is set.
func testUnauthorizedHandler(t *testing.T, target string, handler http.HandlerFunc) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rr := httptest.NewRecorder()

	handler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestHandleAPIStats_Unauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	testUnauthorizedHandler(t, "/admin/internal-analytics/api/stats", m.handleAPIStats)
}

func TestHandleAPIStats_Authorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	testAuthorizedJSONHandler(t, "/admin/internal-analytics/api/stats?range=7d", m.handleAPIStats)
}

func TestHandleRealtime_Unauthorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	testUnauthorizedHandler(t, "/admin/internal-analytics/api/realtime", m.handleRealtime)
}

func TestHandleRealtime_Authorized(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	testAuthorizedJSONHandler(t, "/admin/internal-analytics/api/realtime", m.handleRealtime)
}

// TestSaveSettingsDuringTracking saves the settings form while the tracker
// filters requests. Run with -race: before the settings lock the form wrote
// the fields the tracker reads on every request (security audit 2026-10-17,
// L-3). Without -race it still checks that the last save wins.
func TestSaveSettingsDuringTracking(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	admin := store.User{ID: 1, Email: "admin@test.com", Role: model.RoleAdmin}
	const saves = 20
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range saves {
			form := url.Values{"enabled": {"1"}, "retention_days": {"90"}, "exclude_paths": {fmt.Sprintf("/private-%d", i)}}
			req := httptest.NewRequest(http.MethodPost, "/admin/internal-analytics/settings", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
			m.handleSaveSettings(httptest.NewRecorder(), req)
		}
	})
	wg.Go(func() {
		for range 10 * saves {
			_ = m.IsEnabled()
			_ = m.shouldTrack(httptest.NewRequest(http.MethodGet, "/about", nil))
		}
	})
	wg.Wait()

	settings := m.settingsSnapshot()
	if want := []string{fmt.Sprintf("/private-%d", saves-1)}; !slices.Equal(settings.ExcludePaths, want) {
		t.Errorf("ExcludePaths = %v, want %v", settings.ExcludePaths, want)
	}
	if settings.RetentionDays != 90 {
		t.Errorf("RetentionDays = %d, want 90", settings.RetentionDays)
	}
}
