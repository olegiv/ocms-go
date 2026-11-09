// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/scheduler"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestCoreSchedulerRegistersRetentionJobsBeforeStart fails when the process
// stops registering a retention job for AI draft revisions, OAuth evidence
// or event logs. Jobs added after Start would race the cron loop.
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
	for _, job := range []string{"AddOAuthCleanup", "AddRevisionRetention", "AddEventCleanup"} {
		if at := slices.Index(calls, job); at < 0 || at > start {
			t.Errorf("startCoreScheduler must call s.%s before s.Start(); calls: %v", job, calls)
		}
	}
}

func TestCoreEventCleanupRespectsDisableAndScheduleOverride(t *testing.T) {
	for _, tc := range []struct {
		name        string
		enabled     bool
		diagnostics int
		want        bool
	}{
		{"enabled", true, 90, true},
		{"explicitly disabled", false, 90, false},
		{"both windows indefinite", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, closeDB := testutil.TestDB(t)
			t.Cleanup(closeDB)
			logger := testutil.TestLoggerSilent()
			registry := scheduler.NewRegistry(db, logger)
			sched := scheduler.New(db, logger, registry)
			if _, err := db.Exec(`INSERT INTO scheduler_overrides(source, name, override_schedule)
				VALUES ('core', 'event_cleanup', '@every 1h')`); err != nil {
				t.Fatal(err)
			}
			if err := startCoreScheduler(sched, &config.Config{
				EventCleanupEnabled: tc.enabled, EventDiagnosticDays: tc.diagnostics,
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(sched.Stop)
			found := false
			for _, job := range registry.List() {
				if job.Source == "core" && job.Name == "event_cleanup" {
					found = true
					if !job.CanTrigger || !job.IsOverridden || job.Schedule != "@every 1h" {
						t.Errorf("registered event cleanup = %+v", job)
					}
				}
			}
			if found != tc.want {
				t.Fatalf("event cleanup registered = %v, want %v", found, tc.want)
			}
			if err := registry.TriggerNow("core", "event_cleanup"); (err == nil) != tc.want {
				t.Errorf("trigger result = %v, want success = %v", err, tc.want)
			}
		})
	}
}
