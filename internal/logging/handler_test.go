// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"context"
	"database/sql"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// discardHandler is a slog.Handler that discards all logs.
type discardHandler struct{}

func (h discardHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (h discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (h discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return h }
func (h discardHandler) WithGroup(string) slog.Handler             { return h }

// fetchEvents retrieves events from the database after waiting for async writes.
func fetchEvents(t *testing.T, db *sql.DB, limit int) []store.Event {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	events, err := store.New(db).ListEvents(context.Background(), store.ListEventsParams{
		Limit:  int64(limit),
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	return events
}

// requireSingleEvent fetches events and asserts exactly one was created.
func requireSingleEvent(t *testing.T, db *sql.DB) store.Event {
	t.Helper()
	events := fetchEvents(t, db, 1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	return events[0]
}

func TestEventLogHandler_Handle_ErrorLevel(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Error("database connection failed", "host", "localhost", "port", 5432)

	event := requireSingleEvent(t, db)
	if event.Level != model.EventLevelError {
		t.Errorf("Level = %q, want %q", event.Level, model.EventLevelError)
	}
	if event.Message != "database connection failed" {
		t.Errorf("Message = %q, want %q", event.Message, "database connection failed")
	}
}

func TestEventLogHandler_Handle_WarnLevel(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Warn("slow query detected", "duration_ms", 5000)

	event := requireSingleEvent(t, db)
	if event.Level != model.EventLevelWarning {
		t.Errorf("Level = %q, want %q", event.Level, model.EventLevelWarning)
	}
	if event.Message != "slow query detected" {
		t.Errorf("Message = %q, want %q", event.Message, "slow query detected")
	}
}

func TestEventLogHandler_Handle_InfoLevel_NotCaptured(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Info("server started", "port", 8080) // Should NOT be captured

	events := fetchEvents(t, db, 10)
	if len(events) != 0 {
		t.Errorf("expected 0 events for INFO level, got %d", len(events))
	}
}

func TestEventLogHandler_Handle_DebugLevel_NotCaptured(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Debug("processing request", "request_id", "abc123") // Should NOT be captured

	events := fetchEvents(t, db, 10)
	if len(events) != 0 {
		t.Errorf("expected 0 events for DEBUG level, got %d", len(events))
	}
}

func TestEventLogHandler_Handle_CustomLevel(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandlerWithLevel(discardHandler{}, db, slog.LevelInfo)
	logger := slog.New(handler)

	logger.Info("server started", "port", 8080) // Should be captured with custom level

	events := fetchEvents(t, db, 10)
	if len(events) != 1 {
		t.Errorf("expected 1 event with custom INFO level, got %d", len(events))
	}
}

func TestEventLogHandler_CategoryInference(t *testing.T) {
	testCases := []struct {
		name     string
		message  string
		category string
	}{
		{"auth_failed", "user authentication failed", model.EventCategoryAuth},
		{"login_blocked", "login attempt blocked", model.EventCategoryAuth},
		{"logout", "logout completed", model.EventCategoryAuth},
		{"page", "page not found", model.EventCategoryPage},
		{"cache", "cache invalidation failed", model.EventCategoryCache},
		{"config", "config validation failed", model.EventCategoryConfig},
		{"system_default", "unknown error occurred", model.EventCategorySystem},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			defer cleanup()

			handler := NewEventLogHandler(discardHandler{}, db)
			logger := slog.New(handler)

			logger.Error(tc.message)

			event := requireSingleEvent(t, db)
			if event.Category != tc.category {
				t.Errorf("Category = %q, want %q", event.Category, tc.category)
			}
		})
	}
}

func TestEventLogHandler_ExplicitCategory(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Error("something happened", "category", model.EventCategoryUser) // Explicit category overrides inference

	event := requireSingleEvent(t, db)
	if event.Category != model.EventCategoryUser {
		t.Errorf("Category = %q, want %q (explicit category should override)", event.Category, model.EventCategoryUser)
	}
}

func TestEventLogHandler_MetadataExtraction(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Error("request failed",
		"status_code", 500,
		"path", "/api/users",
		"duration_ms", 1234,
	)

	event := requireSingleEvent(t, db)
	metadata := event.Metadata
	if metadata == "{}" {
		t.Error("Metadata should not be empty")
	}

	for _, key := range []string{"status_code", "path", "duration_ms"} {
		if !contains(metadata, key) {
			t.Errorf("Metadata should contain %q: %s", key, metadata)
		}
	}
}

func TestEventLogHandler_WithAttrs(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	handlerWithAttrs := handler.WithAttrs([]slog.Attr{
		slog.String("service", "api"),
	})

	logger := slog.New(handlerWithAttrs)
	logger.Error("service error")

	event := requireSingleEvent(t, db)
	if event.Message != "service error" {
		t.Errorf("Message = %q, want %q", event.Message, "service error")
	}
}

func TestEventLogHandler_WithGroup(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	handlerWithGroup := handler.WithGroup("request")

	logger := slog.New(handlerWithGroup)
	logger.Error("request error", "id", "abc123")

	event := requireSingleEvent(t, db)
	if event.Message != "request error" {
		t.Errorf("Message = %q, want %q", event.Message, "request error")
	}
}

func TestEventLogHandler_MultipleEvents(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	// Log multiple events
	logger.Error("error 1")
	logger.Warn("warning 1")
	logger.Error("error 2")
	logger.Warn("warning 2")
	logger.Info("info 1") // Should not be captured

	time.Sleep(100 * time.Millisecond)

	q := store.New(db)
	count, err := q.CountEvents(context.Background())
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}

	if count != 4 {
		t.Errorf("expected 4 events (2 errors + 2 warnings), got %d", count)
	}
}

func TestEventLogHandler_SpecialCharactersInMetadata(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Error("parse error",
		"input", `{"key": "value with \"quotes\""}`,
		"path", "C:\\Users\\test",
		"message", "line1\nline2\ttabbed",
	)

	event := requireSingleEvent(t, db)
	if event.Metadata == "" {
		t.Error("Metadata should not be empty")
	}
}

func TestEventLogHandler_WarnRateLimitedByMessage(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	now := time.Now()
	handler.limiter.now = func() time.Time {
		return now
	}

	logger := slog.New(handler)

	logger.Warn("invalid CSRF token", "form_slug", "contact")
	logger.Warn("invalid CSRF token", "form_slug", "contact")

	q := store.New(db)
	count, err := q.CountEvents(context.Background())
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected 1 event after duplicate WARN logs, got %d", count)
	}

	now = now.Add(handler.limiter.interval)
	logger.Warn("invalid CSRF token", "form_slug", "contact")

	count, err = q.CountEvents(context.Background())
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}

	if count != 2 {
		t.Fatalf("expected 2 events after interval elapsed, got %d", count)
	}
}

