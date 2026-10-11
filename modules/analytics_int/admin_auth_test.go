// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
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
