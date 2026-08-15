// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HugoSmits86/nativewebp"

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

func encodeTestWebP(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode WebP fixture: %v", err)
	}
	return buf.Bytes()
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

func TestProcessImageWebPEmitsDeclaredMime(t *testing.T) {
	const mediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	source := image.NewNRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			source.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 3),
				G: uint8(y * 4),
				B: 127,
				A: uint8(64 + (x+y)%192),
			})
		}
	}

	processor := NewProcessor(t.TempDir())
	result, err := processor.ProcessImage(
		bytes.NewReader(encodeTestWebP(t, source)),
		mediaUUID,
		"alpha.webp",
	)
	if err != nil {
		t.Fatalf("ProcessImage(WebP): %v", err)
	}
	if result.MimeType != model.MimeTypeWebP {
		t.Fatalf("MimeType = %q, want %q", result.MimeType, model.MimeTypeWebP)
	}
	if filepath.Ext(result.FilePath) != ".webp" {
		t.Fatalf("FilePath = %q, want .webp extension", result.FilePath)
	}

	stored, err := os.ReadFile(result.FilePath)
	if err != nil {
		t.Fatalf("ReadFile(processed WebP): %v", err)
	}
	if got := http.DetectContentType(stored); got != model.MimeTypeWebP {
		t.Fatalf("stored content type = %q, want %q", got, model.MimeTypeWebP)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("DecodeConfig(processed WebP): %v", err)
	}
	if format != "webp" || config.Width != 64 || config.Height != 48 {
		t.Fatalf("stored image = %s %dx%d, want webp 64x48", format, config.Width, config.Height)
	}
	decoded, format, err := image.Decode(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("Decode(processed WebP): %v", err)
	}
	if format != "webp" {
		t.Fatalf("decoded format = %q, want webp", format)
	}
	_, _, _, gotAlpha := decoded.At(0, 0).RGBA()
	_, _, _, wantAlpha := source.At(0, 0).RGBA()
	if gotAlpha != wantAlpha {
		t.Fatalf("stored alpha = %d, want %d", gotAlpha, wantAlpha)
	}

	width, height, mimeType, err := ValidateImage(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("ValidateImage(processed WebP): %v", err)
	}
	if width != 64 || height != 48 || mimeType != model.MimeTypeWebP {
		t.Fatalf("transfer validation = %dx%d %q, want 64x48 %q",
			width, height, mimeType, model.MimeTypeWebP)
	}
}

