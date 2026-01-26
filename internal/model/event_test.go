// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"testing"
)

func TestEventLevelConstants(t *testing.T) {
	// Verify event level constants have expected values
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"info level", EventLevelInfo, "info"},
		{"warning level", EventLevelWarning, "warning"},
		{"error level", EventLevelError, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("constant = %q, want %q", tt.constant, tt.expected)
			}
		})
	}
}

func TestEventCategoryConstants(t *testing.T) {
	// Verify event category constants have expected values
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"auth category", EventCategoryAuth, "auth"},
		{"page category", EventCategoryPage, "page"},
		{"user category", EventCategoryUser, "user"},
		{"config category", EventCategoryConfig, "config"},
		{"system category", EventCategorySystem, "system"},
		{"cache category", EventCategoryCache, "cache"},
		{"migrator category", EventCategoryMigrator, "migrator"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("constant = %q, want %q", tt.constant, tt.expected)
			}
		})
	}
}

func TestEventCategoriesUnique(t *testing.T) {
	// Verify all categories are unique
	categories := []string{
		EventCategoryAuth,
		EventCategoryPage,
		EventCategoryUser,
		EventCategoryConfig,
		EventCategorySystem,
		EventCategoryCache,
		EventCategoryMigrator,
	}

	seen := make(map[string]bool)
	for _, cat := range categories {
		if seen[cat] {
			t.Errorf("duplicate category: %q", cat)
		}
		seen[cat] = true
	}
}

func TestEventStruct(t *testing.T) {
	event := Event{
		ID:       1,
		Level:    EventLevelInfo,
		Category: EventCategoryMigrator,
		Message:  "Test message",
		Metadata: `{"key": "value"}`,
	}

	if event.ID != 1 {
		t.Errorf("ID = %d, want 1", event.ID)
	}
	if event.Level != "info" {
		t.Errorf("Level = %q, want %q", event.Level, "info")
	}
	if event.Category != "migrator" {
		t.Errorf("Category = %q, want %q", event.Category, "migrator")
	}
	if event.Message != "Test message" {
		t.Errorf("Message = %q, want %q", event.Message, "Test message")
	}
	if event.Metadata != `{"key": "value"}` {
		t.Errorf("Metadata = %q, want %q", event.Metadata, `{"key": "value"}`)
	}
}
