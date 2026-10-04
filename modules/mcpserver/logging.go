// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"log/slog"
)

// sdkLogger returns the logger handed to the go-sdk. The stateless handler
// opens and closes a session for every request and logs each step at Info,
// three lines per call that say nothing the module's own call log does not,
// so only warnings and errors from the SDK are kept.
func sdkLogger(base *slog.Logger, component string) *slog.Logger {
	return slog.New(&minLevelHandler{
		Handler: base.With("component", component).Handler(),
		min:     slog.LevelWarn,
	})
}

// minLevelHandler drops records below min before they reach Handler.
type minLevelHandler struct {
	slog.Handler
	min slog.Level
}

// Enabled reports whether a record at level l is logged.
func (h *minLevelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

// Handle forwards records at or above the minimum level.
func (h *minLevelHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.min {
		return nil
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs keeps the level filter on derived handlers.
func (h *minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &minLevelHandler{Handler: h.Handler.WithAttrs(attrs), min: h.min}
}

// WithGroup keeps the level filter on derived handlers.
func (h *minLevelHandler) WithGroup(name string) slog.Handler {
	return &minLevelHandler{Handler: h.Handler.WithGroup(name), min: h.min}
}
