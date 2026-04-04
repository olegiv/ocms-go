// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestNewTaskExecutor_UsesSSRFSafeDialer(t *testing.T) {
	executor := NewTaskExecutor(nil, slog.Default(), nil, cron.New())

	transport, ok := executor.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", executor.httpClient.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("expected custom DialContext for SSRF protection")
	}

	_, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:80")
	if err == nil {
		t.Fatal("expected loopback connection to be blocked")
	}
	if !strings.Contains(err.Error(), "private IP") {
		t.Fatalf("expected private IP block error, got: %v", err)
	}
}

func TestNewTaskExecutor_Fields(t *testing.T) {
	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()

	te := NewTaskExecutor(nil, logger, nil, cronInst)

	if te == nil {
		t.Fatal("NewTaskExecutor returned nil")
	}
	if te.httpClient == nil {
		t.Error("httpClient should be initialized")
	}
	if te.taskEntries == nil {
		t.Error("taskEntries map should be initialized")
	}
	if te.triggerLimiters == nil {
		t.Error("triggerLimiters map should be initialized")
	}
	if te.cronInst != cronInst {
		t.Error("cronInst not set correctly")
	}
	if te.logger != logger {
		t.Error("logger not set correctly")
	}
}

func TestTaskName(t *testing.T) {
	tests := []struct {
		taskID int64
		want   string
	}{
		{1, "task_1"},
		{42, "task_42"},
		{0, "task_0"},
		{999, "task_999"},
	}

	for _, tt := range tests {
		got := taskName(tt.taskID)
		if got != tt.want {
			t.Errorf("taskName(%d) = %q, want %q", tt.taskID, got, tt.want)
		}
	}
}

func TestAddTask(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	te := NewTaskExecutor(db, logger, nil, cronInst)

	task := store.ScheduledTask{
		ID:             1,
		Name:           "test-task",
		Url:            "https://example.com/health",
		Schedule:       "@every 1h",
		IsActive:       1,
		TimeoutSeconds: 30,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err := te.AddTask(task)
	if err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}

	te.mu.Lock()
	_, ok := te.taskEntries[task.ID]
	te.mu.Unlock()

	if !ok {
		t.Error("task entry should be present after AddTask()")
	}
}

func TestAddTask_InvalidSchedule(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()

	te := NewTaskExecutor(db, logger, nil, cronInst)

	task := store.ScheduledTask{
		ID:       1,
		Name:     "bad-task",
		Url:      "https://example.com/health",
		Schedule: "not a valid cron",
	}

	err := te.AddTask(task)
	if err == nil {
		t.Fatal("AddTask() should error on invalid schedule")
	}
}

func TestRemoveTask(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	te := NewTaskExecutor(db, logger, nil, cronInst)

	task := store.ScheduledTask{
		ID:             1,
		Name:           "test-task",
		Url:            "https://example.com/health",
		Schedule:       "@every 1h",
		IsActive:       1,
		TimeoutSeconds: 30,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := te.AddTask(task); err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}

	// Verify task is present
	te.mu.Lock()
	_, ok := te.taskEntries[task.ID]
	te.mu.Unlock()
	if !ok {
		t.Fatal("task should be present after AddTask()")
	}

	// Remove the task
	te.RemoveTask(task.ID)

	// Verify task is gone
	te.mu.Lock()
	_, ok = te.taskEntries[task.ID]
	te.mu.Unlock()
	if ok {
		t.Error("task should not be present after RemoveTask()")
	}
}

func TestRemoveTask_NonExistent(t *testing.T) {
	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()

	te := NewTaskExecutor(nil, logger, nil, cronInst)

	// Should not panic when removing a task that doesn't exist
	te.RemoveTask(9999)
}

func TestRescheduleTask(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	te := NewTaskExecutor(db, logger, nil, cronInst)

	task := store.ScheduledTask{
		ID:             1,
		Name:           "test-task",
		Url:            "https://example.com/health",
		Schedule:       "@every 1h",
		IsActive:       1,
		TimeoutSeconds: 30,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := te.AddTask(task); err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}

	// Record original entry ID
	te.mu.Lock()
	origEntry := te.taskEntries[task.ID]
	te.mu.Unlock()

	// Reschedule with a different schedule
	task.Schedule = "@every 30m"
	err := te.RescheduleTask(task)
	if err != nil {
		t.Fatalf("RescheduleTask() error = %v", err)
	}

	// Verify new entry ID is different (a new cron entry was added)
	te.mu.Lock()
	newEntry := te.taskEntries[task.ID]
	te.mu.Unlock()

	if newEntry == origEntry {
		t.Error("expected a different cron entry ID after rescheduling")
	}
}

func TestLoadAndScheduleAll_Empty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	te := NewTaskExecutor(db, logger, nil, cronInst)

	err := te.LoadAndScheduleAll()
	if err != nil {
		t.Fatalf("LoadAndScheduleAll() with empty table error = %v", err)
	}

	te.mu.Lock()
	count := len(te.taskEntries)
	te.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 tasks, got %d", count)
	}
}

