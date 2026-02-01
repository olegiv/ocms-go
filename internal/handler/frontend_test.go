// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrontendHandler_Favicon_DefaultFavicon(t *testing.T) {
	db, _ := testHandlerSetup(t)

	// Create handler with no theme manager (nil)
	h := NewFrontendHandler(db, nil, nil, nil, nil)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	w := httptest.NewRecorder()

	// Create a default favicon
	defaultFavicon := []byte{0x00, 0x00, 0x01, 0x00} // Minimal ICO header

	// Call favicon handler
	h.Favicon(w, req, defaultFavicon)

	// Check response
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusOK)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "image/x-icon" {
		t.Errorf("Content-Type = %q; want %q", contentType, "image/x-icon")
	}

	cacheControl := resp.Header.Get("Cache-Control")
	if cacheControl != "public, max-age=31536000" {
		t.Errorf("Cache-Control = %q; want %q", cacheControl, "public, max-age=31536000")
	}
}

func TestFrontendHandler_Favicon_WithThemeSettings(t *testing.T) {
	db, _ := testHandlerSetup(t)

	// Insert a config for theme settings with favicon
	_, err := db.Exec(`INSERT INTO config (key, value, type) VALUES (?, ?, ?)`,
		"theme_settings_default",
		`{"favicon":"/uploads/original/abc123/favicon.ico"}`,
		"json",
	)
	if err != nil {
		t.Fatalf("failed to insert config: %v", err)
	}

	// Note: Without a proper theme manager mock, this test verifies the handler
	// doesn't panic when theme manager is nil. In a full integration test,
	// we would mock the theme manager to return an active theme.

	h := NewFrontendHandler(db, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	w := httptest.NewRecorder()

	defaultFavicon := []byte{0x00, 0x00, 0x01, 0x00}
	h.Favicon(w, req, defaultFavicon)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	// Without active theme, should return default favicon
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestFrontendHandler_Favicon_EmptyDefaultFavicon(t *testing.T) {
	db, _ := testHandlerSetup(t)

	h := NewFrontendHandler(db, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	w := httptest.NewRecorder()

	// Empty default favicon
	var defaultFavicon []byte

	h.Favicon(w, req, defaultFavicon)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	// Should still return 200 with proper headers
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusOK)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "image/x-icon" {
		t.Errorf("Content-Type = %q; want %q", contentType, "image/x-icon")
	}
}
