// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"context"
	"log/slog"
)

// ErrorFileHandler is a slog.Handler that wraps a primary handler and also
// writes records at or above a threshold level to a secondary handler.
// This allows ERROR+ logs to be tee'd to a separate file while all logs
// continue to flow through the primary handler.
type ErrorFileHandler struct {
	primary   slog.Handler
	secondary slog.Handler
	level     slog.Level // Minimum level for the secondary handler
}

// NewErrorFileHandler creates a handler that forwards all logs to primary and
// additionally forwards records at level or above to secondary.
func NewErrorFileHandler(primary, secondary slog.Handler, level slog.Level) *ErrorFileHandler {
	return &ErrorFileHandler{
		primary:   primary,
		secondary: secondary,
		level:     level,
	}
}

// Enabled implements slog.Handler.
func (h *ErrorFileHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.primary.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *ErrorFileHandler) Handle(ctx context.Context, r slog.Record) error {
	// Always forward to the primary handler
	if err := h.primary.Handle(ctx, r); err != nil {
		return err
	}

	// Also forward to secondary if the level is at or above threshold
	if r.Level >= h.level {
		// Best-effort: don't fail the primary if secondary write fails
		_ = h.secondary.Handle(ctx, r)
	}

	return nil
}

// WithAttrs implements slog.Handler.
func (h *ErrorFileHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ErrorFileHandler{
		primary:   h.primary.WithAttrs(attrs),
		secondary: h.secondary.WithAttrs(attrs),
		level:     h.level,
	}
}

// WithGroup implements slog.Handler.
func (h *ErrorFileHandler) WithGroup(name string) slog.Handler {
	return &ErrorFileHandler{
		primary:   h.primary.WithGroup(name),
		secondary: h.secondary.WithGroup(name),
		level:     h.level,
	}
}
