// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logging provides a custom slog handler that integrates with the Event Log system.
// It forwards logs at WARN level and above to the database-backed Event Log for auditing,
// writing them in the background.
package logging

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// EventLogHandler is a slog.Handler that wraps another handler and also writes
// WARN and ERROR level logs to the Event Log database. It skips records
// logged with a service.WithoutEventLog context and writes a given WARN
// message at most once per 30 seconds. The database writes happen in the
// background (see eventLogWriter), so a log call never waits for them; call
// Close before the database closes.
type EventLogHandler struct {
	inner   slog.Handler
	writer  *eventLogWriter
	level   slog.Level // Minimum level to forward to Event Log (default: WARN)
	limiter *warnLogLimiter
}

// eventLogQueueSize is how many entries can wait for the Event Log writer;
// entries beyond it are dropped and counted.
const eventLogQueueSize = 1024

// eventLogWriter writes Event Log entries in one background goroutine. A
// log call used to write its entry itself and, on a locked database, waited
// out the 5-second busy timeout. Failed writes and dropped entries are
// reported to the wrapped handler only: a failed write's report would fail
// the same way, and a drop report would wait behind the same backlog.
type eventLogWriter struct {
	queries *store.Queries
	report  slog.Handler
	entries chan store.CreateEventParams
	flushes chan chan struct{}
	done    chan struct{}
	dropped atomic.Int64
	mu      sync.RWMutex // held for sending entries, and to close them
	closed  bool
}

func newEventLogWriter(db *sql.DB, report slog.Handler) *eventLogWriter {
	w := &eventLogWriter{
		queries: store.New(db),
		report:  report,
		entries: make(chan store.CreateEventParams, eventLogQueueSize),
		flushes: make(chan chan struct{}),
		done:    make(chan struct{}),
	}
	go w.run()
	return w
}

// run writes the queued entries until the queue is closed. A flush request
// is answered once the entries queued when it arrives are written.
func (w *eventLogWriter) run() {
	defer close(w.done)
	for {
		select {
		case params, ok := <-w.entries:
			if !ok {
				w.reportDropped()
				return
			}
			w.write(params)
		case flushed := <-w.flushes:
			for range len(w.entries) {
				w.write(<-w.entries)
			}
			close(flushed)
		}
	}
}

// write writes one entry, reporting a failure and the entries dropped so far.
func (w *eventLogWriter) write(params store.CreateEventParams) {
	if _, err := w.queries.CreateEvent(context.Background(), params); err != nil {
		w.reportError("failed to record a log entry in the Event Log", slog.String("event", params.Message), slog.Any("error", err))
	}
	w.reportDropped()
}

// enqueue queues an entry without waiting, dropping it when the queue is
// full. After close it does nothing: the record has reached the wrapped
// handler, and no writer is left to count it for.
func (w *eventLogWriter) enqueue(params store.CreateEventParams) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return
	}
	select {
	case w.entries <- params:
	default:
		w.dropped.Add(1)
	}
}

// flush waits until the entries queued before it are written or have failed,
// or ctx ends. After close it waits for the writer to finish instead.
func (w *eventLogWriter) flush(ctx context.Context) error {
	flushed := make(chan struct{})
	select {
	case w.flushes <- flushed:
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-flushed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// close stops accepting entries and waits until the queued ones are written,
// or ctx ends, reporting then how many were still queued.
func (w *eventLogWriter) close(ctx context.Context) error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.entries)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		w.reportError(fmt.Sprintf("stopped waiting for the Event Log writer with %d log entries not yet written", len(w.entries)))
		return ctx.Err()
	}
}

// reportDropped reports entries dropped since the last report.
func (w *eventLogWriter) reportDropped() {
	if n := w.dropped.Swap(0); n > 0 {
		w.reportError(fmt.Sprintf("dropped %d log entries for the Event Log: its writer fell behind", n))
	}
}

// reportError logs msg at error level to the wrapped handler only.
func (w *eventLogWriter) reportError(msg string, attrs ...slog.Attr) {
	record := slog.NewRecord(time.Now(), slog.LevelError, msg, 0)
	record.AddAttrs(attrs...)
	_ = w.report.Handle(context.Background(), record)
}

type warnLogLimiter struct {
	mu            sync.Mutex
	lastByMessage map[string]time.Time
	interval      time.Duration
	now           func() time.Time
}

// NewEventLogHandler creates a new EventLogHandler that wraps the given handler.
// Logs at WARN level and above will be written to both the wrapped handler and the Event Log.
func NewEventLogHandler(inner slog.Handler, db *sql.DB) *EventLogHandler {
	return NewEventLogHandlerWithLevel(inner, db, slog.LevelWarn)
}

// NewEventLogHandlerWithLevel creates a new EventLogHandler with a custom minimum level.
func NewEventLogHandlerWithLevel(inner slog.Handler, db *sql.DB, level slog.Level) *EventLogHandler {
	return &EventLogHandler{
		inner:   inner,
		writer:  newEventLogWriter(db, inner),
		level:   level,
		limiter: newWarnLogLimiter(),
	}
}

// Flush waits until the entries handled so far are written to the Event Log
// or have failed, or ctx ends.
func (h *EventLogHandler) Flush(ctx context.Context) error {
	return h.writer.flush(ctx)
}

