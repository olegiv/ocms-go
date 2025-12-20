package logging

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
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

func TestEventLogHandler_Handle_ErrorLevel(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	// Log an error
	logger.Error("database connection failed", "host", "localhost", "port", 5432)

	// Give it a moment to write
	time.Sleep(50 * time.Millisecond)

	// Verify event was created in database
	q := store.New(db)
	events, err := q.ListEvents(context.Background(), store.ListEventsParams{
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Level != model.EventLevelError {
		t.Errorf("Level = %q, want %q", events[0].Level, model.EventLevelError)
	}
	if events[0].Message != "database connection failed" {
		t.Errorf("Message = %q, want %q", events[0].Message, "database connection failed")
	}
}

func TestEventLogHandler_Handle_WarnLevel(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	// Log a warning
	logger.Warn("slow query detected", "duration_ms", 5000)

	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, err := q.ListEvents(context.Background(), store.ListEventsParams{
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Level != model.EventLevelWarning {
		t.Errorf("Level = %q, want %q", events[0].Level, model.EventLevelWarning)
	}
	if events[0].Message != "slow query detected" {
		t.Errorf("Message = %q, want %q", events[0].Message, "slow query detected")
	}
}

func TestEventLogHandler_Handle_InfoLevel_NotCaptured(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	// Log info level - should NOT be captured
	logger.Info("server started", "port", 8080)

	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, err := q.ListEvents(context.Background(), store.ListEventsParams{
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(events) != 0 {
		t.Errorf("expected 0 events for INFO level, got %d", len(events))
	}
}

func TestEventLogHandler_Handle_DebugLevel_NotCaptured(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	// Log debug level - should NOT be captured
	logger.Debug("processing request", "request_id", "abc123")

	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, err := q.ListEvents(context.Background(), store.ListEventsParams{
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(events) != 0 {
		t.Errorf("expected 0 events for DEBUG level, got %d", len(events))
	}
}

func TestEventLogHandler_Handle_CustomLevel(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	// Create handler with INFO as minimum level
	handler := NewEventLogHandlerWithLevel(discardHandler{}, db, slog.LevelInfo)
	logger := slog.New(handler)

	// Log info level - should now be captured
	logger.Info("server started", "port", 8080)

	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, err := q.ListEvents(context.Background(), store.ListEventsParams{
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(events) != 1 {
		t.Errorf("expected 1 event with custom INFO level, got %d", len(events))
	}
}

func TestEventLogHandler_CategoryInference_Auth(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	testCases := []struct {
		message          string
		expectedCategory string
	}{
		{"user authentication failed", model.EventCategoryAuth},
		{"login attempt blocked", model.EventCategoryAuth},
		{"logout completed", model.EventCategoryAuth},
	}

	for _, tc := range testCases {
		// Clear events first
		_, _ = db.Exec("DELETE FROM events")

		logger.Error(tc.message)
		time.Sleep(50 * time.Millisecond)

		q := store.New(db)
		events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

		if len(events) != 1 {
			t.Errorf("message %q: expected 1 event, got %d", tc.message, len(events))
			continue
		}

		if events[0].Category != tc.expectedCategory {
			t.Errorf("message %q: Category = %q, want %q", tc.message, events[0].Category, tc.expectedCategory)
		}
	}
}

func TestEventLogHandler_CategoryInference_Page(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Error("page not found")
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Category != model.EventCategoryPage {
		t.Errorf("Category = %q, want %q", events[0].Category, model.EventCategoryPage)
	}
}

func TestEventLogHandler_CategoryInference_Cache(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Error("cache invalidation failed")
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Category != model.EventCategoryCache {
		t.Errorf("Category = %q, want %q", events[0].Category, model.EventCategoryCache)
	}
}

func TestEventLogHandler_CategoryInference_Config(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	logger.Error("config validation failed")
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Category != model.EventCategoryConfig {
		t.Errorf("Category = %q, want %q", events[0].Category, model.EventCategoryConfig)
	}
}

func TestEventLogHandler_CategoryInference_System(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	// Message without known keywords should default to system
	logger.Error("unknown error occurred")
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Category != model.EventCategorySystem {
		t.Errorf("Category = %q, want %q", events[0].Category, model.EventCategorySystem)
	}
}

func TestEventLogHandler_ExplicitCategory(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)

	// Use explicit category attribute - should override inference
	logger.Error("something happened", "category", model.EventCategoryUser)
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Category != model.EventCategoryUser {
		t.Errorf("Category = %q, want %q (explicit category should override)", events[0].Category, model.EventCategoryUser)
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
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// Check that metadata contains the attributes
	metadata := events[0].Metadata
	if metadata == "{}" {
		t.Error("Metadata should not be empty")
	}

	// Basic checks that keys are present in the JSON
	if !contains(metadata, "status_code") {
		t.Errorf("Metadata should contain 'status_code': %s", metadata)
	}
	if !contains(metadata, "path") {
		t.Errorf("Metadata should contain 'path': %s", metadata)
	}
	if !contains(metadata, "duration_ms") {
		t.Errorf("Metadata should contain 'duration_ms': %s", metadata)
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
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// The wrapped handler should still work and capture events
	if events[0].Message != "service error" {
		t.Errorf("Message = %q, want %q", events[0].Message, "service error")
	}
}

func TestEventLogHandler_WithGroup(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	handler := NewEventLogHandler(discardHandler{}, db)
	handlerWithGroup := handler.WithGroup("request")

	logger := slog.New(handlerWithGroup)
	logger.Error("request error", "id", "abc123")
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// The wrapped handler should still work
	if events[0].Message != "request error" {
		t.Errorf("Message = %q, want %q", events[0].Message, "request error")
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

	// Log with special characters that need JSON escaping
	logger.Error("parse error",
		"input", `{"key": "value with \"quotes\""}`,
		"path", "C:\\Users\\test",
		"message", "line1\nline2\ttabbed",
	)
	time.Sleep(50 * time.Millisecond)

	q := store.New(db)
	events, _ := q.ListEvents(context.Background(), store.ListEventsParams{Limit: 1, Offset: 0})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// Metadata should be valid JSON (not crash on special characters)
	if events[0].Metadata == "" {
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

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
