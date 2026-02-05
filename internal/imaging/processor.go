// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/rwcarlsen/goexif/exif"
	_ "golang.org/x/image/webp" // WebP decoder

	"github.com/olegiv/ocms-go/internal/model"
)

// ProcessResult contains the result of processing an uploaded image.
type ProcessResult struct {
	Width    int
	Height   int
	MimeType string
	Size     int64
	FilePath string
}

// VariantResult contains the result of creating an image variant.
type VariantResult struct {
	Type     string
	Width    int
	Height   int
	Size     int64
	FilePath string
}

// Processor handles image processing operations using pure Go libraries.
type Processor struct {
	uploadDir string
}

// NewProcessor creates a new image processor.
func NewProcessor(uploadDir string) *Processor {
	return &Processor{
		uploadDir: uploadDir,
	}
}

// ProcessImage reads an uploaded image file and returns its metadata.
// It saves the original file and returns processing results.
func (p *Processor) ProcessImage(reader io.Reader, uuid, filename string) (*ProcessResult, error) {
	// Read all data from reader
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data: %w", err)
	}

	// Detect format
	format := detectFormat(data)
	if format == "" {
		return nil, fmt.Errorf("unsupported image format")
	}

	// Decode image
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Read EXIF orientation and auto-rotate
	orientation := readExifOrientation(bytes.NewReader(data))
	img = applyOrientation(img, orientation)

	// Get final dimensions
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Encode without EXIF (pure Go encoders don't preserve EXIF metadata)
	processed, err := encodeImage(img, format, 95)
	if err != nil {
		return nil, fmt.Errorf("failed to encode image: %w", err)
	}

	// Save the processed original
	subDir := filepath.Join("originals", uuid)
	filePath, err := p.saveImageFile(subDir, filename, processed)
	if err != nil {
		return nil, fmt.Errorf("failed to save original image: %w", err)
	}

	return &ProcessResult{
		Width:    width,
		Height:   height,
		MimeType: formatToMimeType(format),
		Size:     int64(len(processed)),
		FilePath: filePath,
	}, nil
}

// CreateVariant creates a resized variant of an image.
func (p *Processor) CreateVariant(sourcePath, uuid, filename string, config model.ImageVariantConfig, variantType string) (*VariantResult, error) {
	// Load source image
	img, err := imaging.Open(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open source image: %w", err)
	}

	// Get source dimensions
	bounds := img.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()

	// Skip if the source is smaller than the target (and not cropping)
	if srcWidth <= config.Width && srcHeight <= config.Height && !config.Crop {
		return nil, nil // No need to create this variant
	}

	// Process based on mode
	var resized image.Image
	if config.Crop {
		// Crop to exact size from center
		resized = imaging.Fill(img, config.Width, config.Height, imaging.Center, imaging.Lanczos)
	} else {
		// Fit within bounds while maintaining aspect ratio
		resized = imaging.Fit(img, config.Width, config.Height, imaging.Lanczos)
	}

	// Get final dimensions
	resBounds := resized.Bounds()
	newWidth := resBounds.Dx()
	newHeight := resBounds.Dy()

	// Determine output format from filename
	format := detectFormatFromFilename(filename)

	// Encode with quality
	processed, err := encodeImage(resized, format, config.Quality)
	if err != nil {
		return nil, fmt.Errorf("failed to encode variant: %w", err)
	}

	// Save the variant
	variantSubDir := filepath.Join(variantType, uuid)
	variantPath, err := p.saveImageFile(variantSubDir, filename, processed)
	if err != nil {
		return nil, fmt.Errorf("failed to save %s variant: %w", variantType, err)
	}

	return &VariantResult{
		Type:     variantType,
		Width:    newWidth,
		Height:   newHeight,
		Size:     int64(len(processed)),
		FilePath: variantPath,
	}, nil
}

// CreateAllVariants creates all standard variants for an image.
// It continues processing even if individual variants fail, returning
// all successfully created variants along with any errors encountered.
func (p *Processor) CreateAllVariants(sourcePath, uuid, filename string) ([]*VariantResult, error) {
	var results []*VariantResult
	var errs []string

	for variantType, config := range model.ImageVariants {
		result, err := p.CreateVariant(sourcePath, uuid, filename, config, variantType)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", variantType, err))
			continue // Continue with other variants
		}
		if result != nil {
			results = append(results, result)
		}
	}

	// Return results even if some variants failed
	if len(errs) > 0 && len(results) == 0 {
		return nil, fmt.Errorf("all variants failed: %s", strings.Join(errs, "; "))
	}

	return results, nil
}

// GetImageDimensions returns the dimensions of an image file.
func (p *Processor) GetImageDimensions(path string) (width, height int, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to open image: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Decode config only for efficiency (doesn't decode full image)
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read image config: %w", err)
	}

	return config.Width, config.Height, nil
}

// IsImage checks if a MIME type represents an image that can be processed.
func (p *Processor) IsImage(mimeType string) bool {
	switch mimeType {
	case model.MimeTypeJPEG, model.MimeTypePNG, model.MimeTypeGIF, model.MimeTypeWebP:
		return true
	default:
		return false
	}
}

