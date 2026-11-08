// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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
// registered method/path pairs, apart from POST /oauth/revoke, answer 404
// while the MCP module is off and 503 when its state cannot be read. The
// module's endpoint is the only resource the server protects (2026-11-03
// audit A-13).
//
// Bug state: add a new route outside the MCP gate in registerOAuthRoutes,
// or move an existing route outside it, and this fails.
func TestOAuthRoutesAnswerOnlyWhileMCPIsActive(t *testing.T) {
	type endpoint struct {
		method, path string
	}
	// Keep the required endpoints and their stub responses so removing a
	// registration still fails. Newly discovered handlers may have their
	// own active response, but must enforce the same module-state gate.
	routes := map[endpoint]int{
		{http.MethodGet, "/.well-known/oauth-authorization-server"}: http.StatusTeapot,
		{http.MethodGet, oauth.MetadataPath}:                        http.StatusTeapot,
		{http.MethodPost, "/oauth/token"}:                           http.StatusTeapot,
		{http.MethodPost, "/oauth/register"}:                        http.StatusTeapot,
		{http.MethodGet, "/oauth/authorize"}:                        http.StatusTeapot,
		{http.MethodPost, "/oauth/authorize"}:                       http.StatusTeapot,
		{http.MethodPost, "/oauth/revoke"}:                          http.StatusTeapot,
	}
	pass := func(next http.Handler) http.Handler { return next }
	for _, state := range []struct {
		name    string
		active  bool
		err     error
		blocked int
	}{
		{"active", true, nil, 0},
		{"inactive", false, nil, http.StatusNotFound},
		{"unreadable", false, errors.New("database is locked"), http.StatusServiceUnavailable},
	} {
		r := chi.NewRouter()
		checked := false
		registerOAuthRoutes(r, fakeOAuthEndpoints{}, reached, func(*http.Request) (bool, error) {
			checked = true
			return state.active, state.err
		}, pass, pass, pass)
		if err := chi.Walk(r, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			route := endpoint{method, path}
			if _, required := routes[route]; !required {
				routes[route] = 0
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for route, activeStatus := range routes {
			checked = false
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
			gated := route.method != http.MethodPost || route.path != "/oauth/revoke"
			want := activeStatus
			if gated && !state.active {
				want = state.blocked
			}
			if want != 0 && w.Code != want {
				t.Errorf("%s: %s %s = %d, want %d", state.name, route.method, route.path, w.Code, want)
			}
			if checked != gated {
				t.Errorf("%s: %s %s checked MCP state = %v, want %v", state.name, route.method, route.path, checked, gated)
			}
		}
	}
}

// Start the production assembly with a clean database and inspect its router
// at the serving boundary. No listener is needed to exercise its middleware.
func TestProductionOAuthRoutesStayGated(t *testing.T) {
	const child = "OCMS_TEST_PRODUCTION_OAUTH_ROUTES"
	if os.Getenv(child) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProductionOAuthRoutesStayGated$")
		cmd.Dir = t.TempDir()
		cmd.Env = []string{
			child + "=1",
			"OCMS_ENV=development",
			"OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!!",
			"OCMS_SITE_URL=http://127.0.0.1:8080",
			"OCMS_OAUTH_ENABLED=true",
			"OCMS_OAUTH_ALLOW_DCR=true",
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("production OAuth router: %v\n%s", err, out)
		}
		return
	}

	checked := 0
	inspect := func(router chi.Router) error {
		foundDiscovery, foundRevoke := false, false
		err := chi.Walk(router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if !strings.HasPrefix(path, "/oauth/") && !strings.HasPrefix(path, "/.well-known/") {
				return nil
			}
			if method == http.MethodGet {
				switch path {
				case "/.well-known/security.txt", "/.well-known/api-catalog",
					"/.well-known/agent-skills/index.json", "/.well-known/mcp/server-card.json":
					return nil // Public metadata outside the OAuth policy.
				}
			}
			if strings.ContainsAny(path, "{*") {
				return fmt.Errorf("OAuth route %s %s needs a concrete test path", method, path)
			}
			checked++
			want := http.StatusNotFound
			foundDiscovery = foundDiscovery || method == http.MethodGet && path == "/.well-known/oauth-authorization-server"
			req := httptest.NewRequest(method, path, nil)
			if method == http.MethodPost && path == "/oauth/revoke" {
				foundRevoke = true
				want = http.StatusOK
				req = httptest.NewRequest(method, path, strings.NewReader("client_id=fixture&token=unknown"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			// Each route gets its own rate-limit key.
			req.RemoteAddr = fmt.Sprintf("[2001:db8::%x]:12345", checked)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != want {
				t.Errorf("inactive MCP: %s %s = %d, want %d", method, path, w.Code, want)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !foundDiscovery || !foundRevoke {
			return fmt.Errorf("missing OAuth discovery or revocation in production router")
		}
		return nil
	}
	if err := run(inspect); err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("production router did not reach the serving boundary")
	}
}
