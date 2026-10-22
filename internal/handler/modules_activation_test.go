// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"go/ast"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// passwordGatedModule is a module whose activation needs a password, like
// DB Manager.
type passwordGatedModule struct{ module.BaseModule }

func (*passwordGatedModule) ActiveByDefault() bool         { return false }
func (*passwordGatedModule) ActivationNeedsPassword() bool { return true }

// TestModuleActivationNeedsPassword fails when a stolen administrator
// session can switch on a module whose activation needs a password (DB
// Manager, whose raw SQL reaches every table) without the administrator's
// current password, or when wrong passwords stop counting toward the lockout.
// Switching it off and toggling other modules need none.
//
// Bug state: have ToggleActive call ActivateConfirmed without
// confirmActivation, and this fails.
func TestModuleActivationNeedsPassword(t *testing.T) {
	env := newMFATestEnv(t, false)
	lp := env.withLockout(t, 2)
	admin := env.createUser(t, model.RoleAdmin)
	logger := testutil.TestLoggerSilent()
	registry := module.NewRegistry(logger)
	for _, m := range []module.Module{
		&passwordGatedModule{module.NewBaseModule("sqlconsole", "1.0.0", "SQL console")},
		&passwordGatedModule{module.NewBaseModule("sqlconsole2", "1.0.0", "SQL console")},
	} {
		if err := registry.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	plain := module.NewBaseModule("plain", "1.0.0", "Plain")
	if err := registry.Register(&plain); err != nil {
		t.Fatal(err)
	}
	if err := registry.InitAll(&module.Context{DB: env.db, Logger: logger}); err != nil {
		t.Fatal(err)
	}
	h := NewModulesHandler(env.db, env.h.renderer, env.sm, registry, module.NewHookRegistry(logger))
	h.SetPasswordConfirmation(lp, nil)

	toggle := func(name string, active bool, password string) (int, string) {
		body, err := json.Marshal(map[string]any{"active": active, "password": password})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/admin/modules/"+name+"/toggle", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req = addUserToContext(requestWithSession(env.sm, requestWithURLParams(req, map[string]string{"name": name})), &admin)
		rr := httptest.NewRecorder()
		h.ToggleActive(rr, req)
		var resp struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		return rr.Code, resp.Error
	}

	for _, tc := range []struct{ password, message string }{
		{"", "Enter your current password to activate this module."},
		{"Not-the-password!", "Your current password is incorrect."},
	} {
		if code, msg := toggle("sqlconsole", true, tc.password); code != http.StatusForbidden || msg != tc.message {
			t.Errorf("activation with password %q = %d %q, want 403 %q", tc.password, code, msg, tc.message)
		}
		if registry.IsActive("sqlconsole") {
			t.Fatalf("activation with password %q switched the module on", tc.password)
		}
	}
	if code, _ := toggle("sqlconsole", true, mfaTestPassword); code != http.StatusOK || !registry.IsActive("sqlconsole") {
		t.Fatalf("activation with the right password = %d, active %t", code, registry.IsActive("sqlconsole"))
	}
	if code, _ := toggle("sqlconsole", false, ""); code != http.StatusOK || registry.IsActive("sqlconsole") {
		t.Fatalf("deactivation = %d, active %t; want it allowed without a password", code, registry.IsActive("sqlconsole"))
	}
	if code, _ := toggle("plain", false, ""); code != http.StatusOK {
		t.Fatalf("toggling an ungated module = %d", code)
	}
	if code, _ := toggle("plain", true, ""); code != http.StatusOK || !registry.IsActive("plain") {
		t.Fatalf("activating an ungated module = %d", code)
	}

	// The earlier wrong password still counts after the right one: a second
	// wrong one locks the account, and then even the right one is refused.
	if code, _ := toggle("sqlconsole2", true, "Not-the-password-2!"); code != http.StatusForbidden {
		t.Fatalf("second wrong password = %d", code)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if locked, _ := lp.IsAccountLocked(admin.Email, middleware.GetClientIP(req)); !locked {
		t.Fatal("wrong activation passwords do not count toward the lockout")
	}
	if code, msg := toggle("sqlconsole2", true, mfaTestPassword); code != http.StatusForbidden || msg != "Too many failed attempts; try again later." || registry.IsActive("sqlconsole2") {
		t.Fatalf("right password while locked = %d %q, active %t", code, msg, registry.IsActive("sqlconsole2"))
	}
}

// TestActivateConfirmedOnlyAfterPassword fails when code other than
// ModulesHandler.ToggleActive, which checks the administrator's password
// first, calls Registry.ActivateConfirmed: that would switch on DB Manager
// without the password its activation needs.
//
// Bug state: call registry.ActivateConfirmed from ToggleSidebar, and this
// names it.
func TestActivateConfirmedOnlyAfterPassword(t *testing.T) {
	checkGatedCalls(t, map[string]string{
		"internal/handler/modules.go (*ModulesHandler).ToggleActive": "calls it only after confirmActivation accepts the password",
	}, func(file *ast.File) []gatedCall {
		return gatedCalls(file, map[string]bool{"ActivateConfirmed": true}, nil)
	}, "activates a password-gated module; confirm the administrator's password first like ModulesHandler.ToggleActive")
}
