// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/oauth"
)

type fakeOAuthEndpoints struct{}

func reached(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }

func (fakeOAuthEndpoints) Discovery(w http.ResponseWriter, r *http.Request) { reached(w, r) }
func (fakeOAuthEndpoints) ProtectedResourceMetadata() http.Handler          { return http.HandlerFunc(reached) }
func (fakeOAuthEndpoints) Token(w http.ResponseWriter, r *http.Request)     { reached(w, r) }
func (fakeOAuthEndpoints) Revoke(w http.ResponseWriter, r *http.Request)    { reached(w, r) }
func (fakeOAuthEndpoints) Register(w http.ResponseWriter, r *http.Request)  { reached(w, r) }

// TestOAuthRoutesAnswerOnlyWhileMCPIsActive checks that the OAuth server's
// routes, apart from token revocation, answer 404 while the MCP module is
// off and 503 when its state cannot be read: the module's endpoint is the
// only resource the server protects (2026-11-03 audit A-13).
//
// Bug state: mount any OAuth route outside the MCP gate in
// registerOAuthRoutes and this fails.
func TestOAuthRoutesAnswerOnlyWhileMCPIsActive(t *testing.T) {
	routes := []struct {
		method, path string
		gated        bool
	}{
		{http.MethodGet, "/.well-known/oauth-authorization-server", true},
		{http.MethodGet, oauth.MetadataPath, true},
		{http.MethodPost, "/oauth/token", true},
		{http.MethodPost, "/oauth/register", true},
		{http.MethodGet, "/oauth/authorize", true},
		{http.MethodPost, "/oauth/authorize", true},
		{http.MethodPost, "/oauth/revoke", false},
	}
	pass := func(next http.Handler) http.Handler { return next }
	for _, state := range []struct {
		name   string
		active bool
		err    error
		gated  int
	}{
		{"active", true, nil, http.StatusTeapot},
		{"inactive", false, nil, http.StatusNotFound},
		{"unreadable", false, errors.New("database is locked"), http.StatusServiceUnavailable},
	} {
		r := chi.NewRouter()
		registerOAuthRoutes(r, fakeOAuthEndpoints{}, reached, func(*http.Request) (bool, error) { return state.active, state.err }, pass, pass, pass)
		for _, route := range routes {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
			want := http.StatusTeapot
			if route.gated {
				want = state.gated
			}
			if w.Code != want {
				t.Errorf("%s: %s %s = %d, want %d", state.name, route.method, route.path, w.Code, want)
			}
		}
	}
}
