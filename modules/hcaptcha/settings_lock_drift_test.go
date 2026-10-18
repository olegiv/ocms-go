// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package hcaptcha

import (
	"fmt"
	"sync"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestSettingsAccessHoldsTheLock fails when module code touches m.settings in
// a function (or closure) that does not first take m.settingsMu.
//
// The login and form pages read the settings on every request while the settings form and
// ReloadSettings replace them, so an unguarded access is a data race, the one
// analytics_int and analytics_ext had (security audit 2026-10-17, L-3).
// Readers go through currentSettings and writers through storeSettings.
//
// Bug state: make IsEnabled read m.settings directly again and this names the
// line.
func TestSettingsAccessHoldsTheLock(t *testing.T) {
	testutil.AssertFieldAccessHoldsLock(t, ".", "settings", "settingsMu")
}

// TestStoreSettingsWhileRendering replaces the settings while pages render
// with them. Run with -race, as CI does; without it the test still checks
// that the last store wins.
func TestStoreSettingsWhileRendering(t *testing.T) {
	m := New()
	const stores = 20
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range stores {
			m.storeSettings(&Settings{Enabled: true, SiteKey: fmt.Sprintf("site-%d", i), SecretKey: "secret", Theme: "light", Size: "normal"})
		}
	})
	// Each reader runs alone in its goroutine: a locked read elsewhere in the
	// same goroutine would order an unlocked one after the write and hide it
	// from the race detector.
	for _, read := range []func(){
		func() { _ = m.IsEnabled() },
		func() { _ = m.RenderWidget() },
		func() { _ = m.GetSettings() },
	} {
		wg.Go(func() {
			for range 10 * stores {
				read()
			}
		})
	}
	wg.Wait()

	if got, want := m.currentSettings().SiteKey, fmt.Sprintf("site-%d", stores-1); got != want {
		t.Errorf("SiteKey = %q, want %q", got, want)
	}
}
