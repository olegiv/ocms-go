// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
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

func TestCreateAllVariants(t *testing.T) {
	// Create a temp directory for uploads
	uploadsDir := t.TempDir()
	p := NewProcessor(uploadsDir)

	// Create a source image large enough for all variants (2400x1600)
	sourceImg := createTestImage(2400, 1600)
	uuid := "test-uuid-all-variants"

	// Save source image first via ProcessImage
	result, err := p.ProcessImage(
		func() *bytes.Reader {
			var buf bytes.Buffer
			_ = png.Encode(&buf, sourceImg)
			return bytes.NewReader(buf.Bytes())
		}(),
		uuid,
		"test-all.png",
	)
	if err != nil {
		t.Fatalf("ProcessImage: %v", err)
	}

	// Create all variants
	variants, err := p.CreateAllVariants(result.FilePath, uuid, "test-all.png")
	if err != nil {
		t.Fatalf("CreateAllVariants: %v", err)
	}

	if len(variants) == 0 {
		t.Fatal("CreateAllVariants returned no variants")
	}

	// All 4 variants should be created for a 2400x1600 image
	if len(variants) != 4 {
		t.Errorf("variant count = %d, want 4", len(variants))
	}

	// Verify each variant has valid dimensions
	for _, v := range variants {
		if v.Width <= 0 || v.Height <= 0 {
			t.Errorf("variant %q has invalid dimensions: %dx%d", v.Type, v.Width, v.Height)
		}
		if v.Size <= 0 {
			t.Errorf("variant %q has invalid size: %d", v.Type, v.Size)
		}
	}
}

func TestCreateAllVariants_SmallSource(t *testing.T) {
	// Create a temp directory for uploads
	uploadsDir := t.TempDir()
	p := NewProcessor(uploadsDir)

	// Create a small source image (100x100) - smaller than all non-crop variants
	sourceImg := createTestImage(100, 100)
	uuid := "test-uuid-small-source"

	// Save source image
	result, err := p.ProcessImage(
		func() *bytes.Reader {
			var buf bytes.Buffer
			_ = png.Encode(&buf, sourceImg)
			return bytes.NewReader(buf.Bytes())
		}(),
		uuid,
		"test-small.png",
	)
	if err != nil {
		t.Fatalf("ProcessImage: %v", err)
	}

	// Create all variants - should only create thumbnail (crop=true)
	variants, err := p.CreateAllVariants(result.FilePath, uuid, "test-small.png")
	if err != nil {
		t.Fatalf("CreateAllVariants: %v", err)
	}

	// Only thumbnail should be created (it crops, so source size doesn't matter)
	if len(variants) != 1 {
		t.Errorf("variant count = %d, want 1 (only thumbnail for small source)", len(variants))
		for _, v := range variants {
			t.Logf("  variant: %s (%dx%d)", v.Type, v.Width, v.Height)
		}
	}
}

func TestCreateAllVariants_InvalidSource(t *testing.T) {
	uploadsDir := t.TempDir()
	p := NewProcessor(uploadsDir)

	// Pass a non-existent source file - all variants should fail
	_, err := p.CreateAllVariants("/nonexistent/path.png", "bad-uuid", "bad.png")
	if err == nil {
		t.Error("CreateAllVariants should fail with non-existent source")
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
