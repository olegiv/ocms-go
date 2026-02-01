// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
)

// faviconTestCase defines a test case for favicon handler tests.
type faviconTestCase struct {
	name           string
	setupDB        func(*testing.T, *sql.DB) // optional DB setup
	favicon        []byte
	wantStatus     int
	wantType       string // empty to skip check
	wantCache      string // empty to skip check
}

func runFaviconTest(t *testing.T, tc faviconTestCase) {
	t.Helper()

	db, _ := testHandlerSetup(t)

	if tc.setupDB != nil {
		tc.setupDB(t, db)
	}

	h := NewFrontendHandler(db, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	w := httptest.NewRecorder()

	h.Favicon(w, req, tc.favicon)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != tc.wantStatus {
		t.Errorf("status = %d; want %d", resp.StatusCode, tc.wantStatus)
	}

	if tc.wantType != "" {
		if got := resp.Header.Get("Content-Type"); got != tc.wantType {
			t.Errorf("Content-Type = %q; want %q", got, tc.wantType)
		}
	}

	if tc.wantCache != "" {
		if got := resp.Header.Get("Cache-Control"); got != tc.wantCache {
			t.Errorf("Cache-Control = %q; want %q", got, tc.wantCache)
		}
	}
}

func TestFrontendHandler_Favicon_DefaultFavicon(t *testing.T) {
	runFaviconTest(t, faviconTestCase{
		name:       "default favicon",
		favicon:    []byte{0x00, 0x00, 0x01, 0x00}, // Minimal ICO header
		wantStatus: http.StatusOK,
		wantType:   "image/x-icon",
		wantCache:  "public, max-age=31536000",
	})
}

func TestFrontendHandler_Favicon_WithThemeSettings(t *testing.T) {
	runFaviconTest(t, faviconTestCase{
		name: "with theme settings",
		setupDB: func(t *testing.T, db *sql.DB) {
			t.Helper()
			_, err := db.Exec(`INSERT INTO config (key, value, type) VALUES (?, ?, ?)`,
				"theme_settings_default",
				`{"favicon":"/uploads/original/abc123/favicon.ico"}`,
				"json",
			)
			if err != nil {
				t.Fatalf("failed to insert config: %v", err)
			}
		},
		favicon:    []byte{0x00, 0x00, 0x01, 0x00},
		wantStatus: http.StatusOK,
		// Note: Without a proper theme manager mock, this test verifies the handler
		// doesn't panic when theme manager is nil. In a full integration test,
		// we would mock the theme manager to return an active theme.
	})
}

func TestFrontendHandler_Favicon_EmptyDefaultFavicon(t *testing.T) {
	runFaviconTest(t, faviconTestCase{
		name:       "empty default favicon",
		favicon:    nil,
		wantStatus: http.StatusOK,
		wantType:   "image/x-icon",
	})
}
