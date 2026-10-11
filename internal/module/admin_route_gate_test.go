// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// gateModule registers one admin route that answers 204 when reached.
type gateModule struct{ BaseModule }

func (m *gateModule) RegisterAdminRoutes(r chi.Router) {
	r.Get("/"+m.Name(), func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

// editorGateModule opts its admin routes in for editors.
type editorGateModule struct {
	gateModule
	allow bool
}

func (m *editorGateModule) EditorAdminRoutes() bool { return m.allow }

func adminRouteRequest(router http.Handler, path string, role string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if role != "" {
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{ID: 7, Email: role + "@example.com", Role: role}))
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestAdminRouteAllGatesEditorsUnlessOptedIn fails when the registry stops
// applying the admin-only gate to module admin routes, or applies it to a
// module that deliberately opted in for editors. A module whose author forgot
// RequireAdmin must be closed by default (finding M-1, 2026-10-11).
func TestAdminRouteAllGatesEditorsUnlessOptedIn(t *testing.T) {
	r := NewRegistry(testutil.TestLoggerSilent())
	plain := &gateModule{BaseModule: NewBaseModule("plain", "1.0.0", "no interface")}
	optedIn := &editorGateModule{gateModule: gateModule{BaseModule: NewBaseModule("opted-in", "1.0.0", "editors allowed")}, allow: true}
	optedOut := &editorGateModule{gateModule: gateModule{BaseModule: NewBaseModule("opted-out", "1.0.0", "editors refused")}, allow: false}
	for _, m := range []Module{plain, optedIn, optedOut} {
		if err := r.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	router := chi.NewRouter()
	r.AdminRouteAll(router)

	for _, tc := range []struct {
		name     string
		path     string
		role     string
		wantCode int
		wantBody string
	}{
		{"editor denied on plain module", "/plain", "editor", http.StatusForbidden, "insufficient permissions"},
		{"editor denied on opted-out module", "/opted-out", "editor", http.StatusForbidden, "insufficient permissions"},
		{"editor allowed on opted-in module", "/opted-in", "editor", http.StatusNoContent, ""},
		{"admin allowed on plain module", "/plain", "admin", http.StatusNoContent, ""},
		{"admin allowed on opted-in module", "/opted-in", "admin", http.StatusNoContent, ""},
		{"anonymous redirected on plain module", "/plain", "", http.StatusSeeOther, ""},
	} {
		w := adminRouteRequest(router, tc.path, tc.role)
		if w.Code != tc.wantCode {
			t.Errorf("%s: status = %d, want %d (body %q)", tc.name, w.Code, tc.wantCode, w.Body.String())
		}
		if tc.wantBody != "" && !strings.Contains(w.Body.String(), tc.wantBody) {
			t.Errorf("%s: body = %q, want it to contain %q", tc.name, w.Body.String(), tc.wantBody)
		}
	}

	if AllowsEditors(plain) || AllowsEditors(optedOut) || !AllowsEditors(optedIn) {
		t.Error("AllowsEditors must mirror the EditorAccessible opt-in")
	}
}
