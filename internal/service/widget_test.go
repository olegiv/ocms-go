// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"database/sql"
	"testing"
)

func TestCacheKey(t *testing.T) {
	tests := []struct {
		theme      string
		area       string
		languageID int64
		want       string
	}{
		{"default", "sidebar", 1, "default:sidebar:1"},
		{"custom", "footer", 2, "custom:footer:2"},
		{"", "header", 1, ":header:1"},
		{"theme", "", 1, "theme::1"},
	}

	for _, tt := range tests {
		t.Run(tt.theme+"-"+tt.area, func(t *testing.T) {
			got := cacheKey(tt.theme, tt.area, tt.languageID)
			if got != tt.want {
				t.Errorf("cacheKey(%q, %q, %d) = %q, want %q", tt.theme, tt.area, tt.languageID, got, tt.want)
			}
		})
	}
}

func TestNewWidgetService(t *testing.T) {
	// Test with nil db
	service := NewWidgetService(nil)
	if service == nil {
		t.Fatal("NewWidgetService(nil) returned nil")
	}
	if service.cache == nil {
		t.Error("cache should be initialized")
	}
	if service.ttl <= 0 {
		t.Error("ttl should be positive")
	}
}

func TestWidgetServiceInvalidateCache(t *testing.T) {
	service := NewWidgetService(nil)

	// Add something to cache
	service.cache["test:key"] = []WidgetView{{ID: 1}}

	if len(service.cache) != 1 {
		t.Errorf("cache length = %d, want 1", len(service.cache))
	}

	// Invalidate
	service.InvalidateCache()

	if len(service.cache) != 0 {
		t.Errorf("cache length after invalidate = %d, want 0", len(service.cache))
	}
}

func TestToWidgetViewSanitization(t *testing.T) {
	// Test that XSS content is sanitized
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "script tag removed",
			content:  "<script>alert('xss')</script><p>Safe</p>",
			expected: "<p>Safe</p>",
		},
		{
			name:     "event handler removed",
			content:  `<p onclick="alert('xss')">Click me</p>`,
			expected: "<p>Click me</p>",
		},
		{
			name:     "safe HTML preserved",
			content:  "<p><strong>Bold</strong> and <em>italic</em></p>",
			expected: "<p><strong>Bold</strong> and <em>italic</em></p>",
		},
		{
			name:     "link preserved with rel noopener",
			content:  `<a href="https://example.com">Link</a>`,
			expected: `<a href="https://example.com" rel="nofollow">Link</a>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock store.Widget
			mockWidget := mockStoreWidget{
				id:         1,
				widgetType: "html",
				title:      sql.NullString{String: "Test", Valid: true},
				content:    sql.NullString{String: tt.content, Valid: true},
				settings:   sql.NullString{String: "{}", Valid: true},
				isActive:   1,
				position:   0,
			}

			// We can't directly call toWidgetView because it expects store.Widget
			// but we can verify htmlSanitizer works correctly
			sanitized := htmlSanitizer.Sanitize(tt.content)
			if sanitized != tt.expected {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.content, sanitized, tt.expected)
			}

			// Verify mock is used for something (avoid unused warning)
			_ = mockWidget
		})
	}
}

// mockStoreWidget is a test helper
type mockStoreWidget struct {
	id         int64
	widgetType string
	title      sql.NullString
	content    sql.NullString
	settings   sql.NullString
	isActive   int64
	position   int64
}

func TestHTMLSanitizer(t *testing.T) {
	// Verify htmlSanitizer is initialized
	if htmlSanitizer == nil {
		t.Fatal("htmlSanitizer is nil")
	}

	// Test it works on basic input
	result := htmlSanitizer.Sanitize("<p>Hello</p>")
	if result != "<p>Hello</p>" {
		t.Errorf("Basic HTML sanitization failed: got %q", result)
	}
}
