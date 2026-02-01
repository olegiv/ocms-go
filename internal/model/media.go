// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"database/sql"
	"time"
)

// Supported image variant types
const (
	VariantThumbnail = "thumbnail"
	VariantMedium    = "medium"
	VariantLarge     = "large"
)

// Supported MIME types
const (
	MimeTypeJPEG = "image/jpeg"
	MimeTypePNG  = "image/png"
	MimeTypeGIF  = "image/gif"
	MimeTypeWebP = "image/webp"
	MimeTypeICO  = "image/x-icon"
	MimeTypePDF  = "application/pdf"
	MimeTypeMP4  = "video/mp4"
	MimeTypeWebM = "video/webm"
)

// ImageVariantConfig defines settings for generating image variants.
type ImageVariantConfig struct {
	Width   int
	Height  int
	Quality int
	Crop    bool // true = crop to exact size, false = fit within bounds
}

// ImageVariants defines the default image variant configurations.
var ImageVariants = map[string]ImageVariantConfig{
	VariantThumbnail: {Width: 150, Height: 150, Quality: 80, Crop: true},
	VariantMedium:    {Width: 800, Height: 600, Quality: 85, Crop: false},
	VariantLarge:     {Width: 1920, Height: 1080, Quality: 90, Crop: false},
}

// Media represents an uploaded file in the media library.
type Media struct {
	ID         int64
	UUID       string
	Filename   string
	MimeType   string
	Size       int64
	Width      sql.NullInt64
	Height     sql.NullInt64
	Alt        string
	Caption    string
	FolderID   sql.NullInt64
	UploadedBy int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// MediaFolder represents a folder in the media library.
type MediaFolder struct {
	ID        int64
	Name      string
	ParentID  sql.NullInt64
	Position  int64
	CreatedAt time.Time
}

// MediaVariant represents a generated variant of an image.
type MediaVariant struct {
	ID        int64
	MediaID   int64
	Type      string
	Width     int64
	Height    int64
	Size      int64
	CreatedAt time.Time
}

// IsImage returns true if the media type is an image.
func (m *Media) IsImage() bool {
	switch m.MimeType {
	case MimeTypeJPEG, MimeTypePNG, MimeTypeGIF, MimeTypeWebP:
		return true
	default:
		return false
	}
}

// IsVideo returns true if the media type is a video.
func (m *Media) IsVideo() bool {
	switch m.MimeType {
	case MimeTypeMP4, MimeTypeWebM:
		return true
	default:
		return false
	}
}

// IsPDF returns true if the media type is a PDF document.
func (m *Media) IsPDF() bool {
	return m.MimeType == MimeTypePDF
}

// SupportedImageTypes returns a list of supported image MIME types.
func SupportedImageTypes() []string {
	return []string{MimeTypeJPEG, MimeTypePNG, MimeTypeGIF, MimeTypeWebP}
}

// SupportedVideoTypes returns a list of supported video MIME types.
func SupportedVideoTypes() []string {
	return []string{MimeTypeMP4, MimeTypeWebM}
}

// SupportedDocumentTypes returns a list of supported document MIME types.
func SupportedDocumentTypes() []string {
	return []string{MimeTypePDF}
}

// AllSupportedTypes returns all supported MIME types.
func AllSupportedTypes() []string {
	types := make([]string, 0)
	types = append(types, SupportedImageTypes()...)
	types = append(types, SupportedVideoTypes()...)
	types = append(types, SupportedDocumentTypes()...)
	return types
}

// IsSupportedMimeType checks if a MIME type is supported.
func IsSupportedMimeType(mimeType string) bool {
	for _, t := range AllSupportedTypes() {
		if t == mimeType {
			return true
		}
	}
	return false
}
