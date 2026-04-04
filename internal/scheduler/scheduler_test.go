// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"log/slog"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestNew(t *testing.T) {
	logger := slog.Default()

	// Test creation without database (nil db allowed for creation)
	s := New(nil, logger, nil)
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.cron == nil {
		t.Error("New() scheduler has nil cron")
	}
	if s.logger != logger {
		t.Error("New() scheduler has wrong logger")
	}
}

func TestScheduler_StartStop(t *testing.T) {
	logger := slog.Default()
	s := New(nil, logger, nil)

	// Start the scheduler
	err := s.Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Stop the scheduler
	s.Stop()

	// Starting and stopping should work without panic
}

func TestScheduler_Cron(t *testing.T) {
	logger := testutil.TestLoggerSilent()
	s := New(nil, logger, nil)

	c := s.Cron()
	if c == nil {
		t.Fatal("Cron() returned nil")
	}
	if c != s.cron {
		t.Error("Cron() returned wrong cron instance")
	}
}

func TestScheduler_StartWithRegistry(t *testing.T) {
	db := testDB(t)
	logger := testutil.TestLoggerSilent()
	registry := NewRegistry(db, logger)

	s := New(nil, logger, registry)
	err := s.Start()
	if err != nil {
		t.Fatalf("Start() with registry error = %v", err)
	}
	defer s.Stop()

	// The scheduled_pages job should be registered in the registry
	jobs := registry.List()
	if len(jobs) == 0 {
		t.Fatal("expected at least one job registered after Start()")
	}

	found := false
	for _, job := range jobs {
		if job.Source == "core" && job.Name == "scheduled_pages" {
			found = true
			if job.Description == "" {
				t.Error("job description should not be empty")
			}
			if job.DefaultSchedule == "" {
				t.Error("job default schedule should not be empty")
			}
			if !job.CanTrigger {
				t.Error("scheduled_pages job should have a trigger function")
			}
		}
	}
	if !found {
		t.Error("scheduled_pages job not found in registry after Start()")
	}
}

func TestScheduler_AddDemoReset(t *testing.T) {
	db := testDB(t)
	logger := testutil.TestLoggerSilent()
	registry := NewRegistry(db, logger)

	s := New(nil, logger, registry)
	// Start cron so AddFunc works correctly
	s.cron.Start()
	defer s.Stop()

	err := s.AddDemoReset("/tmp/test.db", "/tmp/uploads", "/tmp/data")
	if err != nil {
		t.Fatalf("AddDemoReset() error = %v", err)
	}

	// The demo_reset job should be registered
	jobs := registry.List()
	found := false
	for _, job := range jobs {
		if job.Source == "core" && job.Name == "demo_reset" {
			found = true
			if job.Description == "" {
				t.Error("demo_reset job description should not be empty")
			}
			// Demo reset should NOT have a trigger function (sends SIGTERM)
			if job.CanTrigger {
				t.Error("demo_reset job should not have a manual trigger (SIGTERM risk)")
			}
		}
	}
	if !found {
		t.Error("demo_reset job not found in registry after AddDemoReset()")
	}
}

func TestScheduler_AddDemoResetWithoutRegistry(t *testing.T) {
	logger := testutil.TestLoggerSilent()

	s := New(nil, logger, nil)
	s.cron.Start()
	defer s.Stop()

	// Should succeed even without a registry
	err := s.AddDemoReset("/tmp/test.db", "/tmp/uploads", "/tmp/data")
	if err != nil {
		t.Fatalf("AddDemoReset() without registry error = %v", err)
	}
}

func TestScheduler_AddDemoResetWithOverride(t *testing.T) {
	db := testDB(t)
	logger := testutil.TestLoggerSilent()
	registry := NewRegistry(db, logger)

	// Insert a schedule override for the demo_reset job
	_, err := db.Exec("INSERT INTO scheduler_overrides (source, name, override_schedule) VALUES (?, ?, ?)",
		"core", "demo_reset", "0 2 * * *")
	if err != nil {
		t.Fatalf("failed to insert override: %v", err)
	}

	s := New(nil, logger, registry)
	s.cron.Start()
	defer s.Stop()

	err = s.AddDemoReset("/tmp/test.db", "/tmp/uploads", "/tmp/data")
	if err != nil {
		t.Fatalf("AddDemoReset() with override error = %v", err)
	}

	// Verify the effective schedule uses the override
	jobs := registry.List()
	for _, job := range jobs {
		if job.Source == "core" && job.Name == "demo_reset" {
			if job.Schedule != "0 2 * * *" {
				t.Errorf("expected overridden schedule %q, got %q", "0 2 * * *", job.Schedule)
			}
			if !job.IsOverridden {
				t.Error("demo_reset job should be marked as overridden")
			}
		}
	}
}
