// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package dbmanager

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
)

// TestActivationNeedsPassword fails when DB Manager stops asking for the
// administrator's current password on activation. Its raw SQL reaches every
// table, users and sessions included, so a stolen administrator session
// that could switch it on could take any account over.
func TestActivationNeedsPassword(t *testing.T) {
	var m module.Module = New()
	gated, ok := m.(module.PasswordGatedActivation)
	if !ok || !gated.ActivationNeedsPassword() {
		t.Fatal("DB Manager's activation does not need the administrator's password")
	}
}
