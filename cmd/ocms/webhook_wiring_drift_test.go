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
// Bug state: drop sched.SetWebhookDispatcher, or move it after startCoreScheduler,
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
			if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "startCoreScheduler" {
				if len(call.Args) == 1 {
					if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == "sched" {
						positions[name.Name] = call.Pos()
					}
				}
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
	set, start := positions["sched.SetWebhookDispatcher"], positions["startCoreScheduler"]
	if start == token.NoPos {
		t.Fatal("run does not call startCoreScheduler(sched); the layout changed and this test is vacuous")
	}
	if set != token.NoPos && set > start {
		t.Errorf("run calls sched.SetWebhookDispatcher (%s) after startCoreScheduler (%s); the scheduler's goroutines would read it unsynchronized", fset.Position(set), fset.Position(start))
	}
	helper, err := parser.ParseFile(fset, "scheduler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var starts bool
	for _, decl := range helper.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "startCoreScheduler" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Start" {
					if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "s" {
						starts = true
					}
				}
			}
			return true
		})
	}
	if !starts {
		t.Fatal("startCoreScheduler no longer starts its scheduler; the ordering check is vacuous")
	}
}
