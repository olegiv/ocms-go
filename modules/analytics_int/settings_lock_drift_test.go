// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestSettingsAccessHoldsTheLock fails when module code touches m.settings in
// a function (or closure) that does not itself take m.settingsMu.
//
// The tracker reads the settings on every frontend request while the settings
// form and salt rotation write them, so an unguarded access is a data race
// (security audit 2026-10-17, L-3). No test drives the two concurrently and CI
// does not run -race, so this is the check that holds the line. Readers go
// through settingsSnapshot or IsEnabled; writers through storeSettings or
// updateSettings.
//
// Bug state: make IsEnabled read m.settings.Enabled directly again and this
// names the line.
func TestSettingsAccessHoldsTheLock(t *testing.T) {
	testutil.AssertFieldAccessHoldsLock(t, ".", "settings", "settingsMu")
}
