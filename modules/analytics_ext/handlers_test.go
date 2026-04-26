// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_ext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// testModuleWithRenderer creates a module with a real (empty) render.Renderer.
// The renderer is not fully initialised (no templates) so templ-based handlers
// will panic, but redirect-based handlers work fine.
func testModuleWithRenderer(t *testing.T) (*Module, func()) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	ctx, _ := moduleutil.TestModuleContext(t, db)
	ctx.Render = &render.Renderer{}

	if err := m.Init(ctx); err != nil {
		cleanup()
		t.Fatalf("Init: %v", err)
	}
	return m, cleanup
}

// withUser adds a user to the request context.
func withUser(r *http.Request, u store.User) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.ContextKeyUser, u)
	return r.WithContext(ctx)
}

// ---------------------------------------------------------------------------
// handleSaveSettings — redirect-based paths (no template rendering needed)
// ---------------------------------------------------------------------------

func TestHandleSaveSettings_Unauthorized(t *testing.T) {
	m, cleanup := testModuleWithRenderer(t)
	defer cleanup()

	form := url.Values{}
	req := httptest.NewRequest(http.MethodPost, "/admin/external-analytics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No user in context — GetUser returns nil.
	w := httptest.NewRecorder()

	m.handleSaveSettings(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleSaveSettings_ValidationError_GA4NoID(t *testing.T) {
	m, cleanup := testModuleWithRenderer(t)
	defer cleanup()

	form := url.Values{
		"ga4_enabled":        {"1"},
		"ga4_measurement_id": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/external-analytics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, store.User{ID: 1, Email: "admin@example.com", Role: "admin"})

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	// Validation failure → redirect back.
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d (SeeOther redirect on validation error)", w.Code, http.StatusSeeOther)
	}
}

func TestHandleSaveSettings_ValidationError_GTMNoID(t *testing.T) {
	m, cleanup := testModuleWithRenderer(t)
	defer cleanup()

	form := url.Values{
		"gtm_enabled":      {"1"},
		"gtm_container_id": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/external-analytics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, store.User{ID: 1, Email: "admin@example.com", Role: "admin"})

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
}

func TestHandleSaveSettings_ValidationError_MatomoNoURL(t *testing.T) {
	m, cleanup := testModuleWithRenderer(t)
	defer cleanup()

	form := url.Values{
		"matomo_enabled": {"1"},
		"matomo_url":     {""},
		"matomo_site_id": {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/external-analytics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, store.User{ID: 1, Email: "admin@example.com", Role: "admin"})

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
}

func TestHandleSaveSettings_ValidationError_MatomoNoSiteID(t *testing.T) {
	m, cleanup := testModuleWithRenderer(t)
	defer cleanup()

	form := url.Values{
		"matomo_enabled": {"1"},
		"matomo_url":     {"https://matomo.example.com"},
		"matomo_site_id": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/external-analytics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, store.User{ID: 1, Email: "admin@example.com", Role: "admin"})

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
}

func TestHandleSaveSettings_Success(t *testing.T) {
	m, cleanup := testModuleWithRenderer(t)
	defer cleanup()

	form := url.Values{
		"ga4_enabled":        {"1"},
		"ga4_measurement_id": {"G-TESTSAVE01"},
		"gtm_enabled":        {""},
		"gtm_container_id":   {""},
		"matomo_enabled":     {""},
		"matomo_url":         {""},
		"matomo_site_id":     {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/external-analytics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, store.User{ID: 1, Email: "admin@example.com", Role: "admin"})

	w := httptest.NewRecorder()
	m.handleSaveSettings(w, req)

	// Successful save → redirect.
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d (SeeOther redirect on success)", w.Code, http.StatusSeeOther)
	}

	// Settings should be updated in memory.
	if !m.settings.GA4Enabled {
		t.Error("GA4Enabled should be true after save")
	}
	if m.settings.GA4MeasurementID != "G-TESTSAVE01" {
		t.Errorf("GA4MeasurementID = %q, want G-TESTSAVE01", m.settings.GA4MeasurementID)
	}
}
