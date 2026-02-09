// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/robfig/cron/v3"

	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/scheduler"
)

// testSchedulerLogger creates a test logger that discards output.
func testSchedulerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// testSchedulerRegistry creates a test scheduler registry with an in-memory database.
func testSchedulerRegistry(t *testing.T) (*scheduler.Registry, *sql.DB) {
	t.Helper()

	db := testDB(t)
	logger := testSchedulerLogger()
	registry := scheduler.NewRegistry(db, logger)

	return registry, db
}

// testSchedulerHandler creates a SchedulerHandler with test dependencies.
func testSchedulerHandler(t *testing.T) (*SchedulerHandler, *scheduler.Registry, *cron.Cron, *scs.SessionManager) {
	t.Helper()

	registry, db := testSchedulerRegistry(t)

	// Create a minimal renderer for testing
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{
		TemplatesFS:    os.DirFS("../../web/templates"),
		SessionManager: sm,
		DB:             db,
		IsDev:          true,
	})
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	// Create a cron instance for registering test jobs
	cronInst := cron.New()
	cronInst.Start()
	t.Cleanup(func() {
		cronInst.Stop()
	})

	handler := NewSchedulerHandler(db, renderer, registry, nil, nil)

	return handler, registry, cronInst, sm
}

func TestNewSchedulerHandler(t *testing.T) {
	registry, _ := testSchedulerRegistry(t)

	handler := NewSchedulerHandler(nil, nil, registry, nil, nil)

	if handler == nil {
		t.Fatal("NewSchedulerHandler returned nil")
	}
	if handler.registry != registry {
		t.Error("registry not set correctly")
	}
}

func TestSchedulerList(t *testing.T) {
	handler, registry, cronInst, sm := testSchedulerHandler(t)

	// Register a test job
	jobFunc := func() {}
	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job description", "@every 1h", cronInst, entryID, jobFunc, nil)

	// Create request with session
	req := httptest.NewRequest(http.MethodGet, "/admin/scheduler", nil)
	req = requestWithSession(sm, req)
	rec := httptest.NewRecorder()

	// Call handler
	handler.List(rec, req)

	// Verify response
	if rec.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestSchedulerUpdateSchedule(t *testing.T) {
	handler, registry, cronInst, sm := testSchedulerHandler(t)

	// Register a test job
	jobFunc := func() {}
	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", "@every 1h", cronInst, entryID, jobFunc, nil)

	// Create form data
	formData := url.Values{}
	formData.Set("source", "core")
	formData.Set("name", "test-job")
	formData.Set("schedule", "@every 30m")

	// Create request with session
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/update", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithSession(sm, req)
	rec := httptest.NewRecorder()

	// Call handler
	handler.UpdateSchedule(rec, req)

	// Verify redirect
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// Verify schedule was updated
	jobs := registry.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Schedule != "@every 30m" {
		t.Errorf("job.Schedule = %q, want %q", jobs[0].Schedule, "@every 30m")
	}
	if !jobs[0].IsOverridden {
		t.Error("job.IsOverridden should be true")
	}
}

func TestSchedulerUpdateScheduleInvalid(t *testing.T) {
	handler, registry, cronInst, sm := testSchedulerHandler(t)

	// Register a test job
	jobFunc := func() {}
	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", "@every 1h", cronInst, entryID, jobFunc, nil)

	// Create form data with invalid cron expression
	formData := url.Values{}
	formData.Set("source", "core")
	formData.Set("name", "test-job")
	formData.Set("schedule", "invalid cron")

	// Create request with session
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/update", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithSession(sm, req)
	rec := httptest.NewRecorder()

	// Call handler
	handler.UpdateSchedule(rec, req)

	// Verify redirect with error
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// Verify schedule was NOT updated
	jobs := registry.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Schedule != "@every 1h" {
		t.Errorf("job.Schedule = %q, want %q (should be unchanged)", jobs[0].Schedule, "@every 1h")
	}
}

func TestSchedulerUpdateScheduleMissingFields(t *testing.T) {
	handler, _, _, sm := testSchedulerHandler(t)

	tests := []struct {
		name     string
		source   string
		jobName  string
		schedule string
	}{
		{"missing source", "", "test-job", "@every 30m"},
		{"missing name", "core", "", "@every 30m"},
		{"missing schedule", "core", "test-job", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formData := url.Values{}
			if tt.source != "" {
				formData.Set("source", tt.source)
			}
			if tt.jobName != "" {
				formData.Set("name", tt.jobName)
			}
			if tt.schedule != "" {
				formData.Set("schedule", tt.schedule)
			}

			req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/update", strings.NewReader(formData.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = requestWithSession(sm, req)
			rec := httptest.NewRecorder()

			handler.UpdateSchedule(rec, req)

			// Verify redirect with error
			if rec.Code != http.StatusSeeOther {
				t.Errorf("status code = %d, want %d", rec.Code, http.StatusSeeOther)
			}
		})
	}
}

