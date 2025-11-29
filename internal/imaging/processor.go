package imaging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/h2non/bimg"

	"ocms-go/internal/model"
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

// Processor handles image processing operations using libvips via bimg.
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

	// Create bimg image to get metadata
	img := bimg.NewImage(data)
	metadata, err := img.Metadata()
	if err != nil {
		return nil, fmt.Errorf("failed to read image metadata: %w", err)
	}

	// Auto-rotate based on EXIF orientation
	rotated, err := img.AutoRotate()
	if err != nil {
		// If auto-rotate fails, use original data
		rotated = data
	}

	// Strip sensitive EXIF data while preserving orientation
	processed, err := bimg.NewImage(rotated).Process(bimg.Options{
		StripMetadata: true,
	})
	if err != nil {
		// If processing fails, use rotated data
		processed = rotated
	}

	// Ensure originals directory exists
	originalsDir := filepath.Join(p.uploadDir, "originals", uuid)
	if err := os.MkdirAll(originalsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create originals directory: %w", err)
	}

	// Save the processed original
	filePath := filepath.Join(originalsDir, filename)
	if err := os.WriteFile(filePath, processed, 0644); err != nil {
		return nil, fmt.Errorf("failed to save original image: %w", err)
	}

	// Get final dimensions (may have changed due to rotation)
	finalImg := bimg.NewImage(processed)
	finalMeta, err := finalImg.Metadata()
	if err != nil {
		// Use original metadata if we can't get final
		finalMeta = metadata
	}

	return &ProcessResult{
		Width:    finalMeta.Size.Width,
		Height:   finalMeta.Size.Height,
		MimeType: bimgTypeToMimeType(finalMeta.Type),
		Size:     int64(len(processed)),
		FilePath: filePath,
	}, nil
}

// CreateVariant creates a resized variant of an image.
func (p *Processor) CreateVariant(sourcePath, uuid, filename string, config model.ImageVariantConfig, variantType string) (*VariantResult, error) {
	// Read source image
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source image: %w", err)
	}

	img := bimg.NewImage(data)
	metadata, err := img.Metadata()
	if err != nil {
		return nil, fmt.Errorf("failed to read image metadata: %w", err)
	}

	// Calculate dimensions
	var newWidth, newHeight int
	if config.Crop {
		// Crop to exact size
		newWidth = config.Width
		newHeight = config.Height
	} else {
		// Fit within bounds while maintaining aspect ratio
		newWidth, newHeight = calculateFitDimensions(
			metadata.Size.Width,
			metadata.Size.Height,
			config.Width,
			config.Height,
		)
	}

	// Skip if the source is smaller than the target
	if metadata.Size.Width <= config.Width && metadata.Size.Height <= config.Height && !config.Crop {
		return nil, nil // No need to create this variant
	}

	// Process options
	options := bimg.Options{
		Width:   newWidth,
		Height:  newHeight,
		Quality: config.Quality,
	}

	if config.Crop {
		options.Crop = true
		options.Gravity = bimg.GravityCentre
	}

	// Process the image
	processed, err := img.Process(options)
	if err != nil {
		return nil, fmt.Errorf("failed to process image variant: %w", err)
	}

	// Ensure variant directory exists
	variantDir := filepath.Join(p.uploadDir, variantType, uuid)
	if err := os.MkdirAll(variantDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create variant directory: %w", err)
	}

	// Save the variant
	variantPath := filepath.Join(variantDir, filename)
	if err := os.WriteFile(variantPath, processed, 0644); err != nil {
		return nil, fmt.Errorf("failed to save variant image: %w", err)
	}

	// Get final dimensions
	finalImg := bimg.NewImage(processed)
	finalMeta, err := finalImg.Metadata()
	if err != nil {
		finalMeta = bimg.ImageMetadata{
			Size: bimg.ImageSize{Width: newWidth, Height: newHeight},
		}
	}

	return &VariantResult{
		Type:     variantType,
		Width:    finalMeta.Size.Width,
		Height:   finalMeta.Size.Height,
		Size:     int64(len(processed)),
		FilePath: variantPath,
	}, nil
}

// CreateAllVariants creates all standard variants for an image.
func (p *Processor) CreateAllVariants(sourcePath, uuid, filename string) ([]*VariantResult, error) {
	var results []*VariantResult

	for variantType, config := range model.ImageVariants {
		result, err := p.CreateVariant(sourcePath, uuid, filename, config, variantType)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s variant: %w", variantType, err)
		}
		if result != nil {
			results = append(results, result)
		}
	}

	return results, nil
}

// GetImageDimensions returns the dimensions of an image file.
func (p *Processor) GetImageDimensions(path string) (width, height int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read image: %w", err)
	}

	img := bimg.NewImage(data)
	metadata, err := img.Metadata()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read metadata: %w", err)
	}

	return metadata.Size.Width, metadata.Size.Height, nil
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
	img := bimg.NewImage(data)
	metadata, err := img.Metadata()
	if err != nil {
		return ""
	}
	return bimgTypeToMimeType(metadata.Type)
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

// calculateFitDimensions calculates new dimensions to fit within bounds while maintaining aspect ratio.
func calculateFitDimensions(srcWidth, srcHeight, maxWidth, maxHeight int) (int, int) {
	if srcWidth <= maxWidth && srcHeight <= maxHeight {
		return srcWidth, srcHeight
	}

	ratio := float64(srcWidth) / float64(srcHeight)
	maxRatio := float64(maxWidth) / float64(maxHeight)

	var newWidth, newHeight int
	if ratio > maxRatio {
		// Width is the limiting factor
		newWidth = maxWidth
		newHeight = int(float64(maxWidth) / ratio)
	} else {
		// Height is the limiting factor
		newHeight = maxHeight
		newWidth = int(float64(maxHeight) * ratio)
	}

	return newWidth, newHeight
}

// bimgTypeToMimeType converts bimg image type to MIME type string.
func bimgTypeToMimeType(imgType string) string {
	switch strings.ToLower(imgType) {
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
