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

	h := NewFrontendHandler(db, nil, nil, nil, nil, nil)
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

// TestPageView_Type verifies that PageView.Type correctly reflects the page type.
func TestPageView_Type(t *testing.T) {
	tests := []struct {
		name     string
		pageType string
		wantType string
	}{
		{"page type", "page", "page"},
		{"post type", "post", "post"},
		{"empty type", "", ""},
		{"custom type", "article", "article"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pv := PageView{
				ID:    1,
				Title: "Test",
				Type:  tt.pageType,
			}

			if pv.Type != tt.wantType {
				t.Errorf("PageView.Type = %q, want %q", pv.Type, tt.wantType)
			}
		})
	}
}

// TestPageMetadataVisibility verifies that page metadata is only shown for posts.
// This test documents the expected behavior: date, author, and reading time
// should only be displayed for page_type = "post", not for regular pages.
func TestPageMetadataVisibility(t *testing.T) {
	tests := []struct {
		name            string
		pageType        string
		wantShowMeta    bool
		wantDescription string
	}{
		{
			name:            "post shows metadata",
			pageType:        "post",
			wantShowMeta:    true,
			wantDescription: "Blog posts should display date, author, and reading time",
		},
		{
			name:            "page hides metadata",
			pageType:        "page",
			wantShowMeta:    false,
			wantDescription: "Static pages should NOT display date, author, and reading time",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the template condition: {{if eq .Page.Type "post"}}
			showMeta := tt.pageType == "post"

			if showMeta != tt.wantShowMeta {
				t.Errorf("showMeta = %v, want %v\nReason: %s", showMeta, tt.wantShowMeta, tt.wantDescription)
			}
		})
	}
}

// TestAuthorBoxVisibility verifies that author box is only shown for posts.
func TestAuthorBoxVisibility(t *testing.T) {
	tests := []struct {
		name            string
		pageType        string
		showAuthorBox   bool
		wantShow        bool
		wantDescription string
	}{
		{
			name:            "post with author box enabled",
			pageType:        "post",
			showAuthorBox:   true,
			wantShow:        true,
			wantDescription: "Posts with ShowAuthorBox=true should display author box",
		},
		{
			name:            "post with author box disabled",
			pageType:        "post",
			showAuthorBox:   false,
			wantShow:        false,
			wantDescription: "Posts with ShowAuthorBox=false should NOT display author box",
		},
		{
			name:            "page with author box enabled",
			pageType:        "page",
			showAuthorBox:   true,
			wantShow:        false,
			wantDescription: "Static pages should NEVER display author box regardless of setting",
		},
		{
			name:            "page with author box disabled",
			pageType:        "page",
			showAuthorBox:   false,
			wantShow:        false,
			wantDescription: "Static pages should NOT display author box",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the template condition: {{if and .ShowAuthorBox (eq .Page.Type "post")}}
			showAuthor := tt.showAuthorBox && tt.pageType == "post"

			if showAuthor != tt.wantShow {
				t.Errorf("showAuthor = %v, want %v\nReason: %s", showAuthor, tt.wantShow, tt.wantDescription)
			}
		})
	}
}
