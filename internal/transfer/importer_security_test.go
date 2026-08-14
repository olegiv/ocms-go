// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestParseMediaZipPath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid lowercase", input: "media/originals/550e8400-e29b-41d4-a716-446655440000/photo.jpg"},
		{name: "valid uppercase", input: "media/thumbnail/550E8400-E29B-41D4-A716-446655440000/photo.jpg"},
		{name: "noncanonical readable identifier", input: "media/originals/legacy-media-1/photo.jpg", wantErr: true},
		{name: "unhyphenated UUID", input: "media/originals/550e8400e29b41d4a716446655440000/photo.jpg", wantErr: true},
		{name: "URN UUID", input: "media/originals/urn:uuid:550e8400-e29b-41d4-a716-446655440000/photo.jpg", wantErr: true},
		{name: "braced UUID", input: "media/originals/{550e8400-e29b-41d4-a716-446655440000}/photo.jpg", wantErr: true},
		{name: "short UUID", input: "media/originals/550e8400-e29b-41d4-a716-44665544000/photo.jpg", wantErr: true},
		{name: "wrong hyphen position", input: "media/originals/550e840-0e29b-41d4-a716-446655440000/photo.jpg", wantErr: true},
		{name: "nonhex UUID", input: "media/originals/zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz/photo.jpg", wantErr: true},
		{name: "leading UUID whitespace", input: "media/originals/ 550e8400-e29b-41d4-a716-446655440000/photo.jpg", wantErr: true},
		{name: "trailing UUID whitespace", input: "media/originals/550e8400-e29b-41d4-a716-446655440000 /photo.jpg", wantErr: true},
		{name: "unsupported storage directory", input: "media/legacy/550e8400-e29b-41d4-a716-446655440000/photo.jpg", wantErr: true},
		{name: "path traversal", input: "media/../../etc/passwd", wantErr: true},
		{name: "extra segments", input: "media/originals/550e8400-e29b-41d4-a716-446655440000/nested/path.jpg", wantErr: true},
		{name: "dot segment", input: "media/originals/550e8400-e29b-41d4-a716-446655440000/./file.jpg", wantErr: true},
		{name: "missing filename", input: "media/originals/550e8400-e29b-41d4-a716-446655440000/", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseMediaZipPath(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error for %q, got %v", tt.input, err)
			}
		})
	}
}

func TestExtractMediaFiles_RejectsPathTraversal(t *testing.T) {
	reader := newZipReader(t, map[string][]byte{
		"media/originals/../escape/file.jpg": []byte("x"),
	})
	_, err := buildMediaZipManifest(reader, &ExportData{})
	if err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid media path") {
		t.Fatalf("expected invalid media path error, got: %v", err)
	}
}

func TestExtractMediaFiles_RejectsOversizedMediaEntry(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	reader := newZipReader(t, map[string][]byte{
		"media/originals/" + mediaUUID + "/big.bin": bytes.Repeat([]byte("A"), maxZipMediaFileUncompressedBytes+1),
	})
	_, err := buildMediaZipManifest(reader, &ExportData{Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "big.bin", Size: maxZipMediaFileUncompressedBytes + 1,
		FilePath: "media/originals/" + mediaUUID + "/big.bin",
	}}})
	if err == nil {
		t.Fatal("expected oversized media file to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds max size") {
		t.Fatalf("expected max size error, got: %v", err)
	}
}

func TestCopyWithLimit(t *testing.T) {
	_, err := copyWithLimit(io.Discard, strings.NewReader("123456"), 5)
	if err == nil {
		t.Fatal("expected copyWithLimit to fail when content exceeds limit")
	}

	written, err := copyWithLimit(io.Discard, strings.NewReader("12345"), 5)
	if err != nil {
		t.Fatalf("expected copyWithLimit to pass at exact limit, got: %v", err)
	}
	if written != 5 {
		t.Fatalf("expected 5 bytes written, got %d", written)
	}
}

func newZipReader(t *testing.T, files map[string][]byte) *zip.Reader {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed creating zip entry %q: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("failed writing zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed closing zip writer: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed opening zip reader: %v", err)
	}
	return zr
}
