// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/olegiv/ocms-go/internal/mfa"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestRunMFAReset checks `ocms mfa-reset`: it removes the account's second
// factor without OCMS_MFA_KEY and signs the account out, and refuses an
// unknown email or a database that is not there.
func TestRunMFAReset(t *testing.T) {
	unsetEnv(t, "OCMS_DB_PATH", "OCMS_ENV_FILE")
	site := newSiteDir(t)
	dbPath := filepath.Join(site, "data", "ocms.db")
	db, err := store.NewDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	res, err := db.Exec(`INSERT INTO users (email, password_hash, role, name, created_at, updated_at) VALUES ('admin@example.com', 'x', 'admin', 'Admin', ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := mfa.NewService(db, "Zp8Lq3Wn6Rx1Tc9Vb4Ym7Kd2Hf5Gj0Sa8Ne3Ux6Qi=", false)
	if err != nil {
		t.Fatal(err)
	}
	e, err := svc.BeginEnrollment(userID, nil, "Test", "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCodeCustom(e.Secret, now, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmEnrollment(t.Context(), userID, e.Sealed, code); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := runMFAReset([]string{"missing@example.com"}); err == nil || !strings.Contains(err.Error(), "no user") {
		t.Errorf("unknown email: %v, want a no-user error", err)
	}
	if err := runMFAReset([]string{"-db", filepath.Join(site, "typo.db"), "admin@example.com"}); err == nil || !strings.Contains(err.Error(), "no database") {
		t.Errorf("missing database: %v, want a no-database error", err)
	}
	if err := runMFAReset(nil); err == nil {
		t.Error("mfa-reset without an email succeeded")
	}
	if err := runMFAReset([]string{"admin@example.com"}); err != nil {
		t.Fatalf("ocms mfa-reset: %v", err)
	}

	db, err = store.NewDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err = mfa.NewService(db, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled, err := svc.Enrolled(t.Context(), userID); err != nil || enrolled {
		t.Errorf("Enrolled after mfa-reset = %t, %v", enrolled, err)
	}
	var version int64
	if err := db.QueryRow(`SELECT session_version FROM users WHERE id = ?`, userID).Scan(&version); err != nil || version != 2 {
		t.Errorf("session_version = %d, %v; want 2 (enrollment and reset each sign out)", version, err)
	}

	// Without a second factor there is nothing to reset: the account keeps
	// its sessions and the Event Log gets no reset record.
	if err := runMFAReset([]string{"admin@example.com"}); err != nil {
		t.Fatalf("ocms mfa-reset without a second factor: %v", err)
	}
	if err := db.QueryRow(`SELECT session_version FROM users WHERE id = ?`, userID).Scan(&version); err != nil || version != 2 {
		t.Errorf("session_version after a no-op reset = %d, %v; want 2", version, err)
	}
	var resets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE message = 'Two-factor authentication reset from the command line'`).Scan(&resets); err != nil || resets != 1 {
		t.Errorf("reset events = %d, %v; want 1", resets, err)
	}
}
