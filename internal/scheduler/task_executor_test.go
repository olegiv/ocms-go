// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"
)

func TestNewTaskExecutor_UsesSSRFSafeDialer(t *testing.T) {
	executor := NewTaskExecutor(nil, slog.Default(), nil, cron.New())

	transport, ok := executor.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", executor.httpClient.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("expected custom DialContext for SSRF protection")
	}

	_, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:80")
	if err == nil {
		t.Fatal("expected loopback connection to be blocked")
	}
	if !strings.Contains(err.Error(), "private IP") {
		t.Fatalf("expected private IP block error, got: %v", err)
	}
}
