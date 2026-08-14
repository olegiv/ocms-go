// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

func TestCreateVariantFromRootStaysBoundAcrossPathReplacement(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	parent := t.TempDir()
	uploadDir := filepath.Join(parent, "uploads")
	renamedDir := filepath.Join(parent, "captured-uploads")
	originalPath := filepath.Join(uploadDir, model.OriginalsDir, mediaUUID, "image.png")
	if err := os.MkdirAll(filepath.Dir(originalPath), 0o750); err != nil {
		t.Fatal(err)
	}
	var original bytes.Buffer
	if err := png.Encode(&original, createTestImage(800, 600)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalPath, original.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	uploadRoot, err := OpenUploadRoot(uploadDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = uploadRoot.Close() }()

	if err := os.Rename(uploadDir, renamedDir); err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(uploadDir, model.VariantThumbnail, mediaUUID, "image.png")
	if err := os.MkdirAll(filepath.Dir(replacementPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	variant, creation, err := CreateVariantFromRoot(uploadRoot, mediaUUID, "image.png", model.VariantThumbnail)
	if err != nil || variant == nil || creation == nil || creation.Directory == nil {
		t.Fatalf("CreateVariantFromRoot() = (%+v, %+v, %v)", variant, creation, err)
	}
	generatedPath := filepath.Join(renamedDir, model.VariantThumbnail, mediaUUID, "image.png")
	generated, err := os.Open(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	width, height, mimeType, validateErr := ValidateImage(generated)
	closeErr := generated.Close()
	if err := errors.Join(validateErr, closeErr); err != nil {
		t.Fatalf("validate generated variant: %v", err)
	}
	if width != variant.Width || height != variant.Height || mimeType != model.MimeTypePNG {
		t.Fatalf("generated image = %dx%d %s, metadata = %dx%d", width, height, mimeType, variant.Width, variant.Height)
	}
	if content, err := os.ReadFile(replacementPath); err != nil || string(content) != "replacement" {
		t.Fatalf("replacement-root target = %q, error = %v; want preserved", content, err)
	}
}
