// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron/v3"
)

// testDB creates an in-memory SQLite database for testing.
func testDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

// testLogger creates a test logger that discards output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewRegistry(t *testing.T) {
	db := testDB(t)
	logger := testLogger()

	registry := NewRegistry(db, logger)

	if registry == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if registry.db != db {
		t.Error("registry.db not set correctly")
	}
	if registry.logger != logger {
		t.Error("registry.logger not set correctly")
	}
	if registry.jobs == nil {
		t.Error("registry.jobs should be initialized")
	}

	// Verify table was created
	var tableName string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='scheduler_overrides'").Scan(&tableName)
	if err != nil {
		t.Fatalf("scheduler_overrides table not created: %v", err)
	}
	if tableName != "scheduler_overrides" {
		t.Errorf("table name = %q, want %q", tableName, "scheduler_overrides")
	}
}

func TestRegister(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	defer cronInst.Stop()

	jobFunc := func() {}

	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job description", "@every 1h", cronInst, entryID, jobFunc, nil)

	// Verify job is registered
	jobs := registry.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	job := jobs[0]
	if job.Source != "core" {
		t.Errorf("job.Source = %q, want %q", job.Source, "core")
	}
	if job.Name != "test-job" {
		t.Errorf("job.Name = %q, want %q", job.Name, "test-job")
	}
	if job.Description != "Test job description" {
		t.Errorf("job.Description = %q, want %q", job.Description, "Test job description")
	}
	if job.DefaultSchedule != "@every 1h" {
		t.Errorf("job.DefaultSchedule = %q, want %q", job.DefaultSchedule, "@every 1h")
	}
	if job.Schedule != "@every 1h" {
		t.Errorf("job.Schedule = %q, want %q", job.Schedule, "@every 1h")
	}
	if job.IsOverridden {
		t.Error("job.IsOverridden should be false")
	}
	if job.CanTrigger {
		t.Error("job.CanTrigger should be false (triggerFunc is nil)")
	}
}

func TestUpdateSchedule(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	callCount := 0
	jobFunc := func() {
		callCount++
	}

	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", "@every 1h", cronInst, entryID, jobFunc, nil)

	// Update schedule
	newSchedule := "@every 30m"
	err = registry.UpdateSchedule("core", "test-job", newSchedule)
	if err != nil {
		t.Fatalf("UpdateSchedule failed: %v", err)
	}

	// Verify updated schedule in registry
	jobs := registry.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	job := jobs[0]
	if job.Schedule != newSchedule {
		t.Errorf("job.Schedule = %q, want %q", job.Schedule, newSchedule)
	}
	if !job.IsOverridden {
		t.Error("job.IsOverridden should be true after update")
	}

	// Verify override persisted to DB
	var overrideSchedule string
	err = db.QueryRow("SELECT override_schedule FROM scheduler_overrides WHERE source = ? AND name = ?", "core", "test-job").Scan(&overrideSchedule)
	if err != nil {
		t.Fatalf("failed to query override: %v", err)
	}
	if overrideSchedule != newSchedule {
		t.Errorf("DB override_schedule = %q, want %q", overrideSchedule, newSchedule)
	}
}

func TestResetSchedule(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	jobFunc := func() {}

	defaultSchedule := "@every 1h"
	entryID, err := cronInst.AddFunc(defaultSchedule, jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", defaultSchedule, cronInst, entryID, jobFunc, nil)

	// First, update the schedule
	newSchedule := "@every 30m"
	err = registry.UpdateSchedule("core", "test-job", newSchedule)
	if err != nil {
		t.Fatalf("UpdateSchedule failed: %v", err)
	}

	// Verify override exists
	jobs := registry.List()
	if !jobs[0].IsOverridden {
		t.Fatal("job should be overridden before reset")
	}

	// Reset schedule
	err = registry.ResetSchedule("core", "test-job")
	if err != nil {
		t.Fatalf("ResetSchedule failed: %v", err)
	}

	// Verify schedule reset to default
	jobs = registry.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	job := jobs[0]
	if job.Schedule != defaultSchedule {
		t.Errorf("job.Schedule = %q, want %q", job.Schedule, defaultSchedule)
	}
	if job.IsOverridden {
		t.Error("job.IsOverridden should be false after reset")
	}

	// Verify override removed from DB
	var overrideSchedule string
	err = db.QueryRow("SELECT override_schedule FROM scheduler_overrides WHERE source = ? AND name = ?", "core", "test-job").Scan(&overrideSchedule)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v (override should be deleted)", err)
	}
}

func TestTriggerNow(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	defer cronInst.Stop()

	jobFunc := func() {}

	triggered := false
	triggerFunc := func() error {
		triggered = true
		return nil
	}

	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", "@every 1h", cronInst, entryID, jobFunc, triggerFunc)

	// Trigger the job
	err = registry.TriggerNow("core", "test-job")
	if err != nil {
		t.Fatalf("TriggerNow failed: %v", err)
	}

	if !triggered {
		t.Error("job was not triggered")
	}
}

