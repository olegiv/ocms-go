package model

import (
	"testing"
)

func TestMediaIsImage(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{MimeTypeJPEG, true},
		{MimeTypePNG, true},
		{MimeTypeGIF, true},
		{MimeTypeWebP, true},
		{MimeTypePDF, false},
		{MimeTypeMP4, false},
		{MimeTypeWebM, false},
		{"text/plain", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			m := &Media{MimeType: tt.mimeType}
			if got := m.IsImage(); got != tt.want {
				t.Errorf("IsImage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMediaIsVideo(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{MimeTypeMP4, true},
		{MimeTypeWebM, true},
		{MimeTypeJPEG, false},
		{MimeTypePNG, false},
		{MimeTypePDF, false},
		{"video/avi", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			m := &Media{MimeType: tt.mimeType}
			if got := m.IsVideo(); got != tt.want {
				t.Errorf("IsVideo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMediaIsPDF(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{MimeTypePDF, true},
		{MimeTypeJPEG, false},
		{MimeTypeMP4, false},
		{"application/msword", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			m := &Media{MimeType: tt.mimeType}
			if got := m.IsPDF(); got != tt.want {
				t.Errorf("IsPDF() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSupportedImageTypes(t *testing.T) {
	types := SupportedImageTypes()
	expected := []string{MimeTypeJPEG, MimeTypePNG, MimeTypeGIF, MimeTypeWebP}

	if len(types) != len(expected) {
		t.Errorf("SupportedImageTypes() returned %d types, want %d", len(types), len(expected))
	}

	for i, typ := range types {
		if typ != expected[i] {
			t.Errorf("SupportedImageTypes()[%d] = %q, want %q", i, typ, expected[i])
		}
	}
}

func TestSupportedVideoTypes(t *testing.T) {
	types := SupportedVideoTypes()
	expected := []string{MimeTypeMP4, MimeTypeWebM}

	if len(types) != len(expected) {
		t.Errorf("SupportedVideoTypes() returned %d types, want %d", len(types), len(expected))
	}

	for i, typ := range types {
		if typ != expected[i] {
			t.Errorf("SupportedVideoTypes()[%d] = %q, want %q", i, typ, expected[i])
		}
	}
}

func TestSupportedDocumentTypes(t *testing.T) {
	types := SupportedDocumentTypes()
	expected := []string{MimeTypePDF}

	if len(types) != len(expected) {
		t.Errorf("SupportedDocumentTypes() returned %d types, want %d", len(types), len(expected))
	}

	for i, typ := range types {
		if typ != expected[i] {
			t.Errorf("SupportedDocumentTypes()[%d] = %q, want %q", i, typ, expected[i])
		}
	}
}

func TestAllSupportedTypes(t *testing.T) {
	types := AllSupportedTypes()

	// Should include all image, video, and document types
	expectedCount := len(SupportedImageTypes()) + len(SupportedVideoTypes()) + len(SupportedDocumentTypes())
	if len(types) != expectedCount {
		t.Errorf("AllSupportedTypes() returned %d types, want %d", len(types), expectedCount)
	}

	// Verify all expected types are present
	for _, expected := range []string{
		MimeTypeJPEG, MimeTypePNG, MimeTypeGIF, MimeTypeWebP,
		MimeTypeMP4, MimeTypeWebM,
		MimeTypePDF,
	} {
		found := false
		for _, typ := range types {
			if typ == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AllSupportedTypes() missing %q", expected)
		}
	}
}

func TestIsSupportedMimeType(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{MimeTypeJPEG, true},
		{MimeTypePNG, true},
		{MimeTypeGIF, true},
		{MimeTypeWebP, true},
		{MimeTypePDF, true},
		{MimeTypeMP4, true},
		{MimeTypeWebM, true},
		{"text/plain", false},
		{"application/octet-stream", false},
		{"image/bmp", false},
		{"video/avi", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := IsSupportedMimeType(tt.mimeType); got != tt.want {
				t.Errorf("IsSupportedMimeType(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestImageVariants(t *testing.T) {
	// Verify all expected variants exist
	variants := []string{VariantThumbnail, VariantMedium, VariantLarge}
	for _, v := range variants {
		config, ok := ImageVariants[v]
		if !ok {
			t.Errorf("ImageVariants missing %q variant", v)
			continue
		}
		if config.Width <= 0 {
			t.Errorf("ImageVariants[%q].Width = %d, want > 0", v, config.Width)
		}
		if config.Height <= 0 {
			t.Errorf("ImageVariants[%q].Height = %d, want > 0", v, config.Height)
		}
		if config.Quality <= 0 || config.Quality > 100 {
			t.Errorf("ImageVariants[%q].Quality = %d, want 1-100", v, config.Quality)
		}
	}

	// Verify thumbnail is cropped
	if !ImageVariants[VariantThumbnail].Crop {
		t.Error("ImageVariants[thumbnail].Crop should be true")
	}

	// Verify medium and large are not cropped
	if ImageVariants[VariantMedium].Crop {
		t.Error("ImageVariants[medium].Crop should be false")
	}
	if ImageVariants[VariantLarge].Crop {
		t.Error("ImageVariants[large].Crop should be false")
	}
}
