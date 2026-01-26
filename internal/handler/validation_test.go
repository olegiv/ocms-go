// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"errors"
	"testing"
)

func TestValidateSlugWithChecker(t *testing.T) {
	tests := []struct {
		name        string
		slug        string
		checkExists SlugExistsFunc
		want        string
	}{
		{
			name:        "valid unique slug",
			slug:        "my-page",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "",
		},
		{
			name:        "empty slug",
			slug:        "",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "Slug is required",
		},
		{
			name:        "invalid format - uppercase",
			slug:        "My-Page",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "Invalid slug format (use lowercase letters, numbers, and hyphens)",
		},
		{
			name:        "invalid format - spaces",
			slug:        "my page",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "Invalid slug format (use lowercase letters, numbers, and hyphens)",
		},
		{
			name:        "invalid format - special chars",
			slug:        "my_page!",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "Invalid slug format (use lowercase letters, numbers, and hyphens)",
		},
		{
			name:        "slug already exists",
			slug:        "existing-slug",
			checkExists: func() (int64, error) { return 1, nil },
			want:        "Slug already exists",
		},
		{
			name:        "database error",
			slug:        "valid-slug",
			checkExists: func() (int64, error) { return 0, errors.New("db error") },
			want:        "Error checking slug",
		},
		{
			name:        "valid with numbers",
			slug:        "page-123",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "",
		},
		{
			name:        "valid single word",
			slug:        "about",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateSlugWithChecker(tt.slug, tt.checkExists)
			if got != tt.want {
				t.Errorf("ValidateSlugWithChecker(%q) = %q, want %q", tt.slug, got, tt.want)
			}
		})
	}
}

func TestValidateSlugForUpdate(t *testing.T) {
	tests := []struct {
		name        string
		slug        string
		currentSlug string
		checkExists SlugExistsFunc
		want        string
	}{
		{
			name:        "unchanged slug",
			slug:        "my-page",
			currentSlug: "my-page",
			checkExists: func() (int64, error) { return 1, nil }, // Would fail if checked
			want:        "",
		},
		{
			name:        "changed to valid unique slug",
			slug:        "new-page",
			currentSlug: "old-page",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "",
		},
		{
			name:        "changed to existing slug",
			slug:        "taken-page",
			currentSlug: "old-page",
			checkExists: func() (int64, error) { return 1, nil },
			want:        "Slug already exists",
		},
		{
			name:        "changed to invalid format",
			slug:        "Invalid Slug",
			currentSlug: "old-page",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "Invalid slug format (use lowercase letters, numbers, and hyphens)",
		},
		{
			name:        "changed to empty",
			slug:        "",
			currentSlug: "old-page",
			checkExists: func() (int64, error) { return 0, nil },
			want:        "Slug is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateSlugForUpdate(tt.slug, tt.currentSlug, tt.checkExists)
			if got != tt.want {
				t.Errorf("ValidateSlugForUpdate(%q, %q) = %q, want %q", tt.slug, tt.currentSlug, got, tt.want)
			}
		})
	}
}

func TestValidateSlugFormat(t *testing.T) {
	tests := []struct {
		name string
		slug string
		want string
	}{
		{"valid slug", "my-page", ""},
		{"valid with numbers", "page-123", ""},
		{"valid single word", "about", ""},
		{"valid long slug", "this-is-a-very-long-slug-for-testing", ""},
		{"empty slug", "", "Slug is required"},
		{"uppercase", "My-Page", "Invalid slug format"},
		{"spaces", "my page", "Invalid slug format"},
		{"underscore", "my_page", "Invalid slug format"},
		{"special chars", "page@123", "Invalid slug format"},
		{"leading hyphen", "-page", "Invalid slug format"},
		{"trailing hyphen", "page-", "Invalid slug format"},
		{"double hyphen", "my--page", "Invalid slug format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateSlugFormat(tt.slug)
			if got != tt.want {
				t.Errorf("ValidateSlugFormat(%q) = %q, want %q", tt.slug, got, tt.want)
			}
		})
	}
}