func TestTriggerNowNotFound(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	err := registry.TriggerNow("core", "nonexistent-job")
	if err == nil {
		t.Error("expected error for nonexistent job")
	}
	if err.Error() != "job not found: core:nonexistent-job" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestTriggerNowNoTriggerFunc(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	defer cronInst.Stop()

	jobFunc := func() {}

	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	// Register without trigger function
	registry.Register("core", "test-job", "Test job", "@every 1h", cronInst, entryID, jobFunc, nil)

	err = registry.TriggerNow("core", "test-job")
	if err == nil {
		t.Error("expected error when trigger function is nil")
	}
	if err.Error() != "manual trigger not available for: core:test-job" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestGetEffectiveSchedule(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	// Test default schedule (no override)
	schedule := registry.GetEffectiveSchedule("core", "test-job", "@every 1h")
	if schedule != "@every 1h" {
		t.Errorf("GetEffectiveSchedule = %q, want %q", schedule, "@every 1h")
	}

	// Add an override
	_, err := db.Exec("INSERT INTO scheduler_overrides (source, name, override_schedule) VALUES (?, ?, ?)",
		"core", "test-job", "@every 30m")
	if err != nil {
		t.Fatalf("failed to insert override: %v", err)
	}

	// Test override schedule
	schedule = registry.GetEffectiveSchedule("core", "test-job", "@every 1h")
	if schedule != "@every 30m" {
		t.Errorf("GetEffectiveSchedule = %q, want %q", schedule, "@every 30m")
	}
}

func TestUpdateScheduleInvalidCron(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	jobFunc := func() {}

	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", "@every 1h", cronInst, entryID, jobFunc, nil)

	// Try to update with invalid cron expression
	err = registry.UpdateSchedule("core", "test-job", "invalid cron expression")
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}

	// Verify original schedule is unchanged
	jobs := registry.List()
	if jobs[0].Schedule != "@every 1h" {
		t.Errorf("schedule should remain unchanged, got %q", jobs[0].Schedule)
	}
}

func TestListSorted(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	defer cronInst.Stop()

	jobFunc := func() {}

	// Register jobs in mixed order
	jobs := []struct {
		source string
		name   string
	}{
		{"module-b", "job-2"},
		{"core", "job-z"},
		{"module-a", "job-1"},
		{"core", "job-a"},
		{"module-b", "job-1"},
	}

	for _, j := range jobs {
		entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
		if err != nil {
			t.Fatalf("failed to add cron job: %v", err)
		}
		registry.Register(j.source, j.name, "Test job", "@every 1h", cronInst, entryID, jobFunc, nil)
	}

	// Verify sorting by source then name
	result := registry.List()
	if len(result) != 5 {
		t.Fatalf("expected 5 jobs, got %d", len(result))
	}

	expected := []struct {
		source string
		name   string
	}{
		{"core", "job-a"},
		{"core", "job-z"},
		{"module-a", "job-1"},
		{"module-b", "job-1"},
		{"module-b", "job-2"},
	}

	for i, exp := range expected {
		if result[i].Source != exp.source || result[i].Name != exp.name {
			t.Errorf("result[%d] = %s:%s, want %s:%s", i, result[i].Source, result[i].Name, exp.source, exp.name)
		}
	}
}

func TestRegistryTimingInfo(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	jobFunc := func() {}

	// Use a schedule that will run in the future
	entryID, err := cronInst.AddFunc("0 0 * * *", jobFunc) // Daily at midnight
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", "0 0 * * *", cronInst, entryID, jobFunc, nil)

	// Give cron time to schedule
	time.Sleep(10 * time.Millisecond)

	jobs := registry.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	job := jobs[0]

	// NextRun should be in the future
	if job.NextRun.IsZero() {
		t.Error("job.NextRun should not be zero")
	}
	if !job.NextRun.After(time.Now()) {
		t.Error("job.NextRun should be in the future")
	}

	// LastRun should be zero (job hasn't run yet)
	if !job.LastRun.IsZero() {
		t.Error("job.LastRun should be zero (job hasn't run yet)")
	}
}

func TestResetScheduleAlreadyDefault(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	cronInst := cron.New()
	defer cronInst.Stop()

	jobFunc := func() {}

	defaultSchedule := "@every 1h"
	entryID, err := cronInst.AddFunc(defaultSchedule, jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", defaultSchedule, cronInst, entryID, jobFunc, nil)

	// Reset when already at default should succeed with no changes
	err = registry.ResetSchedule("core", "test-job")
	if err != nil {
		t.Errorf("ResetSchedule should succeed when already at default: %v", err)
	}
}

func TestUpdateScheduleNotFound(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	err := registry.UpdateSchedule("core", "nonexistent-job", "@every 30m")
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
	if err.Error() != "job not found: core:nonexistent-job" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestResetScheduleNotFound(t *testing.T) {
	db := testDB(t)
	logger := testLogger()
	registry := NewRegistry(db, logger)

	err := registry.ResetSchedule("core", "nonexistent-job")
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
	if err.Error() != "job not found: core:nonexistent-job" {
		t.Errorf("unexpected error message: %v", err)
	}
}
