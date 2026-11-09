// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// TestCoreSchedulerRegistersRetentionJobsBeforeStart fails when the process
// stops registering a retention job. Without core:ai_revision_cleanup the
// revisions of AI drafts grow without bound; without core:oauth_cleanup the
// OAuth evidence does. Jobs added after Start would race the cron loop.
//
// Bug state: drop the AddRevisionRetention call, or move it after s.Start().
func TestCoreSchedulerRegistersRetentionJobsBeforeStart(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "scheduler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "startCoreScheduler" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "s" {
						calls = append(calls, sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
	start := slices.Index(calls, "Start")
	if start < 0 {
		t.Fatal("startCoreScheduler no longer starts the scheduler; this check is vacuous")
	}
	for _, job := range []string{"AddOAuthCleanup", "AddRevisionRetention"} {
		if at := slices.Index(calls, job); at < 0 || at > start {
			t.Errorf("startCoreScheduler must call s.%s before s.Start(); calls: %v", job, calls)
		}
	}
}