func TestSchedulerResetSchedule(t *testing.T) {
	handler, registry, cronInst, sm := testSchedulerHandler(t)

	// Register a test job
	jobFunc := func() {}
	defaultSchedule := "@every 1h"
	entryID, err := cronInst.AddFunc(defaultSchedule, jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", defaultSchedule, cronInst, entryID, jobFunc, nil)

	// First, update the schedule
	err = registry.UpdateSchedule("core", "test-job", "@every 30m")
	if err != nil {
		t.Fatalf("UpdateSchedule failed: %v", err)
	}

	// Verify it's overridden
	jobs := registry.List()
	if !jobs[0].IsOverridden {
		t.Fatal("job should be overridden before reset")
	}

	// Create form data for reset
	formData := url.Values{}
	formData.Set("source", "core")
	formData.Set("name", "test-job")

	// Create request with session
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/reset", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithSession(sm, req)
	rec := httptest.NewRecorder()

	// Call handler
	handler.ResetSchedule(rec, req)

	// Verify redirect
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// Verify schedule was reset
	jobs = registry.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Schedule != defaultSchedule {
		t.Errorf("job.Schedule = %q, want %q", jobs[0].Schedule, defaultSchedule)
	}
	if jobs[0].IsOverridden {
		t.Error("job.IsOverridden should be false after reset")
	}
}

func TestSchedulerResetScheduleMissingFields(t *testing.T) {
	handler, _, _, sm := testSchedulerHandler(t)

	tests := []struct {
		name    string
		source  string
		jobName string
	}{
		{"missing source", "", "test-job"},
		{"missing name", "core", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formData := url.Values{}
			if tt.source != "" {
				formData.Set("source", tt.source)
			}
			if tt.jobName != "" {
				formData.Set("name", tt.jobName)
			}

			req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/reset", strings.NewReader(formData.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = requestWithSession(sm, req)
			rec := httptest.NewRecorder()

			handler.ResetSchedule(rec, req)

			// Verify redirect with error
			if rec.Code != http.StatusSeeOther {
				t.Errorf("status code = %d, want %d", rec.Code, http.StatusSeeOther)
			}
		})
	}
}

func TestSchedulerTriggerNow(t *testing.T) {
	handler, registry, cronInst, sm := testSchedulerHandler(t)

	// Register a test job with trigger function
	triggered := false
	jobFunc := func() {}
	triggerFunc := func() error {
		triggered = true
		return nil
	}

	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", "@every 1h", cronInst, entryID, jobFunc, triggerFunc)

	// Create request with URL params and session
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/trigger/core/test-job", nil)
	req = requestWithURLParams(req, map[string]string{
		"source": "core",
		"name":   "test-job",
	})
	req = requestWithSession(sm, req)
	rec := httptest.NewRecorder()

	// Call handler
	handler.TriggerNow(rec, req)

	// Verify redirect
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// Verify job was triggered
	if !triggered {
		t.Error("job was not triggered")
	}
}

func TestSchedulerTriggerNowNoTriggerFunc(t *testing.T) {
	handler, registry, cronInst, sm := testSchedulerHandler(t)

	// Register a test job WITHOUT trigger function
	jobFunc := func() {}

	entryID, err := cronInst.AddFunc("@every 1h", jobFunc)
	if err != nil {
		t.Fatalf("failed to add cron job: %v", err)
	}

	registry.Register("core", "test-job", "Test job", "@every 1h", cronInst, entryID, jobFunc, nil)

	// Create request with URL params and session
	req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/trigger/core/test-job", nil)
	req = requestWithURLParams(req, map[string]string{
		"source": "core",
		"name":   "test-job",
	})
	req = requestWithSession(sm, req)
	rec := httptest.NewRecorder()

	// Call handler
	handler.TriggerNow(rec, req)

	// Verify redirect with error
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

func TestSchedulerTriggerNowMissingParams(t *testing.T) {
	handler, _, _, sm := testSchedulerHandler(t)

	tests := []struct {
		name    string
		source  string
		jobName string
	}{
		{"missing source", "", "test-job"},
		{"missing name", "core", ""},
		{"both missing", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/scheduler/trigger/"+tt.source+"/"+tt.jobName, nil)
			req = requestWithURLParams(req, map[string]string{
				"source": tt.source,
				"name":   tt.jobName,
			})
			req = requestWithSession(sm, req)
			rec := httptest.NewRecorder()

			handler.TriggerNow(rec, req)

			// Verify redirect with error
			if rec.Code != http.StatusSeeOther {
				t.Errorf("status code = %d, want %d", rec.Code, http.StatusSeeOther)
			}
		})
	}
}

func TestSchedulerJobView(t *testing.T) {
	// Test the SchedulerJobView struct initialization
	view := SchedulerJobView{
		Source:          "core",
		Name:            "test-job",
		Description:     "Test job description",
		DefaultSchedule: "@every 1h",
		Schedule:        "@every 30m",
		IsOverridden:    true,
		LastRun:         "2025-01-01 12:00:00",
		NextRun:         "2025-01-01 13:00:00",
		CanTrigger:      true,
	}

	if view.Source != "core" {
		t.Errorf("Source = %q, want %q", view.Source, "core")
	}
	if view.Name != "test-job" {
		t.Errorf("Name = %q, want %q", view.Name, "test-job")
	}
	if !view.IsOverridden {
		t.Error("IsOverridden should be true")
	}
	if !view.CanTrigger {
		t.Error("CanTrigger should be true")
	}
}
