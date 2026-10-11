// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"fmt"
	"strings"
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

// TestLockoutPairKeyCannotBeForgedThroughEmail fails when a crafted email
// can produce another address's pair key: the email is raw form input, so
// the scope prefix and control-character stripping must keep the keys apart.
func TestLockoutPairKeyCannotBeForgedThroughEmail(t *testing.T) {
	lp := NewLoginProtection(testLoginProtectionDB(t), testLoginProtectionConfig(3, time.Minute, time.Minute))
	victim, victimIP := "owner@example.com", "203.0.113.9"
	forged := victim + lockoutKeySeparator + victimIP
	for i := range 3 * accountLockoutMultiplier {
		lp.RecordFailedAttempt(forged, fmt.Sprintf("198.51.100.%d", i+1))
	}
	if locked, _ := lp.IsAccountLocked(victim, victimIP); locked {
		t.Fatal("a crafted email must not lock the owner's real email+IP scope")
	}
	for _, scope := range lp.lockoutScopes(forged, "1.2.3.4") {
		if strings.Count(scope.key, lockoutKeySeparator) != strings.Count(lp.lockoutScopes(victim, "1.2.3.4")[0].key, lockoutKeySeparator) && scope.name == "email+ip" {
			t.Fatalf("control characters survived in key %q", scope.key)
		}
	}
}

// TestRemainingAttemptsHonoursAccountScope fails when the account-wide
// counter stops being the binding limit for a fresh address.
func TestRemainingAttemptsHonoursAccountScope(t *testing.T) {
	lp := NewLoginProtection(testLoginProtectionDB(t), testLoginProtectionConfig(3, time.Minute, time.Minute))
	email := "owner@example.com"
	accountThreshold := 3 * accountLockoutMultiplier
	for i := range accountThreshold - 1 {
		lp.RecordFailedAttempt(email, fmt.Sprintf("198.51.100.%d", i+1))
	}
	if got := lp.GetRemainingAttempts(email, "203.0.113.9"); got != 1 {
		t.Fatalf("remaining for a fresh address = %d, want 1 (account scope binds)", got)
	}
	if locked, _ := lp.RecordFailedAttempt(email, "203.0.113.10"); !locked {
		t.Fatal("the account must lock on the final failure")
	}
	if locked, _ := lp.IsAccountLocked(email, "203.0.113.11"); !locked {
		t.Fatal("an address that never failed must see the account lock")
	}
}

// TestAccountScopeBackoffAndWindowReset fails when the account-wide scope
// loses the exponential backoff or the attempt-window reset that the pair
// scope has.
func TestAccountScopeBackoffAndWindowReset(t *testing.T) {
	lp := NewLoginProtection(testLoginProtectionDB(t), testLoginProtectionConfig(2, time.Second, time.Minute))
	email := "owner@example.com"
	total := 2 * accountLockoutMultiplier
	ip := 0
	lockWith := func() time.Duration {
		for range total {
			ip++
			if locked, d := lp.RecordFailedAttempt(email, fmt.Sprintf("198.51.100.%d", ip)); locked {
				return d
			}
		}
		t.Fatal("account did not lock")
		return 0
	}
	first := lockWith()
	second := lockWith()
	if second != 2*first {
		t.Fatalf("second account lock = %v, want twice the first (%v)", second, first)
	}
}

// TestAccountScopeWindowReset fails when failures outside the attempt window
// still count towards the account-wide lock.
func TestAccountScopeWindowReset(t *testing.T) {
	lp := NewLoginProtection(testLoginProtectionDB(t), testLoginProtectionConfig(2, time.Second, 150*time.Millisecond))
	email := "owner@example.com"
	total := 2 * accountLockoutMultiplier
	for i := range total - 1 {
		lp.RecordFailedAttempt(email, fmt.Sprintf("198.51.100.%d", i+1))
	}
	time.Sleep(200 * time.Millisecond)
	if locked, _ := lp.RecordFailedAttempt(email, "203.0.113.9"); locked {
		t.Fatal("a failure after the window must start a new count, not lock")
	}
}
