// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
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
func testAuthorizedJSONHandler(t *testing.T, url string, handler http.HandlerFunc) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
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
func testUnauthorizedHandler(t *testing.T, url string, handler http.HandlerFunc) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
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