// IsSupportedType checks if a MIME type is supported for upload.
func (p *Processor) IsSupportedType(mimeType string) bool {
	return model.IsSupportedMimeType(mimeType)
}

// DetectMimeType detects the MIME type of image data.
func (p *Processor) DetectMimeType(data []byte) string {
	contentType := http.DetectContentType(data)
	// http.DetectContentType returns types like "image/jpeg; charset=utf-8"
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = contentType[:idx]
	}
	return contentType
}

// DeleteMediaFiles removes all files associated with a media item.
func (p *Processor) DeleteMediaFiles(uuid string) error {
	// Delete original
	originalsDir := filepath.Join(p.uploadDir, "originals", uuid)
	if err := os.RemoveAll(originalsDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete originals: %w", err)
	}

	// Delete all variants
	for variantType := range model.ImageVariants {
		variantDir := filepath.Join(p.uploadDir, variantType, uuid)
		if err := os.RemoveAll(variantDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete %s variant: %w", variantType, err)
		}
	}

	return nil
}

// readExifOrientation reads the EXIF orientation tag from image data.
// Returns 1 (normal) if orientation cannot be determined.
func readExifOrientation(r io.Reader) int {
	x, err := exif.Decode(r)
	if err != nil {
		return 1
	}

	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 1
	}

	orientation, err := tag.Int(0)
	if err != nil {
		return 1
	}

	return orientation
}

// applyOrientation applies EXIF orientation transformation to an image.
// Orientation values:
// 1: Normal
// 2: Flip horizontal
// 3: Rotate 180°
// 4: Flip vertical
// 5: Rotate 90° CW + flip horizontal
// 6: Rotate 90° CW
// 7: Rotate 90° CCW + flip horizontal
// 8: Rotate 90° CCW
func applyOrientation(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return imaging.FlipH(img)
	case 3:
		return imaging.Rotate180(img)
	case 4:
		return imaging.FlipV(img)
	case 5:
		return imaging.FlipH(imaging.Rotate270(img))
	case 6:
		return imaging.Rotate270(img)
	case 7:
		return imaging.FlipH(imaging.Rotate90(img))
	case 8:
		return imaging.Rotate90(img)
	default:
		return img
	}
}

// encodeImage encodes an image to bytes with the specified format and quality.
func encodeImage(img image.Image, format string, quality int) ([]byte, error) {
	var buf bytes.Buffer

	switch format {
	case "jpeg", "jpg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	case "gif":
		if err := gif.Encode(&buf, img, nil); err != nil {
			return nil, err
		}
	case "webp":
		// WebP decoding is supported but encoding is not in pure Go
		// Convert to JPEG for output
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	default:
		// Default to JPEG
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

// detectFormat detects the image format from raw bytes.
func detectFormat(data []byte) string {
	contentType := http.DetectContentType(data)
	// Explicitly reject TIFF (CVE-2023-36308 in disintegration/imaging)
	if strings.Contains(contentType, "tiff") {
		return ""
	}
	switch {
	case strings.Contains(contentType, "jpeg"):
		return "jpeg"
	case strings.Contains(contentType, "png"):
		return "png"
	case strings.Contains(contentType, "gif"):
		return "gif"
	case strings.Contains(contentType, "webp"):
		return "webp"
	default:
		return ""
	}
}

// detectFormatFromFilename extracts format from filename extension.
func detectFormatFromFilename(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".png":
		return "png"
	case ".gif":
		return "gif"
	case ".webp":
		return "webp"
	default:
		return "jpeg"
	}
}

// formatToMimeType converts format string to MIME type.
func formatToMimeType(format string) string {
	switch format {
	case "jpeg", "jpg":
		return model.MimeTypeJPEG
	case "png":
		return model.MimeTypePNG
	case "gif":
		return model.MimeTypeGIF
	case "webp":
		return model.MimeTypeWebP
	default:
		return "application/octet-stream"
	}
}

// saveImageFile creates the directory if needed and saves image data to a file.
// The filename is sanitized and the target directory is validated to be within uploadDir.
func (p *Processor) saveImageFile(subDir, filename string, data []byte) (string, error) {
	// Sanitize filename to prevent path traversal
	safeFilename := filepath.Base(filename)
	if safeFilename == "." || safeFilename == ".." || safeFilename == "" {
		return "", fmt.Errorf("invalid filename")
	}

	// Validate subDir doesn't contain path traversal sequences
	cleanSubDir := filepath.Clean(subDir)
	if strings.Contains(cleanSubDir, "..") || filepath.IsAbs(cleanSubDir) {
		return "", fmt.Errorf("invalid subdirectory path")
	}

	// Resolve base directory to absolute path
	absBase, err := filepath.Abs(p.uploadDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}

	// Build target path using validated subDir
	absTarget := filepath.Join(absBase, cleanSubDir)

	// Verify containment using filepath.Rel
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path traversal detected")
	}

	if err := os.MkdirAll(absTarget, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	filePath := filepath.Join(absTarget, safeFilename)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to save image: %w", err)
	}
	return filePath, nil
}
