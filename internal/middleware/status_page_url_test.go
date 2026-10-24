// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestLoadSiteConfigStatusPageURL(t *testing.T) {
	const statusURL = "https://stats.uptimerobot.com/g50rYsPMxR"

	captureStatusPageURL := func(t *testing.T, mw func(http.Handler) http.Handler) string {
		t.Helper()
		var got string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = GetStatusPageURL(r)
			w.WriteHeader(http.StatusOK)
		})
		mw(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/", nil))
		return got
	}

	t.Run("no database leaves it empty", func(t *testing.T) {
		if got := captureStatusPageURL(t, LoadSiteConfig(nil, nil)); got != "" {
			t.Errorf("GetStatusPageURL() = %q, want empty", got)
		}
	})

	for _, withCache := range []bool{false, true} {
		name := "database"
		if withCache {
			name = "cache"
		}
		t.Run(name, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			defer cleanup()
			queries := store.New(db)

			var cacheManager *cache.Manager
			if withCache {
				cacheManager = cache.NewManager(queries)
			}
			mw := LoadSiteConfig(db, cacheManager)

			// The first request also warms the cache.
			if got := captureStatusPageURL(t, mw); got != "" {
				t.Errorf("GetStatusPageURL() without config = %q, want empty", got)
			}

			if _, err := queries.UpsertConfig(context.Background(), store.UpsertConfigParams{
				Key:   model.ConfigKeyStatusPageURL,
				Value: statusURL,
				Type:  model.ConfigTypeString,
			}); err != nil {
				t.Fatalf("UpsertConfig: %v", err)
			}
			if cacheManager != nil {
				cacheManager.InvalidateConfig() // what a config save does
			}

			if got := captureStatusPageURL(t, mw); got != statusURL {
				t.Errorf("GetStatusPageURL() = %q, want %q", got, statusURL)
			}
		})
	}
}
