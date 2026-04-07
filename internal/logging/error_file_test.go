// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestErrorFileHandler_ForwardsErrorToSecondary(t *testing.T) {
	var primaryBuf, secondaryBuf bytes.Buffer

	primary := slog.NewTextHandler(&primaryBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	secondary := slog.NewTextHandler(&secondaryBuf, &slog.HandlerOptions{Level: slog.LevelError})
	h := NewErrorFileHandler(primary, secondary, slog.LevelError)

	logger := slog.New(h)

	logger.Info("info message")
	logger.Error("error message")

	// Primary should have both messages
	if !strings.Contains(primaryBuf.String(), "info message") {
		t.Error("expected primary to contain info message")
	}
	if !strings.Contains(primaryBuf.String(), "error message") {
		t.Error("expected primary to contain error message")
	}

	// Secondary should only have the error message
	if strings.Contains(secondaryBuf.String(), "info message") {
		t.Error("expected secondary to NOT contain info message")
	}
	if !strings.Contains(secondaryBuf.String(), "error message") {
		t.Error("expected secondary to contain error message")
	}
}

func TestErrorFileHandler_SkipsBelowThreshold(t *testing.T) {
	var secondaryBuf bytes.Buffer

	primary := discardHandler{}
	secondary := slog.NewTextHandler(&secondaryBuf, &slog.HandlerOptions{Level: slog.LevelError})
	h := NewErrorFileHandler(primary, secondary, slog.LevelError)

	logger := slog.New(h)

	logger.Info("info only")
	logger.Warn("warn only")

	if secondaryBuf.Len() > 0 {
		t.Errorf("expected secondary to be empty, got: %s", secondaryBuf.String())
	}
}

func TestErrorFileHandler_WithAttrsPreserved(t *testing.T) {
	var secondaryBuf bytes.Buffer

	primary := discardHandler{}
	secondary := slog.NewTextHandler(&secondaryBuf, &slog.HandlerOptions{Level: slog.LevelError})
	h := NewErrorFileHandler(primary, secondary, slog.LevelError)

	logger := slog.New(h).With("component", "api")
	logger.Error("test error")

	if !strings.Contains(secondaryBuf.String(), "component=api") {
		t.Errorf("expected secondary to contain attrs, got: %s", secondaryBuf.String())
	}
}

func TestErrorFileHandler_Enabled(t *testing.T) {
	primary := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelInfo})
	secondary := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError})
	h := NewErrorFileHandler(primary, secondary, slog.LevelError)

	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected Enabled to return true for info level")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("expected Enabled to return true for error level")
	}
}
