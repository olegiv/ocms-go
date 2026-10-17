// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestSettingsAccessHoldsTheLock fails when module code touches m.settings in
// a function (or closure) that does not first take m.mu. Pages render the
// embed widgets and the proxy reads the provider settings on every request
// while the settings form replaces them, the race analytics_int had
// (security audit 2026-10-17, L-3).
//
// Bug state: drop the m.mu.RLock from countEnabled and this names the line.
func TestSettingsAccessHoldsTheLock(t *testing.T) {
	testutil.AssertFieldAccessHoldsLock(t, ".", "settings", "mu")
}
