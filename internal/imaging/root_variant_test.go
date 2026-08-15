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

// TestSameRootFileRejectsSameInodeReplacement pins the reason SameRootFile
// exists. A filesystem is free to hand a freed inode number to the next file
// created at the same path, so os.SameFile on its own cannot tell a recorded
// file from its replacement. The rewrite below keeps the inode deliberately —
// on every platform — so the test fails the moment the size and modification
// time comparison is dropped and the check falls back to inode identity.
func TestSameRootFileRejectsSameInodeReplacement(t *testing.T) {
	ownedPath := filepath.Join(t.TempDir(), "owned")
	if err := os.WriteFile(ownedPath, []byte("owned contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorded, err := os.Lstat(ownedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !SameRootFile(recorded, recorded) {
		t.Fatal("SameRootFile() rejected an unchanged file")
	}

	file, err := os.OpenFile(ownedPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write([]byte("replacement contents that differ"))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	current, err := os.Lstat(ownedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(current, recorded) {
		t.Fatal("in-place rewrite changed the inode; the test no longer exercises inode reuse")
	}
	if SameRootFile(current, recorded) {
		t.Fatal("SameRootFile() accepted a replacement that only shares an inode number")
	}
}

// TestCreateVariantFromRootRecordsPublishedIdentity fails if the returned
// identity is captured from the empty file O_EXCL creates instead of from the
// written one: a zero-size snapshot makes SameRootFile reject the variant this
// call just published, so compensation would refuse to clean up after itself.
func TestCreateVariantFromRootRecordsPublishedIdentity(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	uploadDir := t.TempDir()
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

	_, creation, err := CreateVariantFromRoot(uploadRoot, mediaUUID, "image.png", model.VariantThumbnail)
	if err != nil || creation == nil {
		t.Fatalf("CreateVariantFromRoot() = (%+v, %v)", creation, err)
	}
	if creation.File.Info.Size() == 0 {
		t.Fatal("recorded identity has size 0; it was captured before the variant was written")
	}
	current, err := uploadRoot.Lstat(creation.File.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !SameRootFile(current, creation.File.Info) {
		t.Fatal("SameRootFile() rejected the variant this call published")
	}
}
