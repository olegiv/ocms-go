package util

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple title",
			input:    "Hello World",
			expected: "hello-world",
		},
		{
			name:     "with special characters",
			input:    "Hello, World!",
			expected: "hello-world",
		},
		{
			name:     "with numbers",
			input:    "Page 123",
			expected: "page-123",
		},
		{
			name:     "with accents",
			input:    "Café résumé",
			expected: "cafe-resume",
		},
		{
			name:     "with multiple spaces",
			input:    "Hello   World",
			expected: "hello-world",
		},
		{
			name:     "with hyphens",
			input:    "Hello - World",
			expected: "hello-world",
		},
		{
			name:     "with leading/trailing spaces",
			input:    "  Hello World  ",
			expected: "hello-world",
		},
		{
			name:     "all special characters",
			input:    "!@#$%^&*()",
			expected: "",
		},
		{
			name:     "unicode characters",
			input:    "日本語タイトル",
			expected: "",
		},
		{
			name:     "german umlauts",
			input:    "Über München",
			expected: "uber-munchen",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single word",
			input:    "Hello",
			expected: "hello",
		},
		{
			name:     "mixed case",
			input:    "HeLLo WoRLd",
			expected: "hello-world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Slugify(tt.input)
			if result != tt.expected {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsValidSlug(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid simple slug",
			input:    "hello-world",
			expected: true,
		},
		{
			name:     "valid slug with numbers",
			input:    "page-123",
			expected: true,
		},
		{
			name:     "valid single word",
			input:    "hello",
			expected: true,
		},
		{
			name:     "valid numbers only",
			input:    "123",
			expected: true,
		},
		{
			name:     "invalid - empty",
			input:    "",
			expected: false,
		},
		{
			name:     "invalid - uppercase",
			input:    "Hello-World",
			expected: false,
		},
		{
			name:     "invalid - spaces",
			input:    "hello world",
			expected: false,
		},
		{
			name:     "invalid - special chars",
			input:    "hello!world",
			expected: false,
		},
		{
			name:     "invalid - starts with hyphen",
			input:    "-hello",
			expected: false,
		},
		{
			name:     "invalid - ends with hyphen",
			input:    "hello-",
			expected: false,
		},
		{
			name:     "invalid - consecutive hyphens",
			input:    "hello--world",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidSlug(tt.input)
			if result != tt.expected {
				t.Errorf("IsValidSlug(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
