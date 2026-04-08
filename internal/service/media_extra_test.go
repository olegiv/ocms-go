// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestGetURL(t *testing.T) {
	svc := NewMediaService(nil, "/uploads")

	media := store.Medium{
		Uuid:     "abc-123",
		Filename: "photo.jpg",
	}

	tests := []struct {
		name    string
		variant string
		want    string
	}{
		{"original empty string", "", "/uploads/originals/abc-123/photo.jpg"},
		{"original explicit", "original", "/uploads/originals/abc-123/photo.jpg"},
		{"thumbnail variant", "thumbnail", "/uploads/thumbnail/abc-123/photo.jpg"},
		{"grid variant", "grid", "/uploads/grid/abc-123/photo.jpg"},
		{"large variant", "large", "/uploads/large/abc-123/photo.jpg"},
		{"og variant", "og", "/uploads/og/abc-123/photo.jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.GetURL(media, tt.variant)
			if got != tt.want {
				t.Errorf("GetURL(%q) = %q, want %q", tt.variant, got, tt.want)
			}
		})
	}
}

func TestGetThumbnailURL(t *testing.T) {
	svc := NewMediaService(nil, "/uploads")

	tests := []struct {
		name     string
		mimeType string
		want     string
	}{
		{
			name:     "jpeg returns thumbnail URL",
			mimeType: model.MimeTypeJPEG,
			want:     "/uploads/thumbnail/test-uuid/image.jpg",
		},
		{
			name:     "png returns thumbnail URL",
			mimeType: model.MimeTypePNG,
			want:     "/uploads/thumbnail/test-uuid/image.jpg",
		},
		{
			name:     "gif returns thumbnail URL",
			mimeType: model.MimeTypeGIF,
			want:     "/uploads/thumbnail/test-uuid/image.jpg",
		},
		{
			name:     "webp returns thumbnail URL",
			mimeType: model.MimeTypeWebP,
			want:     "/uploads/thumbnail/test-uuid/image.jpg",
		},
		{
			name:     "pdf returns empty",
			mimeType: model.MimeTypePDF,
			want:     "",
		},
		{
			name:     "mp4 returns empty",
			mimeType: model.MimeTypeMP4,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media := store.Medium{
				Uuid:     "test-uuid",
				Filename: "image.jpg",
				MimeType: tt.mimeType,
			}
			got := svc.GetThumbnailURL(media)
			if got != tt.want {
				t.Errorf("GetThumbnailURL(%q) = %q, want %q", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestNormalizeDetectedMIME(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"image/jpeg", "image/jpeg"},
		{"image/png", "image/png"},
		{"IMAGE/JPEG", "image/jpeg"},
		{"image/jpeg; charset=utf-8", "image/jpeg"},
		{"  image/png  ", "image/png"},
		{"image/vnd.microsoft.icon", model.MimeTypeICO},
		{"image/x-icon", "image/x-icon"},
		{"text/html; charset=utf-8", "text/html"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeDetectedMIME(tt.input)
			if got != tt.want {
				t.Errorf("normalizeDetectedMIME(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetMimeTypeFromExtension_ICO(t *testing.T) {
	// .ico was not covered in existing tests
	got := getMimeTypeFromExtension("favicon.ico")
	if got != model.MimeTypeICO {
		t.Errorf("getMimeTypeFromExtension(\"favicon.ico\") = %q, want %q", got, model.MimeTypeICO)
	}
}

func TestNewMediaService_DefaultUploadDir(t *testing.T) {
	// When uploadDir is empty, DefaultUploadDir should be used
	svc := NewMediaService(nil, "")
	if svc.uploadDir != DefaultUploadDir {
		t.Errorf("uploadDir = %q, want %q", svc.uploadDir, DefaultUploadDir)
	}
}

func TestNewMediaService_CustomUploadDir(t *testing.T) {
	svc := NewMediaService(nil, "/custom/uploads")
	if svc.uploadDir != "/custom/uploads" {
		t.Errorf("uploadDir = %q, want /custom/uploads", svc.uploadDir)
	}
}
