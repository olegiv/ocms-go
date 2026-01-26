// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"database/sql"
	"testing"
)

func TestNullInt64FromPtr(t *testing.T) {
	tests := []struct {
		name     string
		input    *int64
		expected sql.NullInt64
	}{
		{
			name:     "nil pointer",
			input:    nil,
			expected: sql.NullInt64{},
		},
		{
			name:     "positive value",
			input:    ptr(int64(42)),
			expected: sql.NullInt64{Int64: 42, Valid: true},
		},
		{
			name:     "zero value",
			input:    ptr(int64(0)),
			expected: sql.NullInt64{Int64: 0, Valid: true},
		},
		{
			name:     "negative value",
			input:    ptr(int64(-5)),
			expected: sql.NullInt64{Int64: -5, Valid: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NullInt64FromPtr(tt.input)
			if result != tt.expected {
				t.Errorf("NullInt64FromPtr() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestNullInt64FromValue(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected sql.NullInt64
	}{
		{
			name:     "positive value",
			input:    42,
			expected: sql.NullInt64{Int64: 42, Valid: true},
		},
		{
			name:     "zero value",
			input:    0,
			expected: sql.NullInt64{Int64: 0, Valid: true},
		},
		{
			name:     "negative value",
			input:    -5,
			expected: sql.NullInt64{Int64: -5, Valid: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NullInt64FromValue(tt.input)
			if result != tt.expected {
				t.Errorf("NullInt64FromValue() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestParseNullInt64(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected sql.NullInt64
	}{
		{
			name:     "empty string",
			input:    "",
			expected: sql.NullInt64{},
		},
		{
			name:     "zero string",
			input:    "0",
			expected: sql.NullInt64{},
		},
		{
			name:     "positive number",
			input:    "42",
			expected: sql.NullInt64{Int64: 42, Valid: true},
		},
		{
			name:     "negative number",
			input:    "-5",
			expected: sql.NullInt64{Int64: -5, Valid: true},
		},
		{
			name:     "invalid string",
			input:    "abc",
			expected: sql.NullInt64{},
		},
		{
			name:     "whitespace",
			input:    " ",
			expected: sql.NullInt64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseNullInt64(tt.input)
			if result != tt.expected {
				t.Errorf("ParseNullInt64(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseNullInt64Positive(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected sql.NullInt64
	}{
		{
			name:     "empty string",
			input:    "",
			expected: sql.NullInt64{},
		},
		{
			name:     "zero string",
			input:    "0",
			expected: sql.NullInt64{},
		},
		{
			name:     "positive number",
			input:    "42",
			expected: sql.NullInt64{Int64: 42, Valid: true},
		},
		{
			name:     "negative number",
			input:    "-5",
			expected: sql.NullInt64{},
		},
		{
			name:     "invalid string",
			input:    "abc",
			expected: sql.NullInt64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseNullInt64Positive(tt.input)
			if result != tt.expected {
				t.Errorf("ParseNullInt64Positive(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNullStringFromValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected sql.NullString
	}{
		{
			name:     "empty string",
			input:    "",
			expected: sql.NullString{},
		},
		{
			name:     "non-empty string",
			input:    "hello",
			expected: sql.NullString{String: "hello", Valid: true},
		},
		{
			name:     "whitespace only",
			input:    "  ",
			expected: sql.NullString{String: "  ", Valid: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NullStringFromValue(tt.input)
			if result != tt.expected {
				t.Errorf("NullStringFromValue(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNullStringFromPtr(t *testing.T) {
	tests := []struct {
		name     string
		input    *string
		expected sql.NullString
	}{
		{
			name:     "nil pointer",
			input:    nil,
			expected: sql.NullString{},
		},
		{
			name:     "non-empty string",
			input:    strPtr("hello"),
			expected: sql.NullString{String: "hello", Valid: true},
		},
		{
			name:     "empty string pointer",
			input:    strPtr(""),
			expected: sql.NullString{String: "", Valid: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NullStringFromPtr(tt.input)
			if result != tt.expected {
				t.Errorf("NullStringFromPtr() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// Helper functions for tests
func ptr(v int64) *int64 {
	return &v
}

func strPtr(s string) *string {
	return &s
}
