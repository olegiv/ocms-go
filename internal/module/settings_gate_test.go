// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSettingsReloadGate checks that a failed load is retried once per
// interval, not on every call, and that a successful reload sticks.
func TestSettingsReloadGate(t *testing.T) {
	var gate SettingsReloadGate
	calls := 0
	fail := func() error { calls++; return errors.New("database is locked") }
	succeed := func() error { calls++; return nil }

	if loaded, err := gate.Retry(fail); loaded || err == nil || calls != 1 {
		t.Fatalf("first retry: loaded=%v err=%v calls=%d, want a failed attempt with its error", loaded, err, calls)
	}
	if loaded, err := gate.Retry(succeed); loaded || err != nil || calls != 1 {
		t.Fatalf("retry within the interval: loaded=%v err=%v calls=%d, want no attempt", loaded, err, calls)
	}
	past := time.Now().Add(-SettingsRetryInterval - time.Second)
	gate.lastAttempt.Store(&past)
	if loaded, err := gate.Retry(succeed); !loaded || err != nil || calls != 2 || !gate.Loaded() {
		t.Fatalf("retry after the interval: loaded=%v err=%v calls=%d, want loaded after one more call", loaded, err, calls)
	}
	if loaded, _ := gate.Retry(fail); !loaded || calls != 2 {
		t.Errorf("a loaded gate reloaded again (calls=%d)", calls)
	}

	// A shorter RetryInterval allows the next attempt sooner.
	short := SettingsReloadGate{RetryInterval: time.Nanosecond}
	_, _ = short.Retry(fail)
	time.Sleep(time.Millisecond)
	if loaded, _ := short.Retry(succeed); !loaded {
		t.Error("RetryInterval did not shorten the wait between attempts")
	}
}

// TestSettingsReloadGateUsesMonotonicClock checks that the attempt time the
// gate keeps carries a monotonic reading, which time.Time.Sub uses when both
// times carry one, and that settings_gate.go calls none of the time methods
// that drop it or read the wall clock. The gate used to keep unix
// nanoseconds, so a backwards wall-clock step stalled retries until the
// clock caught up.
//
// Bug state: store time.Now().Round(0), or compare UnixNano values, and
// this fails.
func TestSettingsReloadGateUsesMonotonicClock(t *testing.T) {
	gate := SettingsReloadGate{RetryInterval: time.Hour}
	_, _ = gate.Retry(func() error { return errors.New("database is locked") })
	last := gate.lastAttempt.Load()
	if last == nil {
		t.Fatal("Retry did not record its attempt")
	}
	// time.Time.String ends with "m=±<seconds>" when a monotonic reading is
	// present (documented in the time package).
	if !strings.Contains(last.String(), " m=") {
		t.Errorf("the recorded attempt %s has no monotonic reading", last)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "settings_gate.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wallClock := map[string]bool{
		"Unix": true, "UnixMilli": true, "UnixMicro": true, "UnixNano": true,
		"Round": true, "Truncate": true, "UTC": true, "Local": true, "In": true,
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && wallClock[sel.Sel.Name] {
			t.Errorf("%s calls %s, which reads the wall clock or drops the monotonic reading", fset.Position(sel.Pos()), sel.Sel.Name)
		}
		return true
	})
}

// TestSettingsReloadGateRetriesOnce checks that callers racing for a retry
// make one reload between them: the compare-and-swap on the attempt time
// lets a single caller through per interval. The race between reading and
// recording the attempt is narrow, so many rounds run.
//
// Bug state: replace the CompareAndSwap with a Store and this fails.
func TestSettingsReloadGateRetriesOnce(t *testing.T) {
	for round := range 200 {
		gate := SettingsReloadGate{RetryInterval: time.Hour}
		var reloads atomic.Int32
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				<-start
				_, _ = gate.Retry(func() error {
					reloads.Add(1)
					return errors.New("database is locked")
				})
			})
		}
		close(start)
		wg.Wait()
		if n := reloads.Load(); n != 1 {
			t.Fatalf("round %d: 32 racing callers made %d reloads, want 1 per interval", round, n)
		}
	}
}
