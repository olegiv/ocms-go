// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sentinel

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestSettingsAccessHoldsTheLock fails when module code touches one of the
// cached settings flags in a function (or closure) that does not first take
// m.settingsMu. The ban middleware reads them on every request while the
// settings form reloads them, the race analytics_int had (security audit
// 2026-10-17, L-3). Readers go through IsBanCheckEnabled and its siblings.
//
// Bug state: make Init log m.banCheckEnabled directly again and this names
// the line.
func TestSettingsAccessHoldsTheLock(t *testing.T) {
	for _, field := range []string{"banCheckEnabled", "autoBanEnabled", "honeypotAutoBanEnabled"} {
		t.Run(field, func(t *testing.T) {
			testutil.AssertFieldAccessHoldsLock(t, ".", field, "settingsMu")
		})
	}
}