func TestEscapeJSON(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{`hello`, `hello`},
		{`hello "world"`, `hello \"world\"`},
		{`path\to\file`, `path\\to\\file`},
		{"line1\nline2", `line1\nline2`},
		{"col1\tcol2", `col1\tcol2`},
		{"return\rhere", `return\rhere`},
	}

	for _, tc := range testCases {
		result := escapeJSON(tc.input)
		if result != tc.expected {
			t.Errorf("escapeJSON(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestSlogLevelToEventLevel(t *testing.T) {
	h := &EventLogHandler{}

	testCases := []struct {
		level    slog.Level
		expected string
	}{
		{slog.LevelDebug, model.EventLevelInfo},
		{slog.LevelInfo, model.EventLevelInfo},
		{slog.LevelWarn, model.EventLevelWarning},
		{slog.LevelError, model.EventLevelError},
		{slog.LevelError + 4, model.EventLevelError}, // Higher than error
	}

	for _, tc := range testCases {
		result := h.slogLevelToEventLevel(tc.level)
		if result != tc.expected {
			t.Errorf("slogLevelToEventLevel(%v) = %q, want %q", tc.level, result, tc.expected)
		}
	}
}

// contains is a simple wrapper for strings.Contains.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// useDefaultLogger makes logger the default slog logger until the test ends,
// then restores it and the standard log package it takes over.
func useDefaultLogger(t *testing.T, logger *slog.Logger) {
	t.Helper()
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
}

// TestFailedEventWriteStaysOutOfTheEventLog checks that a failed event write
// is logged at error level with the event it lost, and that this record is
// not itself written to the Event Log. The failure used to be logged through
// the Event Log handler like any other record, so on a locked database it
// waited out the busy timeout a second time (review of the 2026-10-17 audit
// fixes).
//
// Bug state: drop the service.SkipsEventLog check in Handle, or log the
// failure in EventService.LogEvent without service.WithoutEventLog, and
// this fails.
func TestFailedEventWriteStaysOutOfTheEventLog(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	var out strings.Builder
	useDefaultLogger(t, slog.New(NewEventLogHandler(slog.NewTextHandler(&out, nil), db)))

	// An EventService whose database is closed fails every write.
	broken, brokenCleanup := testutil.TestDB(t)
	brokenCleanup()
	if err := service.NewEventService(broken).LogSystemEvent(context.Background(), model.EventLevelInfo, "Server stopped", nil, "", "", nil); err == nil {
		t.Fatal("LogSystemEvent on a closed database returned nil")
	}
	for _, want := range []string{`level=ERROR`, `msg="failed to record an event in the Event Log"`, `event="Server stopped"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the failed write was logged without %s:\n%s", want, out.String())
		}
	}
	for _, event := range fetchEvents(t, db, 10) {
		if strings.Contains(event.Message, "failed to record an event") {
			t.Errorf("the failed-write record was written to the Event Log: %q", event.Message)
		}
	}

	// A fresh handler writes the same record without the marker, as it does
	// any error, so the check above is not vacuous.
	control, controlCleanup := testutil.TestDB(t)
	defer controlCleanup()
	slog.New(NewEventLogHandler(discardHandler{}, control)).Error("failed to record an event in the Event Log", "event", "probe")
	if len(fetchEvents(t, control, 10)) != 1 {
		t.Error("an unmarked error was not written to the Event Log; the check above proves nothing")
	}
}

// recordingHandler keeps the records it handles.
type recordingHandler struct{ records *[]slog.Record }

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h recordingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, r)
	return nil
}
func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

// TestEventLogHandlerReportsItsOwnWriteFailures checks that a record the
// Event Log handler cannot write to the database is reported to the wrapped
// handler, so a gap in the audit trail shows in the log. The write error
// used to be dropped.
//
// Bug state: drop the err != nil branch in writeToEventLog and this fails.
func TestEventLogHandlerReportsItsOwnWriteFailures(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	cleanup() // closed: every write fails
	var records []slog.Record
	slog.New(NewEventLogHandler(recordingHandler{records: &records}, db)).Warn("disk almost full")
	var messages []string
	for _, r := range records {
		messages = append(messages, r.Message)
	}
	if len(records) != 2 || records[1].Message != "failed to record a log entry in the Event Log" || records[1].Level != slog.LevelError {
		t.Fatalf("the wrapped handler got %q, want the record and then an error about the failed write", messages)
	}
	records[1].Attrs(func(a slog.Attr) bool {
		if a.Key == "event" && a.Value.String() != "disk almost full" {
			t.Errorf("the failure names %q, want the lost record's message", a.Value.String())
		}
		return true
	})
}
