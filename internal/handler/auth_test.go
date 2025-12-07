package handler

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{30 * time.Second, "30 seconds"},
		{1 * time.Minute, "1 minute"},
		{5 * time.Minute, "5 minutes"},
		{1 * time.Hour, "1 hour"},
		{2 * time.Hour, "2 hours"},
		{90 * time.Second, "1 minute"},
		{150 * time.Second, "2 minutes"},
		{90 * time.Minute, "1 hour"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q; want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestNewAuthHandler(t *testing.T) {
	db := testDB(t)
	sm := testSessionManager(t)

	handler := NewAuthHandler(db, nil, sm, nil, nil)

	if handler == nil {
		t.Fatal("NewAuthHandler returned nil")
	}
	if handler.queries == nil {
		t.Error("queries should not be nil")
	}
	if handler.sessionManager != sm {
		t.Error("sessionManager not set correctly")
	}
	if handler.eventService == nil {
		t.Error("eventService should not be nil")
	}
}

// Note: Login and Logout handler methods require a renderer to set flash messages.
// Full handler testing would require a mock renderer or integration tests.
// These tests focus on validation logic and data structures.

func TestFormatDuration_EdgeCases(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{0, "0 seconds"},
		{999 * time.Millisecond, "0 seconds"},
		{59 * time.Second, "59 seconds"},
		{60 * time.Second, "1 minute"},
		{61 * time.Second, "1 minute"},
		{119 * time.Second, "1 minute"},
		{120 * time.Second, "2 minutes"},
		{59 * time.Minute, "59 minutes"},
		{60 * time.Minute, "1 hour"},
		{61 * time.Minute, "1 hour"},
		{119 * time.Minute, "1 hour"},
		{120 * time.Minute, "2 hours"},
		{24 * time.Hour, "24 hours"},
	}

	for _, tt := range tests {
		t.Run(tt.duration.String(), func(t *testing.T) {
			got := formatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q; want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestAuthHandler_LoginProtectionNil(t *testing.T) {
	db := testDB(t)
	sm := testSessionManager(t)

	// Create handler without login protection
	handler := NewAuthHandler(db, nil, sm, nil, nil)

	// Verify handler is created with nil login protection
	if handler.loginProtection != nil {
		t.Error("loginProtection should be nil")
	}

	// Note: Full Login method testing requires a mock renderer.
	// This test verifies handler construction with nil login protection.
}
