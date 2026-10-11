// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"fmt"
	"testing"
	"time"
)

// TestLockoutIsScopedToEmailAndIP fails when failures from one address lock
// an account for every address again: the per-address threshold must lock
// only that email+IP pair (finding L-7, 2026-10-11).
func TestLockoutIsScopedToEmailAndIP(t *testing.T) {
	lp := NewLoginProtection(testLoginProtectionDB(t), testLoginProtectionConfig(3, time.Minute, time.Minute))
	email := "owner@example.com"
	attacker, owner := "198.51.100.7", "203.0.113.9"

	for range 3 {
		lp.RecordFailedAttempt(email, attacker)
	}
	if locked, _ := lp.IsAccountLocked(email, attacker); !locked {
		t.Fatal("attacker address must be locked after the threshold")
	}
	if locked, _ := lp.IsAccountLocked(email, owner); locked {
		t.Fatal("the owner's address must not be locked by failures from another address")
	}
	if got := lp.GetRemainingAttempts(email, owner); got != 3 {
		t.Fatalf("owner remaining attempts = %d, want the full 3", got)
	}
}

// TestAccountWideLockoutAfterMultipliedThreshold fails when a distributed
// campaign can keep guessing forever: once the failures across addresses
// reach accountLockoutMultiplier times the threshold, the account locks for
// every address.
func TestAccountWideLockoutAfterMultipliedThreshold(t *testing.T) {
	lp := NewLoginProtection(testLoginProtectionDB(t), testLoginProtectionConfig(2, time.Minute, time.Minute))
	email := "owner@example.com"
	total := 2 * accountLockoutMultiplier

	var lockedAt int
	for i := 1; i <= total; i++ {
		ip := fmt.Sprintf("198.51.100.%d", i)
		if locked, _ := lp.RecordFailedAttempt(email, ip); locked && lockedAt == 0 {
			if locked2, _ := lp.IsAccountLocked(email, "203.0.113.9"); locked2 {
				lockedAt = i
			}
		}
	}
	if lockedAt != total {
		t.Fatalf("account-wide lock happened after %d failures, want %d", lockedAt, total)
	}
	if locked, _ := lp.IsAccountLocked(email, "203.0.113.9"); !locked {
		t.Fatal("account must be locked for an address that never failed")
	}

	lp.RecordSuccessfulLogin(email, "203.0.113.9")
	if locked, _ := lp.IsAccountLocked(email, "203.0.113.9"); locked {
		t.Fatal("successful login must clear the account-wide lock")
	}
}
