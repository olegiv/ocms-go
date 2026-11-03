// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/auth"
)

// TestSeedWithAdminPasswordReplacesTheDefault checks that OCMS_DO_SEED with
// OCMS_SEED_ADMIN_PASSWORD creates the admin with that password, so a first
// boot reachable from other hosts never holds the documented default
// credentials (2026-11-03 audit C-2).
//
// Bug state: hash DefaultAdminPassword whatever the argument and this fails.
func TestSeedWithAdminPasswordReplacesTheDefault(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()
	const password = "Owner-chosen bootstrap passphrase"
	if err := SeedWithAdminPassword(ctx, db, true, password); err != nil {
		t.Fatal(err)
	}
	admin, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatal(err)
	}
	if ok, checkErr := auth.CheckPassword(password, admin.PasswordHash); checkErr != nil || !ok {
		t.Fatalf("seeded admin does not accept the configured password: %v", checkErr)
	}
	if hasDefault, checkErr := HasDefaultAdminCredentials(ctx, q); checkErr != nil || hasDefault {
		t.Fatalf("seeded admin still has the default credentials: %v %v", hasDefault, checkErr)
	}
}
