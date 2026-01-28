// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"image"
	"image/color"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

// createTestImage creates a simple test image with the given dimensions.
func createTestImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	return img
}

// runMimeTypeTests runs table-driven tests for mime type checking functions.
func runMimeTypeTests(t *testing.T, checkFn func(string) bool, tests []struct {
	mimeType string
	want     bool
}) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := checkFn(tt.mimeType); got != tt.want {
				t.Errorf("check(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestProcessorIsImage(t *testing.T) {
	p := NewProcessor("./uploads")

	runMimeTypeTests(t, p.IsImage, []struct {
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
		{"application/octet-stream", false},
		{"", false},
	})
}

func TestProcessorIsSupportedType(t *testing.T) {
	p := NewProcessor("./uploads")

	runMimeTypeTests(t, p.IsSupportedType, []struct {
		mimeType string
		want     bool
	}{
		{model.MimeTypeJPEG, true},
		{model.MimeTypePNG, true},
		{model.MimeTypeGIF, true},
		{model.MimeTypeWebP, true},
		{model.MimeTypePDF, true},
		{model.MimeTypeMP4, true},
		{model.MimeTypeWebM, true},
		{"application/octet-stream", false},
		{"text/plain", false},
	})
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg magic bytes", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "jpeg"},
		{"png magic bytes", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, "png"},
		{"gif magic bytes", []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, "gif"},
		{"unknown", []byte{0x00, 0x01, 0x02, 0x03}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectFormat(tt.data); got != tt.want {
				t.Errorf("detectFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectFormatFromFilename(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"image.jpg", "jpeg"},
		{"image.jpeg", "jpeg"},
		{"image.JPG", "jpeg"},
		{"image.png", "png"},
		{"image.PNG", "png"},
		{"image.gif", "gif"},
		{"image.webp", "webp"},
		{"image.unknown", "jpeg"},
		{"noextension", "jpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			if got := detectFormatFromFilename(tt.filename); got != tt.want {
				t.Errorf("detectFormatFromFilename(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestFormatToMimeType(t *testing.T) {
	tests := []struct {
		format string
		want   string
	}{
		{"jpeg", model.MimeTypeJPEG},
		{"jpg", model.MimeTypeJPEG},
		{"png", model.MimeTypePNG},
		{"gif", model.MimeTypeGIF},
		{"webp", model.MimeTypeWebP},
		{"unknown", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			if got := formatToMimeType(tt.format); got != tt.want {
				t.Errorf("formatToMimeType(%q) = %v, want %v", tt.format, got, tt.want)
			}
		})
	}
}

func TestApplyOrientation(t *testing.T) {
	// applyOrientation should return the same image for orientation 1 (normal)
	// For other orientations, it should transform the image
	// We just verify it doesn't panic for all orientations 1-8
	tests := []int{1, 2, 3, 4, 5, 6, 7, 8, 0, 9}

	for _, orientation := range tests {
		t.Run("orientation_"+string(rune('0'+orientation)), func(t *testing.T) {
			// Create a simple 10x10 test image
			img := createTestImage(10, 10)
			result := applyOrientation(img, orientation)
			if result == nil {
				t.Error("applyOrientation returned nil")
			}
		})
	}
}
