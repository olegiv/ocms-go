// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// editorAccessibleModules lists every module whose admin routes editors may
// use. Everything else is admin-only through the registry gate. Adding a
// module here is a deliberate access-control decision: its handlers must
// re-check the role themselves, and its pages must be safe for editors.
var editorAccessibleModules = map[string]bool{
	"analytics_int": true,
}

var routeParamPattern = regexp.MustCompile(`\{[^}]+\}`)

// TestModuleAdminRoutesDenyEditorsUnlessAllowlisted fails when a module opts
// its admin routes in for editors without being listed above, when an
// allowlisted module stops opting in, or when any admin route of a non-listed
// module answers an editor with anything but the registry's admin-only
// denial. Module admin routes are mounted in the editor-level admin group, so
// a module that forgets its own gate is otherwise reachable by every editor.
func TestModuleAdminRoutesDenyEditorsUnlessAllowlisted(t *testing.T) {
	for _, mod := range allModules() {
		name := mod.Name()
		optedIn := module.AllowsEditors(mod)
		if optedIn != editorAccessibleModules[name] {
			t.Errorf("module %q: EditorAdminRoutes opt-in = %v, allowlist = %v; update editorAccessibleModules deliberately",
				name, optedIn, editorAccessibleModules[name])
		}
		if optedIn {
			continue
		}

		registry := module.NewRegistry(testutil.TestLoggerSilent())
		if err := registry.Register(mod); err != nil {
			t.Fatalf("module %q: register: %v", name, err)
		}
		router := chi.NewRouter()
		registry.AdminRouteAll(router)

		var routes [][2]string
		err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			routes = append(routes, [2]string{method, route})
			return nil
		})
		if err != nil {
			t.Fatalf("module %q: walk routes: %v", name, err)
		}

		for _, route := range routes {
			path := routeParamPattern.ReplaceAllString(route[1], "1")
			req := httptest.NewRequest(route[0], path, nil)
			req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{
				ID: 2, Email: "editor@example.com", Role: "editor",
			}))
			w := httptest.NewRecorder()
			if reached := serveRecovering(router, w, req); reached != nil {
				// The modules are not initialised here, so a handler that runs
				// dereferences a nil context: the panic itself proves the gate
				// was bypassed.
				t.Errorf("module %q: editor %s %s reached the handler (panic: %v); want 403 from the registry admin gate",
					name, route[0], route[1], reached)
				continue
			}

			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "insufficient permissions") {
				t.Errorf("module %q: editor %s %s got %d %q; want 403 from the registry admin gate",
					name, route[0], route[1], w.Code, strings.TrimSpace(w.Body.String()))
			}
		}
	}
}

// serveRecovering serves one request and returns the recovered panic value,
// if any, instead of letting it abort the whole test.
func serveRecovering(router http.Handler, w http.ResponseWriter, req *http.Request) (recovered any) {
	defer func() { recovered = recover() }()
	router.ServeHTTP(w, req)
	return nil
}
