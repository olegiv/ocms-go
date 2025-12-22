package api

import (
	"testing"
	"time"

	"ocms-go/internal/handler"
	"ocms-go/internal/store"
)

func TestParseMediaIncludes(t *testing.T) {
	tests := []struct {
		name         string
		include      string
		wantVariants bool
		wantFolder   bool
	}{
		{
			name:         "empty string",
			include:      "",
			wantVariants: false,
			wantFolder:   false,
		},
		{
			name:         "variants only",
			include:      "variants",
			wantVariants: true,
			wantFolder:   false,
		},
		{
			name:         "folder only",
			include:      "folder",
			wantVariants: false,
			wantFolder:   true,
		},
		{
			name:         "both variants and folder",
			include:      "variants,folder",
			wantVariants: true,
			wantFolder:   true,
		},
		{
			name:         "with spaces",
			include:      "variants, folder",
			wantVariants: true,
			wantFolder:   true,
		},
		{
			name:         "reversed order",
			include:      "folder,variants",
			wantVariants: true,
			wantFolder:   true,
		},
		{
			name:         "unknown include ignored",
			include:      "variants,unknown,folder",
			wantVariants: true,
			wantFolder:   true,
		},
		{
			name:         "only unknown",
			include:      "unknown",
			wantVariants: false,
			wantFolder:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVariants, gotFolder := parseMediaIncludes(tt.include)

			if gotVariants != tt.wantVariants {
				t.Errorf("variants = %v, want %v", gotVariants, tt.wantVariants)
			}
			if gotFolder != tt.wantFolder {
				t.Errorf("folder = %v, want %v", gotFolder, tt.wantFolder)
			}
		})
	}
}

func TestIsImageMime(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"image/gif", true},
		{"image/webp", true},
		{"image/svg+xml", true},
		{"application/pdf", false},
		{"video/mp4", false},
		{"text/plain", false},
		{"application/json", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			got := handler.IsImageMime(tt.mimeType)
			if got != tt.want {
				t.Errorf("handler.IsImageMime(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestStoreVariantToResponse(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		variant  store.MediaVariant
		uuid     string
		filename string
		wantURL  string
	}{
		{
			name: "thumbnail variant",
			variant: store.MediaVariant{
				ID:        1,
				Type:      "thumbnail",
				Width:     150,
				Height:    150,
				Size:      5000,
				CreatedAt: now,
			},
			uuid:     "abc-123",
			filename: "image.jpg",
			wantURL:  "/uploads/thumbnail/abc-123/image.jpg",
		},
		{
			name: "medium variant",
			variant: store.MediaVariant{
				ID:        2,
				Type:      "medium",
				Width:     800,
				Height:    600,
				Size:      50000,
				CreatedAt: now,
			},
			uuid:     "def-456",
			filename: "photo.png",
			wantURL:  "/uploads/medium/def-456/photo.png",
		},
		{
			name: "large variant",
			variant: store.MediaVariant{
				ID:        3,
				Type:      "large",
				Width:     1920,
				Height:    1080,
				Size:      200000,
				CreatedAt: now,
			},
			uuid:     "ghi-789",
			filename: "banner.webp",
			wantURL:  "/uploads/large/ghi-789/banner.webp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storeVariantToResponse(tt.variant, tt.uuid, tt.filename)

			if got.ID != tt.variant.ID {
				t.Errorf("ID = %d, want %d", got.ID, tt.variant.ID)
			}
			if got.Type != tt.variant.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.variant.Type)
			}
			if got.Width != tt.variant.Width {
				t.Errorf("Width = %d, want %d", got.Width, tt.variant.Width)
			}
			if got.Height != tt.variant.Height {
				t.Errorf("Height = %d, want %d", got.Height, tt.variant.Height)
			}
			if got.Size != tt.variant.Size {
				t.Errorf("Size = %d, want %d", got.Size, tt.variant.Size)
			}
			if got.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", got.URL, tt.wantURL)
			}
			if !got.CreatedAt.Equal(tt.variant.CreatedAt) {
				t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, tt.variant.CreatedAt)
			}
		})
	}
}

func TestPopulateMediaVariants(t *testing.T) {
	now := time.Now()

	t.Run("empty variants", func(t *testing.T) {
		resp := &MediaResponse{
			UUID:     "abc-123",
			Filename: "image.jpg",
		}

		populateMediaVariants(resp, []store.MediaVariant{})

		if resp.Variants != nil {
			t.Errorf("Variants should be nil for empty input, got %v", resp.Variants)
		}
	})

	t.Run("nil variants", func(t *testing.T) {
		resp := &MediaResponse{
			UUID:     "abc-123",
			Filename: "image.jpg",
		}

		populateMediaVariants(resp, nil)

		if resp.Variants != nil {
			t.Errorf("Variants should be nil for nil input, got %v", resp.Variants)
		}
	})

	t.Run("multiple variants", func(t *testing.T) {
		resp := &MediaResponse{
			UUID:     "abc-123",
			Filename: "image.jpg",
		}

		variants := []store.MediaVariant{
			{ID: 1, Type: "thumbnail", Width: 150, Height: 150, Size: 5000, CreatedAt: now},
			{ID: 2, Type: "medium", Width: 800, Height: 600, Size: 50000, CreatedAt: now},
			{ID: 3, Type: "large", Width: 1920, Height: 1080, Size: 200000, CreatedAt: now},
		}

		populateMediaVariants(resp, variants)

		if len(resp.Variants) != 3 {
			t.Fatalf("expected 3 variants, got %d", len(resp.Variants))
		}

		// Verify first variant
		if resp.Variants[0].Type != "thumbnail" {
			t.Errorf("first variant type = %q, want %q", resp.Variants[0].Type, "thumbnail")
		}
		if resp.Variants[0].URL != "/uploads/thumbnail/abc-123/image.jpg" {
			t.Errorf("first variant URL = %q, want %q", resp.Variants[0].URL, "/uploads/thumbnail/abc-123/image.jpg")
		}

		// Verify second variant
		if resp.Variants[1].Type != "medium" {
			t.Errorf("second variant type = %q, want %q", resp.Variants[1].Type, "medium")
		}

		// Verify third variant
		if resp.Variants[2].Type != "large" {
			t.Errorf("third variant type = %q, want %q", resp.Variants[2].Type, "large")
		}
	})
}
