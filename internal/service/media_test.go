// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal.jpg", "normal.jpg"},
		{"file name.jpg", "file-name.jpg"},
		{"file'name.jpg", "filename.jpg"},
		{"file\"name.jpg", "filename.jpg"},
		{"<script>.jpg", "script.jpg"},
		{"file&name.jpg", "filename.jpg"},
		{"path/to/file.jpg", "file.jpg"},
		{"../../../etc/passwd", "passwd.bin"},
		{"noextension", "noextension.bin"},
		{"file#name?.jpg", "filename.jpg"},
		{"file%20name.jpg", "file20name.jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := sanitizeFilename(tt.input); got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetMimeTypeFromExtension(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"image.jpg", model.MimeTypeJPEG},
		{"image.jpeg", model.MimeTypeJPEG},
		{"IMAGE.JPG", model.MimeTypeJPEG},
		{"photo.png", model.MimeTypePNG},
		{"animation.gif", model.MimeTypeGIF},
		{"modern.webp", model.MimeTypeWebP},
		{"document.pdf", model.MimeTypePDF},
		{"video.mp4", model.MimeTypeMP4},
		{"video.webm", model.MimeTypeWebM},
		{"unknown.xyz", "application/octet-stream"},
		{"noextension", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			if got := getMimeTypeFromExtension(tt.filename); got != tt.want {
				t.Errorf("getMimeTypeFromExtension(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestIsImageMimeType(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{model.MimeTypeJPEG, true},
		{model.MimeTypePNG, true},
		{model.MimeTypeGIF, true},
		{model.MimeTypeWebP, true},
		{model.MimeTypePDF, false},
		{model.MimeTypeMP4, false},
		{model.MimeTypeWebM, false},
		{"text/plain", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := isImageMimeType(tt.mimeType); got != tt.want {
				t.Errorf("isImageMimeType(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestAllowedMimeTypes(t *testing.T) {
	// Verify all expected types are allowed
	expected := []string{
		model.MimeTypeJPEG,
		model.MimeTypePNG,
		model.MimeTypeGIF,
		model.MimeTypeWebP,
		model.MimeTypePDF,
		model.MimeTypeMP4,
		model.MimeTypeWebM,
	}

	for _, mt := range expected {
		if !AllowedMimeTypes[mt] {
			t.Errorf("Expected %q to be in AllowedMimeTypes", mt)
		}
	}

	// Verify unknown types are not allowed
	disallowed := []string{
		"text/plain",
		"application/javascript",
		"text/html",
	}

	for _, mt := range disallowed {
		if AllowedMimeTypes[mt] {
			t.Errorf("Expected %q to NOT be in AllowedMimeTypes", mt)
		}
	}
}

func TestDetectAndValidateUploadMime(t *testing.T) {
	t.Run("accepts valid png with png extension", func(t *testing.T) {
		file := tempMultipartFile(t, []byte{
			0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
			0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		})
		defer func() { _ = file.Close() }()

		mimeType, err := detectAndValidateUploadMime(file, "image.png")
		if err != nil {
			t.Fatalf("expected valid upload, got error: %v", err)
		}
		if mimeType != model.MimeTypePNG {
			t.Fatalf("expected mime %q, got %q", model.MimeTypePNG, mimeType)
		}
	})

	t.Run("rejects extension mismatch", func(t *testing.T) {
		file := tempMultipartFile(t, []byte{
			0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
			0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		})
		defer func() { _ = file.Close() }()

		_, err := detectAndValidateUploadMime(file, "image.html")
		if err == nil {
			t.Fatal("expected extension mismatch error, got nil")
		}
	})

	t.Run("rejects disallowed content type even with allowed extension", func(t *testing.T) {
		file := tempMultipartFile(t, []byte("<!doctype html><html><body>x</body></html>"))
		defer func() { _ = file.Close() }()

		_, err := detectAndValidateUploadMime(file, "image.png")
		if err == nil {
			t.Fatal("expected disallowed mime type error, got nil")
		}
	})
}

func TestCanonicalizeUploadFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		mimeType string
		want     string
		wantErr  bool
	}{
		{
			name:     "normalizes jpeg extension",
			filename: "photo.jpeg",
			mimeType: model.MimeTypeJPEG,
			want:     "photo.jpg",
		},
		{
			name:     "preserves sanitized basename",
			filename: "my file.png",
			mimeType: model.MimeTypePNG,
			want:     "my-file.png",
		},
		{
			name:     "empty basename falls back",
			filename: ".jpeg",
			mimeType: model.MimeTypeJPEG,
			want:     "file.jpg",
		},
		{
			name:     "unsupported mime",
			filename: "note.txt",
			mimeType: "text/plain",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalizeUploadFilename(tt.filename, tt.mimeType)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalizeUploadFilename() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("canonicalizeUploadFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func tempMultipartFile(t *testing.T, content []byte) *os.File {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "upload.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}

	return file
}
