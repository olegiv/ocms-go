// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import "testing"

func TestImportResult_TotalImported(t *testing.T) {
	tests := []struct {
		name   string
		result ImportResult
		want   int
	}{
		{
			name:   "empty result",
			result: ImportResult{},
			want:   0,
		},
		{
			name: "tags only",
			result: ImportResult{
				TagsImported: 5,
			},
			want: 5,
		},
		{
			name: "posts only",
			result: ImportResult{
				PostsImported: 10,
			},
			want: 10,
		},
		{
			name: "users only",
			result: ImportResult{
				UsersImported: 15,
			},
			want: 15,
		},
		{
			name: "media only",
			result: ImportResult{
				MediaImported: 20,
			},
			want: 20,
		},
		{
			name: "all types",
			result: ImportResult{
				TagsImported:  5,
				MediaImported: 10,
				PostsImported: 15,
				UsersImported: 20,
			},
			want: 50,
		},
		{
			name: "with skipped (not counted)",
			result: ImportResult{
				TagsImported:  5,
				PostsImported: 10,
				UsersImported: 3,
				TagsSkipped:   2,
				PostsSkipped:  5,
				UsersSkipped:  7,
			},
			want: 18, // Only imported counts
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.TotalImported(); got != tt.want {
				t.Errorf("TotalImported() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestImportResult_TotalSkipped(t *testing.T) {
	tests := []struct {
		name   string
		result ImportResult
		want   int
	}{
		{
			name:   "empty result",
			result: ImportResult{},
			want:   0,
		},
		{
			name: "tags skipped only",
			result: ImportResult{
				TagsSkipped: 5,
			},
			want: 5,
		},
		{
			name: "posts skipped only",
			result: ImportResult{
				PostsSkipped: 10,
			},
			want: 10,
		},
		{
			name: "users skipped only",
			result: ImportResult{
				UsersSkipped: 15,
			},
			want: 15,
		},
		{
			name: "media skipped only",
			result: ImportResult{
				MediaSkipped: 20,
			},
			want: 20,
		},
		{
			name: "all types skipped",
			result: ImportResult{
				TagsSkipped:  5,
				MediaSkipped: 10,
				PostsSkipped: 15,
				UsersSkipped: 20,
			},
			want: 50,
		},
		{
			name: "with imported (not counted)",
			result: ImportResult{
				TagsImported:  5,
				PostsImported: 10,
				UsersImported: 3,
				TagsSkipped:   2,
				PostsSkipped:  5,
				UsersSkipped:  7,
			},
			want: 14, // Only skipped counts
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.TotalSkipped(); got != tt.want {
				t.Errorf("TotalSkipped() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestImportResult_HasErrors(t *testing.T) {
	tests := []struct {
		name   string
		result ImportResult
		want   bool
	}{
		{
			name:   "no errors",
			result: ImportResult{},
			want:   false,
		},
		{
			name: "nil errors slice",
			result: ImportResult{
				Errors: nil,
			},
			want: false,
		},
		{
			name: "empty errors slice",
			result: ImportResult{
				Errors: []string{},
			},
			want: false,
		},
		{
			name: "one error",
			result: ImportResult{
				Errors: []string{"failed to import user"},
			},
			want: true,
		},
		{
			name: "multiple errors",
			result: ImportResult{
				Errors: []string{
					"failed to import user 1",
					"failed to import user 2",
					"duplicate email",
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasErrors(); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestImportOptions_Fields(t *testing.T) {
	// Test that all import options can be set independently
	opts := ImportOptions{
		ImportTags:   true,
		ImportMedia:  false,
		ImportPosts:  true,
		ImportUsers:  true,
		SkipExisting: false,
	}

	if !opts.ImportTags {
		t.Error("ImportTags should be true")
	}
	if opts.ImportMedia {
		t.Error("ImportMedia should be false")
	}
	if !opts.ImportPosts {
		t.Error("ImportPosts should be true")
	}
	if !opts.ImportUsers {
		t.Error("ImportUsers should be true")
	}
	if opts.SkipExisting {
		t.Error("SkipExisting should be false")
	}
}

func TestImportResult_CombinedScenarios(t *testing.T) {
	// Test a realistic import scenario
	result := ImportResult{
		TagsImported:  10,
		PostsImported: 50,
		UsersImported: 25,
		MediaImported: 100,
		TagsSkipped:   2,
		PostsSkipped:  5,
		UsersSkipped:  10,
		MediaSkipped:  15,
		Errors: []string{
			"failed to create user: duplicate email",
			"failed to import media: file not found",
		},
	}

	// Verify totals
	expectedImported := 10 + 50 + 25 + 100
	if got := result.TotalImported(); got != expectedImported {
		t.Errorf("TotalImported() = %d, want %d", got, expectedImported)
	}

	expectedSkipped := 2 + 5 + 10 + 15
	if got := result.TotalSkipped(); got != expectedSkipped {
		t.Errorf("TotalSkipped() = %d, want %d", got, expectedSkipped)
	}

	if !result.HasErrors() {
		t.Error("HasErrors() should be true")
	}

	if len(result.Errors) != 2 {
		t.Errorf("Expected 2 errors, got %d", len(result.Errors))
	}
}
