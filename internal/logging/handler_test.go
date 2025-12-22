package logging

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"ocms-go/internal/model"
	"ocms-go/internal/store"
)

// testDB creates a temporary test database with migrations applied.
func testDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	// Create temp file for test database
	f, err := os.CreateTemp("", "ocms-logging-test-*.db")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	// Open database
	db, err := store.NewDB(dbPath)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("NewDB: %v", err)
	}

	// Run migrations
	if err := store.Migrate(db); err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Migrate: %v", err)
	}

	// Return cleanup function
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return db, cleanup
}

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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
			db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
	db, cleanup := testDB(t)
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
