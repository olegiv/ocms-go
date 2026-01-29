// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// Upload limits
const (
	MaxUploadSize    = 20 * 1024 * 1024 // 20MB
	DefaultUploadDir = "./uploads"
)

// AllowedMimeTypes defines the MIME types that can be uploaded.
var AllowedMimeTypes = map[string]bool{
	model.MimeTypeJPEG: true,
	model.MimeTypePNG:  true,
	model.MimeTypeGIF:  true,
	model.MimeTypeWebP: true,
	model.MimeTypePDF:  true,
	model.MimeTypeMP4:  true,
	model.MimeTypeWebM: true,
}

// UploadResult contains the result of a media upload.
type UploadResult struct {
	Media    store.Medium
	Variants []store.MediaVariant
}

// MediaService handles media file operations.
type MediaService struct {
	db        *sql.DB
	processor *imaging.Processor
	uploadDir string
}

// NewMediaService creates a new media service.
func NewMediaService(db *sql.DB, uploadDir string) *MediaService {
	if uploadDir == "" {
		uploadDir = DefaultUploadDir
	}
	return &MediaService{
		db:        db,
		processor: imaging.NewProcessor(uploadDir),
		uploadDir: uploadDir,
	}
}

// Upload handles file upload, processing, and database storage.
func (s *MediaService) Upload(ctx context.Context, file multipart.File, header *multipart.FileHeader, userID int64, folderID *int64, languageCode string) (*UploadResult, error) {
	// Validate file size
	if header.Size > MaxUploadSize {
		return nil, fmt.Errorf("file size exceeds maximum allowed (%d bytes)", MaxUploadSize)
	}

	// Detect and validate MIME type
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		// Try to detect from extension
		mimeType = getMimeTypeFromExtension(header.Filename)
	}
	if !AllowedMimeTypes[mimeType] {
		return nil, fmt.Errorf("file type %s is not allowed", mimeType)
	}

	// Generate UUID for the file
	fileUUID := uuid.New().String()

	// Sanitize filename
	filename := sanitizeFilename(header.Filename)

	queries := store.New(s.db)
	now := time.Now()

	var result UploadResult

	// Check if it's an image
	if s.processor.IsImage(mimeType) {
		// Process image
		processResult, err := s.processor.ProcessImage(file, fileUUID, filename)
		if err != nil {
			return nil, fmt.Errorf("failed to process image: %w", err)
		}

		// Create media record
		media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
			Uuid:         fileUUID,
			Filename:     filename,
			MimeType:     processResult.MimeType,
			Size:         processResult.Size,
			Width:        sql.NullInt64{Int64: int64(processResult.Width), Valid: true},
			Height:       sql.NullInt64{Int64: int64(processResult.Height), Valid: true},
			Alt:          sql.NullString{String: "", Valid: true},
			Caption:      sql.NullString{String: "", Valid: true},
			FolderID:     util.NullInt64FromPtr(folderID),
			UploadedBy:   userID,
			LanguageCode: languageCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			// Clean up uploaded files on error
			_ = s.processor.DeleteMediaFiles(fileUUID)
			return nil, fmt.Errorf("failed to create media record: %w", err)
		}

		result.Media = media

		// Create variants
		variants, err := s.processor.CreateAllVariants(processResult.FilePath, fileUUID, filename)
		if err != nil {
			// Log error but don't fail - we still have the original
			fmt.Printf("Warning: failed to create some variants: %v\n", err)
		}

		// Store variant records
		for _, v := range variants {
			variant, err := queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
				MediaID:   media.ID,
				Type:      v.Type,
				Width:     int64(v.Width),
				Height:    int64(v.Height),
				Size:      v.Size,
				CreatedAt: now,
			})
			if err != nil {
				fmt.Printf("Warning: failed to store variant record: %v\n", err)
				continue
			}
			result.Variants = append(result.Variants, variant)
		}
	} else {
		// Non-image file - just save it
		filePath, size, err := s.saveNonImageFile(file, fileUUID, filename)
		if err != nil {
			return nil, fmt.Errorf("failed to save file: %w", err)
		}

		media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
			Uuid:         fileUUID,
			Filename:     filename,
			MimeType:     mimeType,
			Size:         size,
			Width:        sql.NullInt64{Valid: false},
			Height:       sql.NullInt64{Valid: false},
			Alt:          sql.NullString{String: "", Valid: true},
			Caption:      sql.NullString{String: "", Valid: true},
			FolderID:     util.NullInt64FromPtr(folderID),
			UploadedBy:   userID,
			LanguageCode: languageCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			// Clean up on error
			_ = os.Remove(filePath)
			return nil, fmt.Errorf("failed to create media record: %w", err)
		}

		result.Media = media
	}

	return &result, nil
}

