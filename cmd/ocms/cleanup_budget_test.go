// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCleanupBudget checks that shutdown steps share one time limit: a step
// that finishes in time is waited for, one that panics is logged, one that
// overruns is logged and left behind once the shared budget is spent, and a
// quick step after them still runs. The clock is shared: a step after a slow
// one is waited for only what is left. The steps used to run one after
// another with no limit, so a slow webhook delivery or scheduled task held
// the exit past a deployment's stop grace period (review of the 2026-10-17
// audit fixes).
//
// Bug state: make within wait for the step without a timer and the hung
// step holds the test until its ten-second guard; start the deadline anew
// for each step and the hung step is waited for a whole budget; drop the
// recover in within and the test binary crashes.
func TestCleanupBudget(t *testing.T) {
	logs := captureDefaultLog(t)
	budget := &cleanupBudget{limit: time.Second}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var first, last atomic.Bool
	var hungWait time.Duration
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		budget.within("first", func() { time.Sleep(700 * time.Millisecond); first.Store(true) })
		budget.within("panics", func() { panic("step boom") })
		hungStart := time.Now()
		budget.within("hung", func() { <-release })
		hungWait = time.Since(hungStart)
		budget.within("last", func() { last.Store(true) })
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the shutdown steps did not return within ten seconds")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the steps took %v with a one-second budget", elapsed)
	}
	if hungWait > 650*time.Millisecond {
		t.Errorf("the hung step was waited for %v after a 700 ms step, want only the rest of the one-second budget", hungWait)
	}
	if !first.Load() || !last.Load() {
		t.Errorf("first ran: %v, last ran: %v; want both", first.Load(), last.Load())
	}
	if !strings.Contains(logs.String(), `step=hung`) || strings.Contains(logs.String(), `step=first`) {
		t.Errorf("want only the hung step logged as overrunning:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), `msg="shutdown step panicked; continuing" step=panics panic="step boom"`) {
		t.Errorf("the panicking step was not logged:\n%s", logs.String())
	}
}

// TestRunBoundsCleanup fails when one of run's deferred shutdown steps (a
// Stop method or the module shutdown) runs outside the shared cleanup
// budget, where it could hold the exit indefinitely, or when the Event Log
// writer is not closed, with eventLogCloseTimeout, between the database
// close and the last failure log: deferred calls run in reverse, so that
// order writes the failure to the Event Log before the database closes.
//
// Bug state: write defer sched.Stop() in run, drop the Event Log close, or
// close it with context.Background(), and this fails.
func TestRunBoundsCleanup(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	bounded := 0
	var order []string // the database close, the event log close and failure logs, in source order
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "run" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			deferred, ok := n.(*ast.DeferStmt)
			if !ok {
				return true
			}
			call := exprString(deferred.Call)
			switch {
			case strings.Contains(call, "db.Close()"):
				order = append(order, "db")
			case strings.Contains(call, "eventLogHandler.Close("):
				order = append(order, "event log")
				if !strings.Contains(call, "context.WithTimeout(context.Background(), eventLogCloseTimeout)") {
					t.Errorf("%s closes the Event Log writer without eventLogCloseTimeout, so a locked database can hold the exit", fset.Position(deferred.Pos()))
				}
			case strings.HasPrefix(call, "logRunFailure("):
				order = append(order, "failure")
			}
			switch {
			case strings.HasPrefix(call, "cleanup.within("):
				bounded++
			case strings.HasPrefix(call, "signal.Stop("):
			case strings.Contains(call, ".Stop()") || strings.Contains(call, "ShutdownAll()"):
				t.Errorf("%s defers %s outside cleanup.within", fset.Position(deferred.Pos()), call)
			}
			return true
		})
	}
	if bounded < 4 {
		t.Errorf("run defers %d steps through cleanup.within, want the module, webhook, scheduler and cache shutdowns", bounded)
	}
	if got := strings.Join(order, " > "); !strings.Contains(got, "db > event log > failure") {
		t.Errorf("run defers %q, want the database close, then the event log close, then a failure log", got)
	}
}