func TestCreateVariantWebPEmitsDeclaredMime(t *testing.T) {
	const mediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	processor := NewProcessor(t.TempDir())
	original, err := processor.ProcessImage(
		bytes.NewReader(encodeTestWebP(t, createTestImage(160, 120))),
		mediaUUID,
		"photo.webp",
	)
	if err != nil {
		t.Fatalf("ProcessImage(WebP): %v", err)
	}

	variant, err := processor.CreateVariant(
		original.FilePath,
		mediaUUID,
		"photo.webp",
		model.ImageVariantConfig{Width: 40, Height: 30, Quality: 80, Crop: true},
		model.VariantThumbnail,
	)
	if err != nil {
		t.Fatalf("CreateVariant(WebP): %v", err)
	}
	if variant == nil {
		t.Fatal("CreateVariant(WebP) returned nil")
	}
	if filepath.Ext(variant.FilePath) != ".webp" {
		t.Fatalf("FilePath = %q, want .webp extension", variant.FilePath)
	}

	stored, err := os.ReadFile(variant.FilePath)
	if err != nil {
		t.Fatalf("ReadFile(WebP variant): %v", err)
	}
	if got := http.DetectContentType(stored); got != model.MimeTypeWebP {
		t.Fatalf("variant content type = %q, want %q", got, model.MimeTypeWebP)
	}
	width, height, mimeType, err := ValidateImage(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("ValidateImage(WebP variant): %v", err)
	}
	if width != variant.Width || height != variant.Height || mimeType != model.MimeTypeWebP {
		t.Fatalf("transfer validation = %dx%d %q, want %dx%d %q",
			width, height, mimeType, variant.Width, variant.Height, model.MimeTypeWebP)
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

func TestValidateImageRejectsTruncatedPNGAfterConfigDecode(t *testing.T) {
	truncated := createPNGWithDimensions(1, 1)

	config, format, err := image.DecodeConfig(bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("fixture DecodeConfig: %v", err)
	}
	if format != "png" || config.Width != 1 || config.Height != 1 {
		t.Fatalf("fixture config = %s %dx%d, want png 1x1", format, config.Width, config.Height)
	}

	_, _, _, err = ValidateImage(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("ValidateImage should reject a PNG without image data")
	}
	if !strings.Contains(err.Error(), "failed to decode image") {
		t.Fatalf("error = %v, want full-decode error", err)
	}
}

func TestValidateImageRejectsOversizedIHDRBeforeFullDecode(t *testing.T) {
	oversized := createPNGWithDimensions(MaxImageWidth+1, 1)

	config, format, err := image.DecodeConfig(bytes.NewReader(oversized))
	if err != nil {
		t.Fatalf("fixture DecodeConfig: %v", err)
	}
	if format != "png" || config.Width != MaxImageWidth+1 || config.Height != 1 {
		t.Fatalf("fixture config = %s %dx%d, want png %dx1",
			format, config.Width, config.Height, MaxImageWidth+1)
	}

	_, _, _, err = ValidateImage(bytes.NewReader(oversized))
	if err == nil {
		t.Fatal("ValidateImage should reject an oversized PNG header")
	}
	if !strings.Contains(err.Error(), "dimensions exceed maximum allowed") {
		t.Fatalf("error = %v, want dimension-limit error before full decode", err)
	}
	if strings.Contains(err.Error(), "failed to decode image") {
		t.Fatalf("error = %v, oversized header reached full decode", err)
	}
}

// encodeOversizedPNG returns a real, decodable PNG that is over MaxImageWidth
// but cheap to decode: it exceeds the width cap without approaching the pixel
// cap, so the test stays fast.
func encodeOversizedPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, createTestImage(MaxImageWidth+1, 100)); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// TestProcessImageWithOptions_DownscaleOptIn pins the split between untrusted
// uploads and trusted migration files.
//
// Bug state: drop the DownscaleOversized branch from validateDecodable and the
// downscale case fails, which is how oversized Drupal images were silently lost
// during migration.
func TestProcessImageWithOptions_DownscaleOptIn(t *testing.T) {
	oversized := encodeOversizedPNG(t)

	t.Run("rejects without opt-in", func(t *testing.T) {
		p := NewProcessor(t.TempDir())
		_, err := p.ProcessImage(bytes.NewReader(oversized), "u", "wide.png")
		if err == nil {
			t.Fatal("ProcessImage should reject an oversized image by default")
		}
		if !strings.Contains(err.Error(), "exceed maximum allowed") {
			t.Fatalf("error = %v, want dimensions limit error", err)
		}
	})

	t.Run("downscales with opt-in", func(t *testing.T) {
		p := NewProcessor(t.TempDir())
		got, err := p.ProcessImageWithOptions(bytes.NewReader(oversized), "u", "wide.png",
			ProcessOptions{DownscaleOversized: true})
		if err != nil {
			t.Fatalf("ProcessImageWithOptions: %v", err)
		}
		if !got.Downscaled {
			t.Error("Downscaled = false, want true")
		}
		if got.Width > MaxImageWidth || got.Height > MaxImageHeight {
			t.Errorf("stored size %dx%d exceeds cap %dx%d",
				got.Width, got.Height, MaxImageWidth, MaxImageHeight)
		}
		if got.OriginalWidth != MaxImageWidth+1 {
			t.Errorf("OriginalWidth = %d, want %d", got.OriginalWidth, MaxImageWidth+1)
		}
	})
}

// TestFitBoundsSatisfiesEveryCap is the cheap, exact guard on the downscale
// geometry — building 50 MP fixtures to prove this through ProcessImage would
// cost seconds per case.
//
// Bug state: fit to maxImageWidth/maxImageHeight alone (the obvious
// imaging.Fit call) and the "over pixel cap only" rows fail. That is a real
// regression, not a hypothetical: eco-energy.jpg is 9422x6486, which sits
// inside 10000x10000 while being 61 MP, so a per-side-only fit stores it
// unchanged and still over the cap.
func TestFitBoundsSatisfiesEveryCap(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
	}{
		{"over pixel cap only", 9422, 6486}, // 61.1 MP, real: eco-energy.jpg
		{"over both caps", 11232, 7488},     // 84.1 MP, real: Graphic Bulldozer
		{"over width cap only", MaxImageWidth + 1, 100},
		{"extreme aspect ratio", 29000, 900},
		{"square over pixel cap", 9000, 9000}, // 81 MP, both sides legal
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h := fitBounds(tt.width, tt.height)
			if w > maxImageWidth || h > maxImageHeight {
				t.Errorf("fitBounds(%d,%d) = %dx%d, exceeds side cap %dx%d",
					tt.width, tt.height, w, h, maxImageWidth, maxImageHeight)
			}
			if got := int64(w) * int64(h); got > maxImagePixels {
				t.Errorf("fitBounds(%d,%d) = %dx%d = %d px, exceeds pixel cap %d",
					tt.width, tt.height, w, h, got, maxImagePixels)
			}
			if w < 1 || h < 1 {
				t.Errorf("fitBounds(%d,%d) = %dx%d, degenerate", tt.width, tt.height, w, h)
			}
			// Aspect ratio must survive the fit. The tolerance is relative and
			// generous because flooring is applied per side: on a 100 px side,
			// losing the fractional pixel is already a 1% shift, which is
			// inherent to integer bounds rather than a fault in the scaling.
			srcRatio := float64(tt.width) / float64(tt.height)
			gotRatio := float64(w) / float64(h)
			if drift := (gotRatio - srcRatio) / srcRatio; drift > 0.02 || drift < -0.02 {
				t.Errorf("aspect ratio drifted %.2f%%: %.4f -> %.4f",
					drift*100, srcRatio, gotRatio)
			}
		})
	}
}

