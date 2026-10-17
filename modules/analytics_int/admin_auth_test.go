// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// TestAdminRoutesRequireAUser fails when any admin route of this module,
// which opts its routes in for editors, stops checking the caller itself.
// The registry gate does not apply to opted-in modules, so the handlers are
// the only line of defence.
func TestAdminRoutesRequireAUser(t *testing.T) {
	m := New()
	router := chi.NewRouter()
	m.RegisterAdminRoutes(router)
	for _, route := range [][2]string{
		{http.MethodGet, "/internal-analytics"},
		{http.MethodGet, "/internal-analytics/report"},
		{http.MethodGet, "/internal-analytics/api/stats"},
		{http.MethodGet, "/internal-analytics/api/realtime"},
		{http.MethodPost, "/internal-analytics/settings"},
		{http.MethodPost, "/internal-analytics/aggregate"},
	} {
		w := httptest.NewRecorder()
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("%s %s reached module code without a user: %v", route[0], route[1], recovered)
				}
			}()
			router.ServeHTTP(w, httptest.NewRequest(route[0], route[1], nil))
		}()
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a user: status %d, want 401", route[0], route[1], w.Code)
		}
	}
}

// TestSettingsAndAggregationRequireAdmin fails when an editor can change the
// tracking settings or start a full aggregation. Editors keep the statistics
// pages; the module's editor opt-in must not extend to its write actions
// (security audit 2026-10-17, L-3).
func TestSettingsAndAggregationRequireAdmin(t *testing.T) {
	m := New()
	router := chi.NewRouter()
	m.RegisterAdminRoutes(router)
	editor := store.User{ID: 2, Email: "editor@test.com", Role: model.RoleEditor}
	for _, path := range []string{"/internal-analytics/settings", "/internal-analytics/aggregate"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, editor))
		w := httptest.NewRecorder()
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("POST %s reached module code as an editor: %v", path, recovered)
				}
			}()
			router.ServeHTTP(w, req)
		}()
		if w.Code != http.StatusForbidden {
			t.Errorf("POST %s as an editor: status %d, want 403", path, w.Code)
		}
	}
}

// TestDashboardShowsSettingsToAdminsOnly checks that editors are not offered
// forms whose actions return 403 to them.
func TestDashboardShowsSettingsToAdminsOnly(t *testing.T) {
	for _, tt := range []struct {
		role string
		want bool
	}{
		{model.RoleAdmin, true},
		{model.RoleEditor, false},
	} {
		pc := &adminviews.PageContext{User: adminviews.UserInfo{Role: tt.role}}
		var buf bytes.Buffer
		if err := AnalyticsIntPage(pc, AnalyticsIntViewData{}).Render(context.Background(), &buf); err != nil {
			t.Fatalf("rendering as %s: %v", tt.role, err)
		}
		for _, action := range []string{`action="/admin/internal-analytics/settings"`, `action="/admin/internal-analytics/aggregate"`} {
			if got := strings.Contains(buf.String(), action); got != tt.want {
				t.Errorf("%s sees %s = %v, want %v", tt.role, action, got, tt.want)
			}
		}
	}
}
