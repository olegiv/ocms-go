// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"database/sql"
	"net/url"
	"sort"
	"time"
)

// Supported image variant types
const (
	VariantThumbnail = "thumbnail"
	VariantGrid      = "grid"
	VariantSmall     = "small"
	VariantMedium    = "medium"
	VariantLarge     = "large"
	VariantOG        = "og"

	// VariantOriginal addresses the unresized upload. Its files live under
	// "originals", not "original", so it is not simply the variant name.
	VariantOriginal = "original"
)

// MediaURL builds the public URL for a stored media file.
//
// The filename is percent-encoded, which is not cosmetic. Uploaded filenames
// routinely contain spaces and non-ASCII characters, and html/template escapes
// a URL differently depending on the attribute it lands in: in src it encodes
// the spaces for you, but in srcset a space ends the URL candidate, so the
// whole entry is replaced with "#ZgotmplZ" and the browser — which prefers
// srcset over src — renders a broken image. Encoding here means every caller
// gets a URL that is safe in both.
func MediaURL(variant, uuid, filename string) string {
	dir := variant
	if variant == "" || variant == VariantOriginal {
		dir = OriginalsDir
	}
	return "/uploads/" + dir + "/" + uuid + "/" + url.PathEscape(filename)
}

// Minimum dimensions for featured images
const (
	MinFeaturedImageWidth  = 1200
	MinFeaturedImageHeight = 800
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
	VariantGrid:      {Width: 256, Height: 256, Quality: 85, Crop: true},
	VariantSmall:     {Width: 400, Height: 300, Quality: 85, Crop: false},
	VariantMedium:    {Width: 800, Height: 600, Quality: 85, Crop: false},
	VariantLarge:     {Width: 1920, Height: 1080, Quality: 90, Crop: false},
	VariantOG:        {Width: 1200, Height: 630, Quality: 85, Crop: false},
}

// OriginalsDir is the directory holding unresized uploads. It is "originals",
// not VariantOriginal ("original"), which is why it cannot be derived from the
// variant name — see MediaURL, which special-cases exactly this.
const OriginalsDir = "originals"

// MediaStorageDirs returns every directory under the uploads root that can hold
// files for one media UUID: the originals plus one per image variant.
//
// It exists because three separate cleanup paths each kept their own hardcoded
// copy of this list, and the migrator's had drifted — it omitted "og", so every
// imported image left /uploads/og/<uuid> behind after "delete imported content"
// with no database or tracking row left to find it from. Deleting a media item
// must remove everything creating one can produce, so both sides derive from
// ImageVariants.
func MediaStorageDirs() []string {
	dirs := make([]string, 0, len(ImageVariants)+1)
	dirs = append(dirs, OriginalsDir)
	for variant := range ImageVariants {
		dirs = append(dirs, variant)
	}
	sort.Strings(dirs)
	return dirs
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
