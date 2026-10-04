// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"context"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// optInModule is a mock module implementing ActivationDefaulter.
type optInModule struct {
	*mockModule
	activeByDefault bool
}

func (m *optInModule) ActiveByDefault() bool { return m.activeByDefault }

// TestActivationDefaulterRegistersInactive verifies an opt-in module is
// inserted inactive on first registration, is not initialized at startup, and
// can still be switched on by an administrator.
func TestActivationDefaulterRegistersInactive(t *testing.T) {
	logger := testutil.TestLoggerSilent()
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	r := NewRegistry(logger)
	m := &optInModule{mockModule: newMockModule("opt-in", "1.0.0"), activeByDefault: false}
	if err := r.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ctx := &Context{DB: db, Logger: logger}
	if err := r.InitAll(ctx); err != nil {
		t.Fatalf("InitAll: %v", err)
	}

	if r.IsActive("opt-in") {
		t.Error("opt-in module must be inactive after first registration")
	}
	if m.initCalled {
		t.Error("opt-in module must not be initialized while inactive")
	}
	row, err := store.New(db).GetModule(context.Background(), "opt-in")
	if err != nil {
		t.Fatalf("GetModule: %v", err)
	}
	if row.IsActive {
		t.Error("opt-in module row must be persisted with is_active=0")
	}

	if err := r.SetActive("opt-in", true); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if !m.initCalled {
		t.Error("activating an opt-in module must initialize it")
	}

	// A later start keeps the administrator's choice instead of the default.
	r2 := NewRegistry(logger)
	m2 := &optInModule{mockModule: newMockModule("opt-in", "1.0.0"), activeByDefault: false}
	if err := r2.Register(m2); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r2.InitAll(ctx); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	if !r2.IsActive("opt-in") {
		t.Error("restart must keep an opt-in module that an administrator activated")
	}
	if !m2.initCalled {
		t.Error("restart must initialize an activated opt-in module")
	}
}

// TestActivationDefaulterActiveByDefaultTrue verifies a module that implements
// the interface but returns true behaves like an ordinary module.
func TestActivationDefaulterActiveByDefaultTrue(t *testing.T) {
	logger := testutil.TestLoggerSilent()
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	r := NewRegistry(logger)
	m := &optInModule{mockModule: newMockModule("default-on", "1.0.0"), activeByDefault: true}
	if err := r.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.InitAll(&Context{DB: db, Logger: logger}); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	if !r.IsActive("default-on") || !m.initCalled {
		t.Error("a module returning ActiveByDefault()=true must be active and initialized")
	}
}
