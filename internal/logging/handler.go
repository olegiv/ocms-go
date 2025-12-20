// Package logging provides a custom slog handler that integrates with the Event Log system.
// It forwards logs at WARN level and above to the database-backed Event Log for auditing.
package logging

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"

	"ocms-go/internal/model"
	"ocms-go/internal/store"
)

// EventLogHandler is a slog.Handler that wraps another handler and also writes
// WARN and ERROR level logs to the Event Log database.
type EventLogHandler struct {
	inner   slog.Handler
	queries *store.Queries
	level   slog.Level // Minimum level to forward to Event Log (default: WARN)
}

// NewEventLogHandler creates a new EventLogHandler that wraps the given handler.
// Logs at WARN level and above will be written to both the wrapped handler and the Event Log.
func NewEventLogHandler(inner slog.Handler, db *sql.DB) *EventLogHandler {
	return &EventLogHandler{
		inner:   inner,
		queries: store.New(db),
		level:   slog.LevelWarn,
	}
}

// NewEventLogHandlerWithLevel creates a new EventLogHandler with a custom minimum level.
func NewEventLogHandlerWithLevel(inner slog.Handler, db *sql.DB, level slog.Level) *EventLogHandler {
	return &EventLogHandler{
		inner:   inner,
		queries: store.New(db),
		level:   level,
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

	// Only write to Event Log if the level is at or above our threshold
	if r.Level >= h.level {
		h.writeToEventLog(ctx, r)
	}

	return nil
}

// WithAttrs implements slog.Handler.
func (h *EventLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &EventLogHandler{
		inner:   h.inner.WithAttrs(attrs),
		queries: h.queries,
		level:   h.level,
	}
}

// WithGroup implements slog.Handler.
func (h *EventLogHandler) WithGroup(name string) slog.Handler {
	return &EventLogHandler{
		inner:   h.inner.WithGroup(name),
		queries: h.queries,
		level:   h.level,
	}
}

// writeToEventLog writes a log record to the Event Log database.
func (h *EventLogHandler) writeToEventLog(_ context.Context, r slog.Record) {
	level := h.slogLevelToEventLevel(r.Level)
	category := h.extractCategory(r)
	metadata := h.extractMetadata(r)

	// Create the event in the database
	// We use a background context to ensure the event is logged even if the request context is cancelled
	_, _ = h.queries.CreateEvent(context.Background(), store.CreateEventParams{
		Level:     level,
		Category:  category,
		Message:   r.Message,
		UserID:    sql.NullInt64{}, // No user context available from slog
		Metadata:  metadata,
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
	case strings.Contains(msg, "cache"):
		return model.EventCategoryCache
	default:
		return model.EventCategorySystem
	}
}

// extractMetadata collects all log attributes into a JSON string.
func (h *EventLogHandler) extractMetadata(r slog.Record) string {
	if r.NumAttrs() == 0 {
		return "{}"
	}

	// Build a simple JSON object from attributes
	var sb strings.Builder
	sb.WriteString("{")
	first := true

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
