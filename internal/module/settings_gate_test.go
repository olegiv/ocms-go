// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"errors"
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
	gate.lastAttempt.Store(time.Now().Add(-SettingsRetryInterval - time.Second).UnixNano())
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