// Close stops writing new entries to the Event Log and waits until the
// queued ones are written, or ctx ends; it then reports to the wrapped
// handler how many were left. Records handled afterwards still reach the
// wrapped handler.
func (h *EventLogHandler) Close(ctx context.Context) error {
	return h.writer.close(ctx)
}

func newWarnLogLimiter() *warnLogLimiter {
	return &warnLogLimiter{
		lastByMessage: make(map[string]time.Time),
		interval:      30 * time.Second,
		now:           time.Now,
	}
}

// Enabled implements slog.Handler.
func (h *EventLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *EventLogHandler) Handle(ctx context.Context, r slog.Record) error {
	// Always forward to the inner handler first
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}

	// Only write to Event Log if the level is at or above our threshold, and
	// not records logged with a service.WithoutEventLog context.
	if r.Level >= h.level && !service.SkipsEventLog(ctx) && h.shouldWriteToEventLog(r) {
		h.writeToEventLog(ctx, r)
	}

	return nil
}

// WithAttrs implements slog.Handler.
func (h *EventLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &EventLogHandler{
		inner:   h.inner.WithAttrs(attrs),
		writer:  h.writer,
		level:   h.level,
		limiter: h.limiter,
	}
}

// WithGroup implements slog.Handler.
func (h *EventLogHandler) WithGroup(name string) slog.Handler {
	return &EventLogHandler{
		inner:   h.inner.WithGroup(name),
		writer:  h.writer,
		level:   h.level,
		limiter: h.limiter,
	}
}

func (h *EventLogHandler) shouldWriteToEventLog(r slog.Record) bool {
	if r.Level > slog.LevelWarn {
		return true
	}

	key := r.Message
	if key == "" {
		key = "warn"
	}

	now := h.limiter.now()
	h.limiter.mu.Lock()
	defer h.limiter.mu.Unlock()

	last, ok := h.limiter.lastByMessage[key]
	if ok && now.Sub(last) < h.limiter.interval {
		return false
	}

	h.limiter.lastByMessage[key] = now
	return true
}

// writeToEventLog queues a log record for the Event Log database. The
// request path is read from ctx now; the write happens in the background.
func (h *EventLogHandler) writeToEventLog(ctx context.Context, r slog.Record) {
	h.writer.enqueue(store.CreateEventParams{
		Level:     h.slogLevelToEventLevel(r.Level),
		Category:  h.extractCategory(r),
		Message:   r.Message,
		UserID:    sql.NullInt64{}, // No user context available from slog
		Metadata:  h.extractMetadata(ctx, r),
		CreatedAt: r.Time,
	})
}

// slogLevelToEventLevel converts a slog.Level to an Event Log level.
func (h *EventLogHandler) slogLevelToEventLevel(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return model.EventLevelError
	case level >= slog.LevelWarn:
		return model.EventLevelWarning
	default:
		return model.EventLevelInfo
	}
}

// extractCategory attempts to extract a category from the log record attributes.
// It looks for a "category" attribute or infers from common patterns.
func (h *EventLogHandler) extractCategory(r slog.Record) string {
	var category string

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "category" {
			category = a.Value.String()
			return false // Stop iteration
		}
		return true
	})

	if category != "" {
		return category
	}

	// Try to infer category from the message or other attributes
	msg := strings.ToLower(r.Message)
	switch {
	case strings.Contains(msg, "auth") || strings.Contains(msg, "login") || strings.Contains(msg, "logout"):
		return model.EventCategoryAuth
	case strings.Contains(msg, "page") || strings.Contains(msg, "content"):
		return model.EventCategoryPage
	case strings.Contains(msg, "user"):
		return model.EventCategoryUser
	case strings.Contains(msg, "config") || strings.Contains(msg, "setting"):
		return model.EventCategoryConfig
	case strings.Contains(msg, "tag"):
		return model.EventCategoryTag
	case strings.Contains(msg, "category"):
		return model.EventCategoryCategory
	case strings.Contains(msg, "media"):
		return model.EventCategoryMedia
	case strings.Contains(msg, "cache"):
		return model.EventCategoryCache
	default:
		return model.EventCategorySystem
	}
}

// extractMetadata collects all log attributes into a JSON string.
// It also includes the request path from context if available.
func (h *EventLogHandler) extractMetadata(ctx context.Context, r slog.Record) string {
	// Build a simple JSON object from attributes
	var sb strings.Builder
	sb.WriteString("{")
	first := true

	// Add request path from context if available
	if path := middleware.GetRequestPath(ctx); path != "" {
		sb.WriteString(`"path":"`)
		sb.WriteString(escapeJSON(path))
		sb.WriteString(`"`)
		first = false
	}

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "category" {
			return true // Skip category, already extracted
		}
		if !first {
			sb.WriteString(",")
		}
		first = false
		sb.WriteString(`"`)
		sb.WriteString(escapeJSON(a.Key))
		sb.WriteString(`":"`)
		sb.WriteString(escapeJSON(a.Value.String()))
		sb.WriteString(`"`)
		return true
	})

	sb.WriteString("}")
	return sb.String()
}

// escapeJSON escapes special characters in a string for JSON.
func escapeJSON(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