// TestProcessImageWithOptions_HonorsDecodableCeiling checks that opting into
// downscaling does not disarm the decode-bomb guard: past maxDecodablePixels
// the file is refused however the caller asked.
func TestProcessImageWithOptions_HonorsDecodableCeiling(t *testing.T) {
	p := NewProcessor(t.TempDir())

	bomb := createPNGWithDimensions(40000, 40000) // 1.6e9 px, far above the ceiling
	_, err := p.ProcessImageWithOptions(bytes.NewReader(bomb), "u", "bomb.png",
		ProcessOptions{DownscaleOversized: true})
	if err == nil {
		t.Fatal("ProcessImageWithOptions should reject a decode bomb even when downscaling")
	}
	if !strings.Contains(err.Error(), "maximum decodable") {
		t.Fatalf("error = %v, want decodable ceiling error", err)
	}
}

// TestProcessImageKeepsUploadPathStrict guards the boundary the downscale work
// must not cross: the untrusted upload path calls ProcessImage, which must stay
// equivalent to the zero-value options.
func TestProcessImageKeepsUploadPathStrict(t *testing.T) {
	p := NewProcessor(t.TempDir())
	oversized := encodeOversizedPNG(t)

	_, viaPlain := p.ProcessImage(bytes.NewReader(oversized), "u", "wide.png")
	_, viaZeroOpts := p.ProcessImageWithOptions(bytes.NewReader(oversized), "u", "wide.png", ProcessOptions{})
	if (viaPlain == nil) != (viaZeroOpts == nil) {
		t.Fatalf("ProcessImage and zero-option ProcessImageWithOptions disagree: %v vs %v",
			viaPlain, viaZeroOpts)
	}
	if viaPlain == nil {
		t.Fatal("ProcessImage must keep rejecting oversized images")
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

func TestIsCanonicalMediaUUID(t *testing.T) {
	for _, valid := range []string{
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
	} {
		if !IsCanonicalMediaUUID(valid) {
			t.Errorf("IsCanonicalMediaUUID(%q) = false, want true", valid)
		}
	}
	for _, invalid := range []string{"", "abc123", "../../outside", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeeZ"} {
		if IsCanonicalMediaUUID(invalid) {
			t.Errorf("IsCanonicalMediaUUID(%q) = true, want false", invalid)
		}
	}
}

func TestDeleteMediaFilesRemovesEveryStorageDirectory(t *testing.T) {
	uploadDir := t.TempDir()
	const mediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, storageDir := range model.MediaStorageDirs() {
		dir := filepath.Join(uploadDir, storageDir, mediaUUID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", storageDir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file.bin"), []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", storageDir, err)
		}
	}

	if err := DeleteMediaFiles(uploadDir, mediaUUID); err != nil {
		t.Fatalf("DeleteMediaFiles() error = %v", err)
	}
	for _, storageDir := range model.MediaStorageDirs() {
		path := filepath.Join(uploadDir, storageDir, mediaUUID)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("media directory %q remains: %v", path, err)
		}
	}
}

