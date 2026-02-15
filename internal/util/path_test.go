// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     string
		wantErr  bool
	}{
		{
			name:  "simple filename",
			input: "image.jpg",
			want:  "image.jpg",
		},
		{
			name:  "filename with spaces",
			input: "my image.jpg",
			want:  "my image.jpg",
		},
		{
			name:  "path traversal attempt",
			input: "../../../etc/passwd",
			want:  "passwd",
		},
		{
			name:  "path with directory",
			input: "uploads/images/photo.png",
			want:  "photo.png",
		},
		{
			name:  "absolute path",
			input: "/var/www/uploads/file.txt",
			want:  "file.txt",
		},
		{
			name:    "single dot",
			input:   ".",
			wantErr: true,
		},
		{
			name:    "double dot",
			input:   "..",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:  "hidden file",
			input: ".htaccess",
			want:  ".htaccess",
		},
		{
			name:  "double extension",
			input: "file.tar.gz",
			want:  "file.tar.gz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeFilename(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("SanitizeFilename() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("SanitizeFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidatePathWithinBase(t *testing.T) {
	// Create a temporary directory structure for testing
	tmpDir := t.TempDir()

	uploadsDir := filepath.Join(tmpDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		t.Fatalf("Failed to create uploads dir: %v", err)
	}

	tests := []struct {
		name       string
		basePath   string
		targetPath string
		wantErr    bool
	}{
		{
			name:       "same directory",
			basePath:   uploadsDir,
			targetPath: uploadsDir,
			wantErr:    false,
		},
		{
			name:       "subdirectory",
			basePath:   uploadsDir,
			targetPath: filepath.Join(uploadsDir, "images"),
			wantErr:    false,
		},
		{
			name:       "deep subdirectory",
			basePath:   uploadsDir,
			targetPath: filepath.Join(uploadsDir, "images", "2024", "01"),
			wantErr:    false,
		},
		{
			name:       "traversal to parent",
			basePath:   uploadsDir,
			targetPath: filepath.Join(uploadsDir, ".."),
			wantErr:    true,
		},
		{
			name:       "traversal with subdirectory",
			basePath:   uploadsDir,
			targetPath: filepath.Join(uploadsDir, "images", "..", ".."),
			wantErr:    true,
		},
		{
			name:       "traversal to sibling",
			basePath:   uploadsDir,
			targetPath: filepath.Join(uploadsDir, "..", "config"),
			wantErr:    true,
		},
		{
			name:       "absolute path outside base",
			basePath:   uploadsDir,
			targetPath: "/etc/passwd",
			wantErr:    true,
		},
		{
			name:       "similar prefix but different directory",
			basePath:   uploadsDir,
			targetPath: uploadsDir + "-malicious",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePathWithinBase(tt.basePath, tt.targetPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePathWithinBase() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSafeJoinPath(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	tests := []struct {
		name       string
		basePath   string
		components []string
		wantErr    bool
	}{
		{
			name:       "simple join",
			basePath:   tmpDir,
			components: []string{"uploads", "file.txt"},
			wantErr:    false,
		},
		{
			name:       "traversal in component",
			basePath:   tmpDir,
			components: []string{"..", "secret.txt"},
			wantErr:    true,
		},
		{
			name:       "hidden traversal",
			basePath:   tmpDir,
			components: []string{"uploads", "..", "..", "etc", "passwd"},
			wantErr:    true,
		},
		{
			// Note: filepath.Join("/base", "/etc/passwd") returns "/base/etc/passwd"
			// on Unix - it does NOT treat the second path as absolute like Python does.
			// So this path IS within the base and is safe.
			name:       "absolute path component (safe in Go)",
			basePath:   tmpDir,
			components: []string{"/etc/passwd"},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafeJoinPath(tt.basePath, tt.components...)
			if (err != nil) != tt.wantErr {
				t.Errorf("SafeJoinPath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestContainsPathTraversal(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		want  bool
	}{
		{
			name: "simple path",
			path: "uploads/file.txt",
			want: false,
		},
		{
			name: "leading double dot",
			path: "../etc/passwd",
			want: true,
		},
		{
			// Note: "uploads/../config/secret.txt" cleans to "config/secret.txt"
			// which has no ".." - the traversal was resolved within the path.
			// This doesn't escape the working directory.
			name: "middle double dot (resolved)",
			path: "uploads/../config/secret.txt",
			want: false,
		},
		{
			name: "multiple traversals",
			path: "../../../../../../etc/passwd",
			want: true,
		},
		{
			name: "single dot is safe",
			path: "./uploads/file.txt",
			want: false,
		},
		{
			name: "double dot in filename is safe",
			path: "file..name.txt",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsPathTraversal(tt.path); got != tt.want {
				t.Errorf("ContainsPathTraversal(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
