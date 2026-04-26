// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
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

func createPNGWithDimensions(width, height uint32) []byte {
	var out bytes.Buffer
	out.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10}) // signature

	ihdrData := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdrData[0:4], width)
	binary.BigEndian.PutUint32(ihdrData[4:8], height)
	ihdrData[8] = 8 // bit depth
	ihdrData[9] = 2 // color type RGB

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ihdrData)))
	out.Write(lenBuf[:])
	out.WriteString("IHDR")
	out.Write(ihdrData)

	ihdrCRC := crc32.NewIEEE()
	_, _ = ihdrCRC.Write([]byte("IHDR"))
	_, _ = ihdrCRC.Write(ihdrData)
	binary.BigEndian.PutUint32(lenBuf[:], ihdrCRC.Sum32())
	out.Write(lenBuf[:])

	// IEND chunk
	binary.BigEndian.PutUint32(lenBuf[:], 0)
	out.Write(lenBuf[:])
	out.WriteString("IEND")
	iendCRC := crc32.ChecksumIEEE([]byte("IEND"))
	binary.BigEndian.PutUint32(lenBuf[:], iendCRC)
	out.Write(lenBuf[:])

	return out.Bytes()
}

// runMimeTypeTests runs table-driven tests for mime type checking functions.
func runMimeTypeTests(t *testing.T, checkFn func(string) bool, tests []struct {
	mimeType string
	want     bool
},
) {
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

	// All 6 variants should be created for a 2400x1600 image (including og)
	if len(variants) != 6 {
		t.Errorf("variant count = %d, want 6", len(variants))
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

	// Create all variants - should only create cropped variants (thumbnail + grid)
	variants, err := p.CreateAllVariants(result.FilePath, uuid, "test-small.png")
	if err != nil {
		t.Fatalf("CreateAllVariants: %v", err)
	}

	// Thumbnail and grid should be created (both crop=true, so source size doesn't matter)
	if len(variants) != 2 {
		t.Errorf("variant count = %d, want 2 (thumbnail and grid for small source)", len(variants))
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

func TestProcessImage_RejectsOversizedDimensions(t *testing.T) {
	uploadsDir := t.TempDir()
	p := NewProcessor(uploadsDir)

	hugePNG := createPNGWithDimensions(40000, 40000)
	_, err := p.ProcessImage(bytes.NewReader(hugePNG), "test-uuid", "bomb.png")
	if err == nil {
		t.Fatal("ProcessImage should fail for oversized image dimensions")
	}
	if !strings.Contains(err.Error(), "exceed maximum allowed") {
		t.Fatalf("error = %v, want dimensions limit error", err)
	}
}

func TestCreateVariant_RejectsOversizedDimensions(t *testing.T) {
	uploadsDir := t.TempDir()
	p := NewProcessor(uploadsDir)

	hugePNG := createPNGWithDimensions(40000, 40000)
	sourcePath := uploadsDir + "/huge.png"
	if err := os.WriteFile(sourcePath, hugePNG, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := p.CreateVariant(sourcePath, "test-uuid", "huge.png", model.ImageVariantConfig{
		Width:   1200,
		Height:  800,
		Quality: 85,
		Crop:    false,
	}, "medium")
	if err == nil {
		t.Fatal("CreateVariant should fail for oversized image dimensions")
	}
	if !strings.Contains(err.Error(), "exceed maximum allowed") {
		t.Fatalf("error = %v, want dimensions limit error", err)
	}
}
