// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRunWiresWebhooksIntoEveryWriter fails when run does not give the
// webhook dispatcher to the REST pages and media services and to the
// scheduler, or gives it to the scheduler only after starting it. Only the
// admin UI sent webhook events, so integrations missed every REST write and
// every scheduled publish (review of the 2026-10-17 audit fixes).
//
// Bug state: drop sched.SetWebhookDispatcher, or move it after sched.Start,
// and this fails.
func TestRunWiresWebhooksIntoEveryWriter(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]token.Pos{} // receiver.method -> first call in run
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "run" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); ok {
				name := recv.Name + "." + sel.Sel.Name
				if _, seen := positions[name]; !seen {
					positions[name] = call.Pos()
				}
			}
			return true
		})
	}
	for _, writer := range []string{"pagesSvc", "mediaSvc", "sched"} {
		if _, ok := positions[writer+".SetWebhookDispatcher"]; !ok {
			t.Errorf("run does not call %s.SetWebhookDispatcher, so its writes send no webhook events", writer)
		}
	}
	set, start := positions["sched.SetWebhookDispatcher"], positions["sched.Start"]
	if start == token.NoPos {
		t.Fatal("run does not call sched.Start; the layout changed and this test is vacuous")
	}
	if set != token.NoPos && set > start {
		t.Errorf("run calls sched.SetWebhookDispatcher (%s) after sched.Start (%s); the scheduler's goroutines would read it unsynchronized", fset.Position(set), fset.Position(start))
	}
}
