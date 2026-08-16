// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestConnectingAdminRoutesAreBlockedInDemoMode fails when a migrator route
// that dials a caller-supplied database host is registered outside a
// BlockInDemoMode group.
//
// A demo publishes its admin credentials, so these routes are reachable by
// anyone. The host allowlist matches on hostname only — modules/migrator/
// sources/shared/policy.go rejects ports in its entries — so an allowed entry
// covers every port on that host, and aiming a source at the server's own
// listener parks a request until the driver's handshake times out. Repeating it
// exhausts the handlers on a small demo machine.
//
// The guard is expressed over every POST rather than the three routes that
// exist today, so a new mutating route inherits it instead of quietly shipping
// unguarded.
//
// Bug state: move any r.Post out of the BlockInDemoMode group and this names
// the route.
func TestConnectingAdminRoutesAreBlockedInDemoMode(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "module.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing module.go: %v", err)
	}

	var register *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "RegisterAdminRoutes" {
			register = fn
			break
		}
	}
	if register == nil {
		t.Fatal("RegisterAdminRoutes not found; the routes moved and this test is vacuous")
	}

	guarded, unguarded := classifyPostRoutes(register)
	if len(guarded)+len(unguarded) == 0 {
		t.Fatal("no POST routes parsed from RegisterAdminRoutes")
	}
	if len(unguarded) > 0 {
		sort.Strings(unguarded)
		t.Errorf("these migrator POST routes are not inside a "+
			"middleware.BlockInDemoMode group, so a demo visitor can drive them "+
			"with attacker-chosen hosts:\n  %s", strings.Join(unguarded, "\n  "))
	}

	// The routes that dial or write must be present, not merely unguarded-free.
	for _, want := range []string{
		"/migrator/{source}/test",
		"/migrator/{source}/import",
		"/migrator/{source}/delete",
	} {
		if !slices.Contains(guarded, want) {
			t.Errorf("%s is no longer registered as a demo-blocked POST route", want)
		}
	}
}

// classifyPostRoutes splits the POST routes registered in fn into those inside
// a func literal that installs BlockInDemoMode and those that are not.
func classifyPostRoutes(fn *ast.FuncDecl) (guarded, unguarded []string) {
	var walk func(node ast.Node, blocked bool)
	walk = func(node ast.Node, blocked bool) {
		ast.Inspect(node, func(n ast.Node) bool {
			lit, ok := n.(*ast.FuncLit)
			if ok && lit.Body != node {
				// Descend into each chi group with its own blocked state, so a
				// nested group can add the guard without the outer one having it.
				walk(lit.Body, blocked || installsDemoBlock(lit.Body))
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Post" || len(call.Args) == 0 {
				return true
			}
			lit2, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit2.Kind != token.STRING {
				return true
			}
			route, uerr := strconv.Unquote(lit2.Value)
			if uerr != nil {
				return true
			}
			if blocked {
				guarded = append(guarded, route)
			} else {
				unguarded = append(unguarded, route)
			}
			return true
		})
	}
	walk(fn.Body, installsDemoBlock(fn.Body))
	return guarded, unguarded
}

// installsDemoBlock reports whether body calls Use(middleware.BlockInDemoMode(...))
// directly, ignoring nested func literals.
func installsDemoBlock(body *ast.BlockStmt) bool {
	for _, stmt := range body.List {
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := exprStmt.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Use" {
			continue
		}
		for _, arg := range call.Args {
			inner, ok := arg.(*ast.CallExpr)
			if !ok {
				continue
			}
			innerSel, ok := inner.Fun.(*ast.SelectorExpr)
			if ok && innerSel.Sel.Name == "BlockInDemoMode" {
				return true
			}
		}
	}
	return false
}