func TestDeleteMediaFilesTreatsMissingUploadRootAsComplete(t *testing.T) {
	uploadDir := filepath.Join(t.TempDir(), "already-removed")
	if err := DeleteMediaFiles(uploadDir, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"); err != nil {
		t.Fatalf("DeleteMediaFiles(missing root) error = %v, want idempotent success", err)
	}
}

func TestDeleteMediaFilesAttemptsEveryDirectoryAndJoinsErrors(t *testing.T) {
	const mediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	firstErr := errors.New("first failure")
	secondErr := errors.New("second failure")
	var calls []string

	err := deleteMediaFilesWith(t.TempDir(), mediaUUID, func(_ *os.Root, path string) error {
		storageDir := filepath.Base(filepath.Dir(path))
		calls = append(calls, storageDir)
		switch len(calls) {
		case 1:
			return firstErr
		case 2:
			return secondErr
		default:
			return nil
		}
	})
	if len(calls) != len(model.MediaStorageDirs()) {
		t.Errorf("remove calls = %d, want %d; cleanup stopped after an error", len(calls), len(model.MediaStorageDirs()))
	}
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Errorf("DeleteMediaFiles() error = %v, want both failures joined", err)
	}
}

func TestDeleteMediaFilesRejectsMalformedUUIDBeforeRemoval(t *testing.T) {
	called := false
	err := deleteMediaFilesWith(t.TempDir(), "../outside", func(*os.Root, string) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Errorf("deleteMediaFilesWith() = (%v, called=%v), want rejection before removal", err, called)
	}
}

func TestDeleteMediaFilesRejectsFilesystemRootBeforeRemoval(t *testing.T) {
	called := false
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	err := deleteMediaFilesWith(root, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", func(*os.Root, string) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Errorf("deleteMediaFilesWith(filesystem root) = (%v, called=%v), want rejection before removal", err, called)
	}
}

