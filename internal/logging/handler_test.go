// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"slices"
	"strings"
	"sync"
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

// testHandlers are the handlers the running test created; fetchEvents
// flushes them so their background writes are in the database.
var testHandlers []*EventLogHandler

// newTestHandler creates an Event Log handler that the test flushes before
// reading events and closes when it ends.
func newTestHandler(t *testing.T, inner slog.Handler, db *sql.DB) *EventLogHandler {
	t.Helper()
	return trackTestHandler(t, NewEventLogHandler(inner, db))
}

// newTestHandlerWithLevel is newTestHandler with a custom minimum level.
func newTestHandlerWithLevel(t *testing.T, inner slog.Handler, db *sql.DB, level slog.Level) *EventLogHandler {
	t.Helper()
	return trackTestHandler(t, NewEventLogHandlerWithLevel(inner, db, level))
}

func trackTestHandler(t *testing.T, h *EventLogHandler) *EventLogHandler {
	t.Helper()
	testHandlers = append(testHandlers, h)
	t.Cleanup(func() {
		testHandlers = nil
		if err := h.Close(context.Background()); err != nil {
			t.Errorf("closing the handler: %v", err)
		}
	})
	return h
}

// flushTestHandlers waits until the test's handlers have written what they
// queued.
func flushTestHandlers(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, h := range testHandlers {
		if err := h.Flush(ctx); err != nil {
			t.Fatalf("flushing the Event Log handler: %v", err)
		}
	}
}

// fetchEvents retrieves events from the database after the test's handlers
// have written what they queued.
func fetchEvents(t *testing.T, db *sql.DB, limit int) []store.Event {
	t.Helper()
	flushTestHandlers(t)
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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandlerWithLevel(t, discardHandler{}, db, slog.LevelInfo)
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

			handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
	logger := slog.New(handler)

	// Log multiple events
	logger.Error("error 1")
	logger.Warn("warning 1")
	logger.Error("error 2")
	logger.Warn("warning 2")
	logger.Info("info 1") // Should not be captured
	flushTestHandlers(t)

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

	handler := newTestHandler(t, discardHandler{}, db)
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

	handler := newTestHandler(t, discardHandler{}, db)
	now := time.Now()
	handler.limiter.now = func() time.Time {
		return now
	}

	logger := slog.New(handler)

	logger.Warn("invalid CSRF token", "form_slug", "contact")
	logger.Warn("invalid CSRF token", "form_slug", "contact")
	flushTestHandlers(t)

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
	flushTestHandlers(t)

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
	useDefaultLogger(t, slog.New(newTestHandler(t, slog.NewTextHandler(&out, nil), db)))

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
	slog.New(newTestHandler(t, discardHandler{}, control)).Error("failed to record an event in the Event Log", "event", "probe")
	if len(fetchEvents(t, control, 10)) != 1 {
		t.Error("an unmarked error was not written to the Event Log; the check above proves nothing")
	}
}

// recordingHandler keeps the records it handles, from the log calls and the
// Event Log writer goroutine alike.
type recordingHandler struct {
	mu      *sync.Mutex
	records *[]slog.Record
}

func newRecordingHandler() recordingHandler {
	return recordingHandler{mu: &sync.Mutex{}, records: &[]slog.Record{}}
}

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, r)
	return nil
}
func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

// all returns the records handled so far.
func (h recordingHandler) all() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(*h.records)
}

// has reports whether a record whose message contains part was handled.
func (h recordingHandler) has(part string) bool {
	return slices.ContainsFunc(h.all(), func(r slog.Record) bool { return strings.Contains(r.Message, part) })
}

