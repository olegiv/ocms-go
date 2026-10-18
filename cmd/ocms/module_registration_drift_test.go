// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/modules/sentinel"
)

// TestAllModulesMirrorsRegisterModules fails when a module is registered at
// startup but missing from allModules(), or listed there but never
// registered. allModules() is a hand-kept mirror, and a module missing from it
// would let its template funcs escape the placeholder guard in
// TestEveryModuleTemplateFuncHasRendererPlaceholder.
func TestAllModulesMirrorsRegisterModules(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	registry := module.NewRegistry(testutil.TestLoggerSilent())
	if _, err := registerModules(registry, sentinel.New(), scs.New(), service.NewEventService(db)); err != nil {
		t.Fatalf("registerModules: %v", err)
	}

	registered := make(map[string]bool)
	for _, mod := range registry.List() {
		registered[mod.Name()] = true
	}
	enumerated := make(map[string]bool)
	for _, mod := range allModules() {
		enumerated[mod.Name()] = true
	}

	for name := range registered {
		if !enumerated[name] {
			t.Errorf("module %q is registered by registerModules() but missing from allModules()", name)
		}
	}
	for name := range enumerated {
		if !registered[name] {
			t.Errorf("module %q is listed in allModules() but registerModules() never registers it", name)
		}
	}
	if !registered["mcp"] {
		t.Error("the MCP module must be registered at startup")
	}
}

// TestInternalAnalyticsTrackingIgnoresStartupEnabled fails when run installs
// the internal analytics tracking middleware only if tracking is enabled
// while the router is built. The middleware checks IsEnabled per request;
// installed only when enabled, it tracked nothing until a restart when the
// settings loaded after a startup read failure or tracking was turned on
// later (Codex review of PR #189). Whether the module is active at startup
// may still decide it.
//
// Bug state: guard r.Use(internalAnalyticsModule.GetTrackingMiddleware()) in
// main.go with `internalAnalyticsModule.IsEnabled()` and this fails.
func TestInternalAnalyticsTrackingIgnoresStartupEnabled(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	var ancestors []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return true
		}
		if call, ok := n.(*ast.CallExpr); ok && calledName(call) == "GetTrackingMiddleware" {
			found = true
			for _, ancestor := range ancestors {
				if ifStmt, ok := ancestor.(*ast.IfStmt); ok && callsMethod(ifStmt.Cond, "IsEnabled") {
					t.Errorf("%s installs the tracking middleware only while tracking is enabled; it checks IsEnabled per request", fset.Position(call.Pos()))
				}
			}
		}
		ancestors = append(ancestors, n)
		return true
	})
	if !found {
		t.Fatal("main.go never installs GetTrackingMiddleware; the layout changed and this test is vacuous")
	}
}

// callsMethod reports whether expr contains a call of a method or function
// named name.
func callsMethod(expr ast.Expr, name string) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && calledName(call) == name {
			found = true
		}
		return !found
	})
	return found
}
