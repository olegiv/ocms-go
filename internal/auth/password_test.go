// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"testing"
	"time"
)

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("changeme")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned empty hash")
	}
	t.Logf("Generated hash: %s", hash)
}

func TestCheckPassword_Correct(t *testing.T) {
	hash, err := HashPassword("changeme")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}

	valid, err := CheckPassword("changeme", hash)
	if err != nil {
		t.Fatalf("CheckPassword error: %v", err)
	}
	if !valid {
		t.Fatal("Correct password was rejected")
	}
}

func TestCheckPassword_Wrong(t *testing.T) {
	hash, err := HashPassword("changeme")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}

	valid, err := CheckPassword("wrongpassword", hash)
	if err != nil {
		t.Fatalf("CheckPassword error: %v", err)
	}
	if valid {
		t.Fatal("Wrong password was accepted")
	}
}

func TestCheckPassword_DBHash(t *testing.T) {
	// This is the actual hash stored in the database for "changeme"
	dbHash := "$argon2id$v=19$m=65536,t=1,p=4$mucMvOaS6lZ2LWNS1OEFKw$UYEWv8cvCOO6l2zGeqv3JPVe1nyy0x9GXBfYEuDM544"

	valid, err := CheckPassword("changeme", dbHash)
	if err != nil {
		t.Fatalf("CheckPassword error: %v", err)
	}
	if !valid {
		t.Fatal("DB hash rejected correct password 'changeme'")
	}

	// Also verify wrong password is rejected
	valid, err = CheckPassword("wrongpassword", dbHash)
	if err != nil {
		t.Fatalf("CheckPassword error: %v", err)
	}
	if valid {
		t.Fatal("DB hash accepted wrong password")
	}
}

func TestNeedsRehash_OldParameters(t *testing.T) {
	// Hash with old parameters (m=65536, t=1, p=4) — should need rehash
	oldHash := "$argon2id$v=19$m=65536,t=1,p=4$mucMvOaS6lZ2LWNS1OEFKw$UYEWv8cvCOO6l2zGeqv3JPVe1nyy0x9GXBfYEuDM544"
	if !NeedsRehash(oldHash) {
		t.Fatal("hash with old parameters should need rehash")
	}
}

func TestNeedsRehash_CurrentParameters(t *testing.T) {
	// Hash with current parameters — should NOT need rehash
	hash, err := HashPassword("testpassword")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if NeedsRehash(hash) {
		t.Fatal("freshly hashed password should not need rehash")
	}
}

func TestNeedsRehash_InvalidHash(t *testing.T) {
	if !NeedsRehash("invalid") {
		t.Fatal("invalid hash should need rehash")
	}
	if !NeedsRehash("") {
		t.Fatal("empty hash should need rehash")
	}
}

// TestVerifyDummyPassword_RunsArgon2 ensures the dummy-password timing
// anchor actually performs Argon2id work, rather than returning early.
// This is the drift test for audit finding FIND-003: if a future refactor
// turns VerifyDummyPassword into a no-op, the login handler's
// non-existent-user branch would regain its timing side channel.
//
// The lower bound is intentionally loose (5ms) to accommodate slow CI
// machines; the current Argon2 parameters (m=19456, t=2, p=1) take
// ~20ms even on a fast laptop, so a no-op implementation returning in
// microseconds will fail this test reliably.
func TestVerifyDummyPassword_RunsArgon2(t *testing.T) {
	if dummyPasswordHash == "" {
		t.Fatal("dummyPasswordHash not initialized — init() failed?")
	}

	start := time.Now()
	VerifyDummyPassword("any-password-ignored")
	elapsed := time.Since(start)

	const minCost = 5 * time.Millisecond
	if elapsed < minCost {
		t.Errorf("VerifyDummyPassword completed in %v, below %v floor — Argon2 work not performed, timing enumeration possible", elapsed, minCost)
	}
}

// TestVerifyDummyPassword_HashIsValidArgon2id pins the shape of the
// pre-computed dummy hash so a future refactor can't silently replace it
// with an invalid placeholder (which would make VerifyArgon2 error out
// fast and defeat the timing normalization).
func TestVerifyDummyPassword_HashIsValidArgon2id(t *testing.T) {
	if dummyPasswordHash == "" {
		t.Fatal("dummyPasswordHash not initialized")
	}
	// Any Argon2id hash is rejected by NeedsRehash only when parameters
	// drift; a well-formed, current-parameter hash must not need rehash.
	if NeedsRehash(dummyPasswordHash) {
		t.Errorf("dummyPasswordHash reports NeedsRehash=true — it's malformed or uses stale parameters: %q", dummyPasswordHash)
	}
}

func TestRehashProducesVerifiableHash(t *testing.T) {
	password := "changeme1234"

	// Create hash with old-style parameters (verify it works)
	oldHash := "$argon2id$v=19$m=65536,t=1,p=4$mucMvOaS6lZ2LWNS1OEFKw$UYEWv8cvCOO6l2zGeqv3JPVe1nyy0x9GXBfYEuDM544"
	valid, err := CheckPassword("changeme", oldHash)
	if err != nil {
		t.Fatalf("CheckPassword old hash error: %v", err)
	}
	if !valid {
		t.Fatal("old hash should verify correctly")
	}

	// Re-hash with current parameters
	newHash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}

	// Verify the new hash works
	valid, err = CheckPassword(password, newHash)
	if err != nil {
		t.Fatalf("CheckPassword new hash error: %v", err)
	}
	if !valid {
		t.Fatal("re-hashed password should verify correctly")
	}

	// Verify the new hash uses current parameters (no rehash needed)
	if NeedsRehash(newHash) {
		t.Fatal("newly hashed password should not need rehash")
	}
}
