// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"errors"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

// gatedModule is a module whose activation needs a password.
type gatedModule struct{ *mockModule }

func (gatedModule) ActiveByDefault() bool         { return false }
func (gatedModule) ActivationNeedsPassword() bool { return true }

// TestSetActiveRefusesPasswordGatedModule fails when a module whose
// activation needs the administrator's password can be switched on through
// SetActive, which no password check guards, instead of ActivateConfirmed.
func TestSetActiveRefusesPasswordGatedModule(t *testing.T) {
	logger := testutil.TestLoggerSilent()
	r := NewRegistry(logger)
	if err := r.Register(gatedModule{newMockModule("sql", "1.0.0")}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(newMockModule("plain", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	if err := r.InitAll(&Context{DB: db, Logger: logger}); err != nil {
		t.Fatal(err)
	}

	if !r.ActivationNeedsPassword("sql") || r.ActivationNeedsPassword("plain") || r.ActivationNeedsPassword("missing") {
		t.Fatal("ActivationNeedsPassword does not follow PasswordGatedActivation")
	}
	if err := r.SetActive("sql", true); !errors.Is(err, ErrActivationNeedsPassword) {
		t.Fatalf("SetActive(sql, true) = %v, want ErrActivationNeedsPassword", err)
	}
	if r.IsActive("sql") {
		t.Fatal("SetActive switched on a password-gated module")
	}
	if err := r.ActivateConfirmed("sql"); err != nil || !r.IsActive("sql") {
		t.Fatalf("ActivateConfirmed = %v, active %t", err, r.IsActive("sql"))
	}
	if err := r.SetActive("sql", false); err != nil || r.IsActive("sql") {
		t.Fatalf("deactivating = %v, active %t; want it allowed without a password", err, r.IsActive("sql"))
	}
	for _, info := range r.ListInfo() {
		if info.ActivationNeedsPassword != (info.Name == "sql") {
			t.Errorf("ListInfo %s: ActivationNeedsPassword = %t", info.Name, info.ActivationNeedsPassword)
		}
	}
}
