// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package module

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// slowInitModule is an opt-in mock whose Init takes long enough for
// concurrent activations to overlap, and counts how often it runs.
type slowInitModule struct {
	*optInModule
	inits atomic.Int32
}

func (m *slowInitModule) Init(*Context) error {
	m.inits.Add(1)
	time.Sleep(20 * time.Millisecond)
	return nil
}

// TestSetActiveInitializesOnce verifies concurrent activations of a module
// that was never initialized run Init exactly once. Opt-in modules are always
// initialized this way, so a double-click or two admins toggling at once must
// not initialize them twice.
func TestSetActiveInitializesOnce(t *testing.T) {
	logger := testutil.TestLoggerSilent()
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	r := NewRegistry(logger)
	m := &slowInitModule{optInModule: &optInModule{mockModule: newMockModule("slow", "1.0.0")}}
	if err := r.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.InitAll(&Context{DB: db, Logger: logger}); err != nil {
		t.Fatalf("InitAll: %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := r.SetActive("slow", true); err != nil {
				t.Errorf("SetActive: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := m.inits.Load(); got != 1 {
		t.Errorf("Init ran %d times, want exactly 1", got)
	}
	if !r.IsActive("slow") {
		t.Error("module must be active after activation")
	}
}

// failingUpdateModule renames the modules table during its first Init, so the
// status update that follows a successful Init fails.
type failingUpdateModule struct {
	*optInModule
	inits atomic.Int32
}

func (m *failingUpdateModule) Init(ctx *Context) error {
	if m.inits.Add(1) == 1 {
		if _, err := ctx.DB.Exec(`ALTER TABLE modules RENAME TO modules_gone`); err != nil {
			return err
		}
	}
	return nil
}

// TestSetActiveRetryDoesNotReinitialize verifies a module whose Init succeeded
// is not initialized again when the status update after it failed and the
// administrator retries.
func TestSetActiveRetryDoesNotReinitialize(t *testing.T) {
	logger := testutil.TestLoggerSilent()
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	r := NewRegistry(logger)
	m := &failingUpdateModule{optInModule: &optInModule{mockModule: newMockModule("flaky", "1.0.0")}}
	if err := r.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.InitAll(&Context{DB: db, Logger: logger}); err != nil {
		t.Fatalf("InitAll: %v", err)
	}

	if err := r.SetActive("flaky", true); err == nil {
		t.Fatal("the status update must fail once the modules table is gone")
	}
	if err := r.SetActive("flaky", true); err == nil {
		t.Fatal("the retry must fail on the status update too")
	}
	if got := m.inits.Load(); got != 1 {
		t.Errorf("Init ran %d times, want 1", got)
	}
}
