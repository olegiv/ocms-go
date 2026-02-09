// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"log/slog"
	"testing"
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