func TestLoadAndScheduleAll_WithTasks(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	// Insert active tasks
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 3; i++ {
		_, err := queries.CreateScheduledTask(ctx, store.CreateScheduledTaskParams{
			Name:           "task",
			Url:            "https://example.com/health",
			Schedule:       "@every 1h",
			IsActive:       1,
			TimeoutSeconds: 30,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if err != nil {
			t.Fatalf("CreateScheduledTask() error = %v", err)
		}
	}

	// Insert one inactive task (should not be scheduled)
	_, err := queries.CreateScheduledTask(ctx, store.CreateScheduledTaskParams{
		Name:           "inactive-task",
		Url:            "https://example.com/inactive",
		Schedule:       "@every 1h",
		IsActive:       0,
		TimeoutSeconds: 30,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("CreateScheduledTask() error = %v", err)
	}

	te := NewTaskExecutor(db, logger, nil, cronInst)
	err = te.LoadAndScheduleAll()
	if err != nil {
		t.Fatalf("LoadAndScheduleAll() error = %v", err)
	}

	te.mu.Lock()
	count := len(te.taskEntries)
	te.mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 active tasks scheduled, got %d", count)
	}
}

func TestRegisterCleanupJob_WithRegistry(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	registry := NewRegistry(db, logger)
	te := NewTaskExecutor(db, logger, registry, cronInst)

	te.RegisterCleanupJob()

	// Verify cleanup job is registered
	jobs := registry.List()
	found := false
	for _, job := range jobs {
		if job.Source == "task" && job.Name == "cleanup" {
			found = true
			if !job.CanTrigger {
				t.Error("cleanup job should have a trigger function")
			}
			if job.Description == "" {
				t.Error("cleanup job description should not be empty")
			}
		}
	}
	if !found {
		t.Error("cleanup job not found in registry after RegisterCleanupJob()")
	}
}

func TestRegisterCleanupJob_WithoutRegistry(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	te := NewTaskExecutor(db, logger, nil, cronInst)

	// Should not panic when no registry is set
	te.RegisterCleanupJob()
}

func TestAddTask_WithRegistry(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	registry := NewRegistry(db, logger)
	te := NewTaskExecutor(db, logger, registry, cronInst)

	task := store.ScheduledTask{
		ID:             42,
		Name:           "registry-task",
		Url:            "https://example.com/health",
		Schedule:       "@every 1h",
		IsActive:       1,
		TimeoutSeconds: 30,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err := te.AddTask(task)
	if err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}

	// Verify task is in the registry
	jobs := registry.List()
	found := false
	for _, job := range jobs {
		if job.Source == "task" && job.Name == "task_42" {
			found = true
			if !job.CanTrigger {
				t.Error("task job should have a trigger function")
			}
		}
	}
	if !found {
		t.Error("task not found in registry after AddTask()")
	}
}

func TestRemoveTask_WithRegistry(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	registry := NewRegistry(db, logger)
	te := NewTaskExecutor(db, logger, registry, cronInst)

	task := store.ScheduledTask{
		ID:             10,
		Name:           "removable-task",
		Url:            "https://example.com/health",
		Schedule:       "@every 1h",
		IsActive:       1,
		TimeoutSeconds: 30,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := te.AddTask(task); err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}

	// Verify registered
	if len(registry.List()) == 0 {
		t.Fatal("expected task in registry after AddTask()")
	}

	// Remove the task
	te.RemoveTask(task.ID)

	// Verify removed from registry
	jobs := registry.List()
	for _, job := range jobs {
		if job.Source == "task" && job.Name == "task_10" {
			t.Error("task should not be in registry after RemoveTask()")
		}
	}
}

func TestTriggerTask_NotFound(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()

	te := NewTaskExecutor(db, logger, nil, cronInst)

	err := te.TriggerTask(9999)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
	if !strings.Contains(err.Error(), "task not found") {
		t.Errorf("expected 'task not found' error, got: %v", err)
	}
}

func TestTriggerTask_RateLimit(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	logger := testutil.TestLoggerSilent()
	cronInst := cron.New()
	cronInst.Start()
	defer cronInst.Stop()

	te := NewTaskExecutor(db, logger, nil, cronInst)

	// Create a task in the DB
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	task, err := queries.CreateScheduledTask(ctx, store.CreateScheduledTaskParams{
		Name:           "rate-limited-task",
		Url:            "https://example.com/health",
		Schedule:       "@every 1h",
		IsActive:       1,
		TimeoutSeconds: 30,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("CreateScheduledTask() error = %v", err)
	}

	// First trigger should succeed (goes async, but rate limiter allows it)
	err = te.TriggerTask(task.ID)
	if err != nil {
		t.Fatalf("first TriggerTask() should succeed, got: %v", err)
	}

	// Second immediate trigger should be rate-limited
	err = te.TriggerTask(task.ID)
	if err == nil {
		t.Fatal("expected rate limit error on second immediate trigger")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestNewTaskExecutor_CheckRedirectBlocksPrivate(t *testing.T) {
	executor := NewTaskExecutor(nil, slog.Default(), nil, cron.New())

	// Simulate a redirect to a private IP
	privateReq, err := http.NewRequest(http.MethodGet, "http://192.168.1.1/secret", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Pass through the CheckRedirect function
	via := []*http.Request{{}}
	redirectErr := executor.httpClient.CheckRedirect(privateReq, via)
	if redirectErr == nil {
		t.Fatal("expected redirect to private IP to be blocked")
	}
	if !strings.Contains(redirectErr.Error(), "redirect blocked") {
		t.Errorf("expected 'redirect blocked' error, got: %v", redirectErr)
	}
}

func TestNewTaskExecutor_CheckRedirectTooMany(t *testing.T) {
	executor := NewTaskExecutor(nil, slog.Default(), nil, cron.New())

	// Simulate a redirect to a public URL but with too many hops
	req, err := http.NewRequest(http.MethodGet, "https://example.com/page", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Build a chain of 10 previous requests (at limit)
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = &http.Request{}
	}

	redirectErr := executor.httpClient.CheckRedirect(req, via)
	if redirectErr == nil {
		t.Fatal("expected error for too many redirects")
	}
	if !strings.Contains(redirectErr.Error(), "too many redirects") {
		t.Errorf("expected 'too many redirects' error, got: %v", redirectErr)
	}
}