// TestEventLogHandlerReportsItsOwnWriteFailures checks that a record the
// Event Log handler cannot write to the database is reported to the wrapped
// handler, so a gap in the audit trail shows in the log. The write error
// used to be dropped.
//
// Bug state: drop the err != nil branch in eventLogWriter.write and this
// fails.
func TestEventLogHandlerReportsItsOwnWriteFailures(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	cleanup() // closed: every write fails
	recorder := newRecordingHandler()
	slog.New(newTestHandler(t, recorder, db)).Warn("disk almost full")
	flushTestHandlers(t)
	records := recorder.all()
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

// lockedTestDB returns a migrated file database and a function that holds
// its write lock from another connection until the returned release runs.
func lockedTestDB(t *testing.T) (db *sql.DB, lock func() (release func())) {
	t.Helper()
	path := t.TempDir() + "/events.db"
	db, err := store.NewDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	locker, err := store.NewDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	return db, func() func() {
		tx, err := locker.BeginTx(context.Background(), nil) // _txlock=immediate: takes the write lock
		if err != nil {
			t.Fatal(err)
		}
		return func() { _ = tx.Rollback() }
	}
}

// TestEventLogWritesDoNotBlockTheCaller checks that a log call returns at
// once while the database is locked, and that its entry is written when the
// lock goes. The handler used to write the entry itself, so on a locked
// database every warning held its caller, a request handler too, for up to
// the 5-second busy timeout (review of the 2026-10-17 audit fixes).
//
// Bug state: write the entry in writeToEventLog instead of queuing it and
// the log call takes the busy timeout.
func TestEventLogWritesDoNotBlockTheCaller(t *testing.T) {
	db, lock := lockedTestDB(t)
	logger := slog.New(newTestHandler(t, discardHandler{}, db))
	release := lock()
	start := time.Now()
	logger.Warn("written while the database is locked")
	elapsed := time.Since(start)
	release()
	if elapsed > 500*time.Millisecond {
		t.Errorf("a log call took %v while the database was locked; it must not wait for the write", elapsed)
	}
	if events := fetchEvents(t, db, 10); len(events) != 1 || events[0].Message != "written while the database is locked" {
		t.Errorf("events after the lock went: %v, want the queued entry", events)
	}
}

// TestEventLogFlushWaitsForQueuedEntries checks that Flush returns only once
// the entries queued before it are written, here a backlog behind a locked
// database. The tests read the Event Log after a Flush.
//
// Bug state: answer a flush request without writing the queued entries in
// eventLogWriter.run and this fails.
func TestEventLogFlushWaitsForQueuedEntries(t *testing.T) {
	db, lock := lockedTestDB(t)
	logger := slog.New(newTestHandler(t, discardHandler{}, db))
	release := lock()
	const queued = 20
	for i := range queued {
		logger.Error(fmt.Sprintf("queued %d", i))
	}
	flushed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		flushed <- testHandlers[0].Flush(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	release()
	if err := <-flushed; err != nil {
		t.Fatal(err)
	}
	count, err := store.New(db).CountEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != queued {
		t.Errorf("events after Flush = %d, want the %d entries queued before it", count, queued)
	}
}

// TestEventLogQueueOverflowIsReported checks that entries beyond the queue
// are dropped without blocking and that the drop is reported to the wrapped
// handler, so a gap in the audit trail shows in the log.
func TestEventLogQueueOverflowIsReported(t *testing.T) {
	db, lock := lockedTestDB(t)
	recorder := newRecordingHandler()
	logger := slog.New(newTestHandler(t, recorder, db))
	release := lock()
	start := time.Now()
	for i := range eventLogQueueSize + 50 {
		logger.Error(fmt.Sprintf("error %d", i)) // errors skip the rate limiter
	}
	elapsed := time.Since(start)
	release()
	if elapsed > 2*time.Second {
		t.Errorf("%d log calls took %v while the database was locked; a full queue must drop, not wait", eventLogQueueSize+50, elapsed)
	}
	flushTestHandlers(t)
	if !recorder.has("log entries for the Event Log: its writer fell behind") {
		t.Error("entries beyond the queue were dropped without a report")
	}
}

// TestEventLogCloseWritesQueuedEntries checks that Close waits until every
// queued entry is written, here while the database is locked when Close
// starts, and that records handled afterwards still reach the wrapped
// handler but not the database.
//
// Bug state: make close return without waiting for the writer and this
// fails.
func TestEventLogCloseWritesQueuedEntries(t *testing.T) {
	db, lock := lockedTestDB(t)
	recorder := newRecordingHandler()
	handler := NewEventLogHandler(recorder, db)
	logger := slog.New(handler)
	release := lock()
	const queued = 20
	for i := range queued {
		logger.Error(fmt.Sprintf("before close %d", i))
	}
	closed := make(chan error, 1)
	go func() { closed <- handler.Close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned (%v) before the queued entries could be written", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	logger.Error("after close")
	count, err := store.New(db).CountEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != queued {
		t.Errorf("events after Close = %d, want the %d entries queued before it", count, queued)
	}
	if records := recorder.all(); len(records) == 0 || records[len(records)-1].Message != "after close" {
		t.Errorf("records after Close did not reach the wrapped handler: %d records", len(records))
	}
}

// TestEventLogCloseReportsUnwrittenEntries checks that a Close that stops
// waiting, here on a database locked throughout, says how many entries it
// left unwritten. The server closes the Event Log with a deadline, and the
// entries it leaves used to vanish without a count (review of the
// 2026-10-17 audit fixes).
//
// Bug state: drop the report in close's ctx.Done case and this fails.
func TestEventLogCloseReportsUnwrittenEntries(t *testing.T) {
	db, lock := lockedTestDB(t)
	recorder := newRecordingHandler()
	handler := NewEventLogHandler(recorder, db)
	logger := slog.New(handler)
	release := lock()
	defer release()
	for i := range 20 {
		logger.Error(fmt.Sprintf("queued %d", i))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := handler.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close on a locked database returned %v, want context.DeadlineExceeded", err)
	}
	if !recorder.has("log entries not yet written") {
		t.Error("Close stopped waiting without reporting the entries it left unwritten")
	}
}

// TestEventLogHandlerConcurrentUse logs from several goroutines while Flush
// and Close run, for the race detector: a log call during Close must neither
// send on the closed queue nor block, and Flush must return.
//
// Bug state: read w.closed in enqueue without holding w.mu and this panics
// with a send on a closed channel or reports a race under -race.
func TestEventLogHandlerConcurrentUse(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	handler := NewEventLogHandler(discardHandler{}, db)
	logger := slog.New(handler)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				logger.Error(fmt.Sprintf("goroutine %d entry %d", g, i))
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wg.Go(func() {
		if err := handler.Flush(ctx); err != nil {
			t.Errorf("Flush: %v", err)
		}
	})
	time.Sleep(time.Millisecond)
	if err := handler.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	wg.Wait()
	if err := handler.Flush(ctx); err != nil {
		t.Errorf("Flush after Close: %v", err)
	}
}
