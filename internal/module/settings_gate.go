// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"sync/atomic"
	"time"
)

// SettingsRetryInterval is the default minimum time between the reload
// attempts SettingsReloadGate.Retry makes.
const SettingsRetryInterval = time.Minute

// SettingsReloadGate tracks whether a module's stored settings are in effect.
// A module whose settings fail to load at startup runs on safe fallback values
// and calls Retry from the code that reads them; Retry reloads at most once
// per interval, so the stored settings come back after the database recovers
// without a query on every request. There is no timer: the next read after
// the interval retries. The zero value is a gate whose settings are not
// loaded yet.
type SettingsReloadGate struct {
	// RetryInterval overrides SettingsRetryInterval when positive.
	RetryInterval time.Duration

	loaded atomic.Bool
	// lastAttempt is when Retry last attempted a reload. It keeps the
	// monotonic clock reading time.Now returns, so a wall-clock step does not
	// stretch or skip the interval.
	lastAttempt atomic.Pointer[time.Time]
}

// Loaded reports whether the stored settings are in effect.
func (g *SettingsReloadGate) Loaded() bool { return g.loaded.Load() }

// MarkLoaded records that the stored settings are in effect.
func (g *SettingsReloadGate) MarkLoaded() { g.loaded.Store(true) }

// Retry calls reload when the settings are not loaded and no reload was tried
// within the interval, marking them loaded when it succeeds. It reports
// whether the stored settings are in effect afterwards and returns the error
// of the reload it tried, if that failed, for the caller to log.
func (g *SettingsReloadGate) Retry(reload func() error) (bool, error) {
	if g.loaded.Load() {
		return true, nil
	}
	interval := g.RetryInterval
	if interval <= 0 {
		interval = SettingsRetryInterval
	}
	now := time.Now()
	last := g.lastAttempt.Load()
	if (last != nil && now.Sub(*last) < interval) || !g.lastAttempt.CompareAndSwap(last, &now) {
		return false, nil
	}
	if err := reload(); err != nil {
		return false, err
	}
	g.loaded.Store(true)
	return true, nil
}
