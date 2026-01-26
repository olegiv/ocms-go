// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
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
