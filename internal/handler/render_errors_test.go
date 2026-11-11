// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestRenderTemplLogsRenderErrors checks that a failed admin render is logged,
// not only answered with a 500.
func TestRenderTemplLogsRenderErrors(t *testing.T) {
	logs := captureLogs(t)
	failing := templ.ComponentFunc(func(context.Context, io.Writer) error {
		return errors.New("component exploded")
	})
	w := httptest.NewRecorder()
	renderTempl(w, httptest.NewRequest(http.MethodGet, "/admin/broken", nil), failing)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	for _, want := range []string{"templ render failed", "/admin/broken", "component exploded"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs.String())
		}
	}
}

// TestOptionalConfigValueLogsLookupFailures checks that the frontend reads a
// missing config row as "" quietly, but logs a lookup that failed, with and
// without the config cache.
func TestOptionalConfigValueLogsLookupFailures(t *testing.T) {
	for _, withCache := range []bool{false, true} {
		name := "database"
		if withCache {
			name = "cache"
		}
		t.Run(name, func(t *testing.T) {
			db, _ := testHandlerSetup(t)
			var cacheManager *cache.Manager
			if withCache {
				cacheManager = cache.NewManager(store.New(db))
			}
			h := NewFrontendHandler(db, testThemeManager(), cacheManager, slog.Default(), nil, nil)
			logs := captureLogs(t)

			if got := h.optionalConfigValue(context.Background(), model.SiteLogoConfigKey); got != "" {
				t.Errorf("unset site_logo = %q, want empty", got)
			}
			if logs.Len() != 0 {
				t.Errorf("a missing row was logged:\n%s", logs.String())
			}

			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if cacheManager != nil {
				cacheManager.InvalidateConfig() // force a reload from the closed database
			}
			if got := h.optionalConfigValue(context.Background(), model.SiteLogoConfigKey); got != "" {
				t.Errorf("failed lookup = %q, want empty", got)
			}
			if !strings.Contains(logs.String(), "failed to load config value") {
				t.Errorf("a failed lookup was not logged:\n%s", logs.String())
			}
		})
	}
}