func TestDeleteMediaFilesRejectsUploadSymlinkToFilesystemRoot(t *testing.T) {
	called := false
	uploadDir := filepath.Join(t.TempDir(), "uploads")
	filesystemRoot := filepath.VolumeName(uploadDir) + string(filepath.Separator)
	if err := os.Symlink(filesystemRoot, uploadDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := deleteMediaFilesWith(uploadDir, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", func(*os.Root, string) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Errorf("deleteMediaFilesWith(symlink to root) = (%v, called=%v), want rejection before removal", err, called)
	}
}

func TestOpenVerifiedUploadRootRejectsReplacementSymlink(t *testing.T) {
	parent := t.TempDir()
	uploadDir := filepath.Join(parent, "uploads")
	if err := os.Mkdir(uploadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	movedDir := filepath.Join(parent, "uploads-original")

	root, err := openVerifiedUploadRoot(uploadDir, func() {
		if renameErr := os.Rename(uploadDir, movedDir); renameErr != nil {
			t.Fatalf("rename upload root: %v", renameErr)
		}
		if symlinkErr := os.Symlink(outsideDir, uploadDir); symlinkErr != nil {
			t.Fatalf("replace upload root with symlink: %v", symlinkErr)
		}
	})
	if root != nil {
		_ = root.Close()
		t.Fatal("openVerifiedUploadRoot() returned a root after path replacement")
	}
	if err == nil {
		t.Fatal("openVerifiedUploadRoot() accepted a replacement symlink")
	}
}

func TestDeleteMediaFilesRejectsSymlinkedStorageDirectory(t *testing.T) {
	uploadDir := t.TempDir()
	outsideDir := t.TempDir()
	const mediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	outsideMediaDir := filepath.Join(outsideDir, mediaUUID)
	if err := os.Mkdir(outsideMediaDir, 0o755); err != nil {
		t.Fatalf("Mkdir outside media: %v", err)
	}
	outsideFile := filepath.Join(outsideMediaDir, "must-remain.bin")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(uploadDir, model.OriginalsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := DeleteMediaFiles(uploadDir, mediaUUID)
	if err == nil {
		t.Fatal("DeleteMediaFiles() accepted a storage-directory symlink outside uploads")
	}
	if data, readErr := os.ReadFile(outsideFile); readErr != nil || string(data) != "outside" {
		t.Fatalf("outside file was removed or changed: data=%q error=%v", data, readErr)
	}
}

func TestSaveImageFileRemovesPartialOutputAfterWriteFailure(t *testing.T) {
	uploadDir := t.TempDir()
	p := NewProcessor(uploadDir)
	const mediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	subDir := filepath.Join(model.OriginalsDir, mediaUUID)
	writeErr := errors.New("disk full")

	_, err := p.saveImageFileWith(subDir, "photo.png", []byte("complete"),
		func(root *os.Root, path string, _ []byte, mode os.FileMode) error {
			if err := root.WriteFile(path, []byte("partial"), mode); err != nil {
				return fmt.Errorf("write partial fixture: %w", err)
			}
			return writeErr
		}, (*os.Root).Close)
	if !errors.Is(err, writeErr) {
		t.Fatalf("saveImageFileWith() error = %v, want write failure", err)
	}
	targetDir := filepath.Join(uploadDir, subDir)
	if _, statErr := os.Stat(targetDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("partial image directory remains after write failure: %v", statErr)
	}
}

func TestSaveImageFileRemovesOutputAfterRootCloseFailure(t *testing.T) {
	uploadDir := t.TempDir()
	p := NewProcessor(uploadDir)
	const mediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	subDir := filepath.Join(model.OriginalsDir, mediaUUID)

	closeErr := errors.New("forced close failure")
	_, err := p.saveImageFileWith(subDir, "photo.png", []byte("complete"),
		func(root *os.Root, path string, data []byte, mode os.FileMode) error {
			return root.WriteFile(path, data, mode)
		}, func(root *os.Root) error {
			if err := root.Close(); err != nil {
				return err
			}
			return closeErr
		})
	if !errors.Is(err, closeErr) || !strings.Contains(err.Error(), "close uploads directory") {
		t.Fatalf("saveImageFileWith() error = %v, want close failure", err)
	}
	targetDir := filepath.Join(uploadDir, subDir)
	if _, statErr := os.Stat(targetDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("image directory remains after close failure: %v", statErr)
	}
}

func TestProcessImageRejectsSymlinkedStorageDirectory(t *testing.T) {
	uploadDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(uploadDir, model.OriginalsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, createTestImage(4, 4)); err != nil {
		t.Fatal(err)
	}
	const mediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	_, err := NewProcessor(uploadDir).ProcessImage(bytes.NewReader(encoded.Bytes()), mediaUUID, "photo.png")
	if err == nil {
		t.Fatal("ProcessImage accepted a storage-directory symlink outside uploads")
	}
	outsideTarget := filepath.Join(outsideDir, mediaUUID)
	if _, statErr := os.Stat(outsideTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("image output escaped uploads into %s: %v", outsideTarget, statErr)
	}
}
