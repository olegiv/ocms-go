package imaging

import (
	"testing"

	"ocms-go/internal/model"
)

func TestProcessorIsImage(t *testing.T) {
	p := NewProcessor("./uploads")

	tests := []struct {
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
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := p.IsImage(tt.mimeType); got != tt.want {
				t.Errorf("IsImage(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestProcessorIsSupportedType(t *testing.T) {
	p := NewProcessor("./uploads")

	tests := []struct {
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
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := p.IsSupportedType(tt.mimeType); got != tt.want {
				t.Errorf("IsSupportedType(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestCalculateFitDimensions(t *testing.T) {
	tests := []struct {
		name                   string
		srcW, srcH, maxW, maxH int
		wantW, wantH           int
	}{
		{
			name: "smaller than max",
			srcW: 100, srcH: 100, maxW: 800, maxH: 600,
			wantW: 100, wantH: 100,
		},
		{
			name: "width limiting",
			srcW: 1600, srcH: 1200, maxW: 800, maxH: 600,
			wantW: 800, wantH: 600,
		},
		{
			name: "height limiting",
			srcW: 800, srcH: 1200, maxW: 800, maxH: 600,
			wantW: 400, wantH: 600,
		},
		{
			name: "landscape to smaller",
			srcW: 1920, srcH: 1080, maxW: 800, maxH: 600,
			wantW: 800, wantH: 450,
		},
		{
			name: "portrait to smaller",
			srcW: 1080, srcH: 1920, maxW: 800, maxH: 600,
			wantW: 337, wantH: 600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotW, gotH := calculateFitDimensions(tt.srcW, tt.srcH, tt.maxW, tt.maxH)
			if gotW != tt.wantW || gotH != tt.wantH {
				t.Errorf("calculateFitDimensions(%d, %d, %d, %d) = (%d, %d), want (%d, %d)",
					tt.srcW, tt.srcH, tt.maxW, tt.maxH, gotW, gotH, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestBimgTypeToMimeType(t *testing.T) {
	tests := []struct {
		imgType string
		want    string
	}{
		{"jpeg", model.MimeTypeJPEG},
		{"JPEG", model.MimeTypeJPEG},
		{"jpg", model.MimeTypeJPEG},
		{"png", model.MimeTypePNG},
		{"PNG", model.MimeTypePNG},
		{"gif", model.MimeTypeGIF},
		{"webp", model.MimeTypeWebP},
		{"unknown", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.imgType, func(t *testing.T) {
			if got := bimgTypeToMimeType(tt.imgType); got != tt.want {
				t.Errorf("bimgTypeToMimeType(%q) = %v, want %v", tt.imgType, got, tt.want)
			}
		})
	}
}