// Delete removes a media item and its files.
func (s *MediaService) Delete(ctx context.Context, mediaID int64) error {
	queries := store.New(s.db)

	// Get media record first
	media, err := queries.GetMediaByID(ctx, mediaID)
	if err != nil {
		return fmt.Errorf("failed to get media: %w", err)
	}

	// Delete variants from DB
	if err := queries.DeleteMediaVariants(ctx, mediaID); err != nil {
		return fmt.Errorf("failed to delete variant records: %w", err)
	}

	// Delete media record from DB
	if err := queries.DeleteMedia(ctx, mediaID); err != nil {
		return fmt.Errorf("failed to delete media record: %w", err)
	}

	// Delete files from disk
	if err := s.processor.DeleteMediaFiles(media.Uuid); err != nil {
		// Log but don't fail - DB records are already deleted
		fmt.Printf("Warning: failed to delete files for media %d: %v\n", mediaID, err)
	}

	return nil
}

// GetURL returns the URL path for a media item.
func (s *MediaService) GetURL(media store.Medium, variant string) string {
	if variant == "" || variant == "original" {
		return fmt.Sprintf("/uploads/originals/%s/%s", media.Uuid, media.Filename)
	}
	return fmt.Sprintf("/uploads/%s/%s/%s", variant, media.Uuid, media.Filename)
}

// GetThumbnailURL returns the thumbnail URL for a media item.
func (s *MediaService) GetThumbnailURL(media store.Medium) string {
	if !isImageMimeType(media.MimeType) {
		return "" // No thumbnail for non-images
	}
	return s.GetURL(media, model.VariantThumbnail)
}

// saveNonImageFile saves a non-image file to the uploads directory.
func (s *MediaService) saveNonImageFile(file io.Reader, uuid, filename string) (string, int64, error) {
	// Create directory
	dir := filepath.Join(s.uploadDir, "originals", uuid)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", 0, fmt.Errorf("failed to create directory: %w", err)
	}

	// Create file
	filePath := filepath.Join(dir, filename)
	out, err := os.Create(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create file: %w", err)
	}
	defer func() { _ = out.Close() }()

	// Copy data
	size, err := io.Copy(out, file)
	if err != nil {
		_ = os.Remove(filePath)
		return "", 0, fmt.Errorf("failed to write file: %w", err)
	}

	return filePath, size, nil
}

// Helper functions

func sanitizeFilename(filename string) string {
	// Remove path separators
	filename = filepath.Base(filename)

	// Replace problematic characters
	replacer := strings.NewReplacer(
		" ", "-",
		"'", "",
		"\"", "",
		"<", "",
		">", "",
		"&", "",
		"#", "",
		"?", "",
		"%", "",
	)
	filename = replacer.Replace(filename)

	// Ensure we have an extension
	if filepath.Ext(filename) == "" {
		filename += ".bin"
	}

	return filename
}

func getMimeTypeFromExtension(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return model.MimeTypeJPEG
	case ".png":
		return model.MimeTypePNG
	case ".gif":
		return model.MimeTypeGIF
	case ".webp":
		return model.MimeTypeWebP
	case ".pdf":
		return model.MimeTypePDF
	case ".mp4":
		return model.MimeTypeMP4
	case ".webm":
		return model.MimeTypeWebM
	default:
		return "application/octet-stream"
	}
}

func isImageMimeType(mimeType string) bool {
	switch mimeType {
	case model.MimeTypeJPEG, model.MimeTypePNG, model.MimeTypeGIF, model.MimeTypeWebP:
		return true
	default:
		return false
	}
}
