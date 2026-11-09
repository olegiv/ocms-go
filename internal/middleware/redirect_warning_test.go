// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// TestBlockedRedirectWarningNamesTheTarget checks that refusing an unsafe
// redirect target logs which target was refused, as it did before the shared
// content-path refactor, so an operator can find and fix the redirect row
// (2026-11-03 review S13).
//
// Bug state: log only the source path in the warning and this fails.
func TestBlockedRedirectWarningNamesTheTarget(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rm := &RedirectsMiddleware{
		redirects: []store.Redirect{{SourcePath: "/old", TargetUrl: "//outside.test/landing", StatusCode: http.StatusFound, Enabled: true}},
		lastLoad:  time.Now(),
		cacheTTL:  time.Hour,
	}
	rr := httptest.NewRecorder()
	rm.Handler(http.NotFoundHandler()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/old", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unsafe redirect status = %d, want 400", rr.Code)
	}
	line := logs.String()
	if !strings.Contains(line, "blocked unsafe redirect target") || !strings.Contains(line, "target=//outside.test/landing") || !strings.Contains(line, "source=/old") {
		t.Errorf("warning lacks the source or target: %s", line)
	}
}
