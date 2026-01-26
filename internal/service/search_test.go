// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"testing"
)

func TestSearchServiceEscapeQuery(t *testing.T) {
	service := &SearchService{}

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "simple word",
			query: "hello",
			want:  `"hello"*`,
		},
		{
			name:  "multiple words",
			query: "hello world",
			want:  `"hello"* OR "world"*`,
		},
		{
			name:  "special characters removed",
			query: "hello:world*test",
			want:  `"hello"* OR "world"* OR "test"*`,
		},
		{
			name:  "quotes removed",
			query: `"quoted phrase"`,
			want:  `"quoted"* OR "phrase"*`,
		},
		{
			name:  "parentheses removed",
			query: "(hello OR world)",
			want:  `"hello"* OR "OR"* OR "world"*`,
		},
		{
			name:  "empty string",
			query: "",
			want:  "",
		},
		{
			name:  "only special characters",
			query: ":*^()",
			want:  "",
		},
		{
			name:  "unicode characters preserved (Cyrillic)",
			query: "привет мир",
			want:  `"привет"* OR "мир"*`,
		},
		{
			name:  "unicode characters preserved (Chinese)",
			query: "你好世界",
			want:  `"你好世界"*`,
		},
		{
			name:  "mixed ASCII and Unicode",
			query: "hello мир test",
			want:  `"hello"* OR "мир"* OR "test"*`,
		},
		{
			name:  "whitespace trimmed",
			query: "  hello  world  ",
			want:  `"hello"* OR "world"*`,
		},
		{
			name:  "underscore preserved",
			query: "hello_world",
			want:  `"hello_world"*`,
		},
		{
			name:  "hyphen preserved",
			query: "hello-world",
			want:  `"hello-world"*`,
		},
		{
			name:  "numbers preserved",
			query: "test123",
			want:  `"test123"*`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.escapeQuery(tt.query)
			if got != tt.want {
				t.Errorf("escapeQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestSearchServiceGenerateExcerpt(t *testing.T) {
	service := &SearchService{}

	tests := []struct {
		name   string
		body   string
		query  string
		maxLen int
		check  func(t *testing.T, got string)
	}{
		{
			name:   "short body",
			body:   "Hello world",
			query:  "hello",
			maxLen: 200,
			check: func(t *testing.T, got string) {
				if got != "Hello world" {
					t.Errorf("got %q, want %q", got, "Hello world")
				}
			},
		},
		{
			name:   "body longer than maxLen without match",
			body:   "Lorem ipsum dolor sit amet consectetur adipiscing elit. This is some very long text that exceeds the maximum length we want to show in the excerpt.",
			query:  "xyz",
			maxLen: 50,
			check: func(t *testing.T, got string) {
				if len(got) > 60 { // Allow some margin for ...
					t.Errorf("excerpt too long: %d chars", len(got))
				}
			},
		},
		{
			name:   "empty body",
			body:   "",
			query:  "test",
			maxLen: 200,
			check: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("got %q, want empty", got)
				}
			},
		},
		{
			name:   "html tags stripped",
			body:   "<p>Hello <strong>world</strong></p>",
			query:  "hello",
			maxLen: 200,
			check: func(t *testing.T, got string) {
				if got == "" {
					t.Error("excerpt should not be empty")
				}
				// Should not contain HTML tags
				if contains(got, "<") || contains(got, ">") {
					t.Errorf("excerpt should not contain HTML tags: %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.generateExcerpt(tt.body, tt.query, tt.maxLen)
			tt.check(t, got)
		})
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple paragraph",
			input: "<p>Hello World</p>",
			want:  "Hello World",
		},
		{
			name:  "nested tags",
			input: "<div><p>Hello <strong>World</strong></p></div>",
			want:  "Hello World",
		},
		{
			name:  "multiple spaces normalized",
			input: "<p>Hello</p>   <p>World</p>",
			want:  "Hello World",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "no tags",
			input: "Hello World",
			want:  "Hello World",
		},
		{
			name:  "html entities decoded",
			input: "Hello &amp; World",
			want:  "Hello & World",
		},
		{
			name:  "script tags removed",
			input: "<script>alert('xss')</script>Safe content",
			want:  "alert('xss')Safe content",
		},
		{
			name:  "self-closing tags",
			input: "Line 1<br/>Line 2<hr>Line 3",
			want:  "Line 1Line 2Line 3",
		},
		{
			name:  "attributes removed with tags",
			input: `<a href="http://example.com" class="link">Click me</a>`,
			want:  "Click me",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTMLTags(tt.input)
			if got != tt.want {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSearchParams(t *testing.T) {
	params := SearchParams{
		Query:  "test query",
		Limit:  10,
		Offset: 20,
	}

	if params.Query != "test query" {
		t.Errorf("Query = %q, want %q", params.Query, "test query")
	}
	if params.Limit != 10 {
		t.Errorf("Limit = %d, want 10", params.Limit)
	}
	if params.Offset != 20 {
		t.Errorf("Offset = %d, want 20", params.Offset)
	}
}

func TestNewSearchService(t *testing.T) {
	// NewSearchService with nil db (just testing it doesn't panic)
	service := NewSearchService(nil)
	if service == nil {
		t.Fatal("NewSearchService(nil) returned nil")
	}
	if service.db != nil {
		t.Error("db should be nil when passed nil")
	}
}

// Helper function
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
