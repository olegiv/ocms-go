// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"archive/zip"
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestParseMediaZipPath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid", input: "media/originals/550e8400-e29b-41d4-a716-446655440000/photo.jpg"},
		{name: "path traversal", input: "media/../../etc/passwd", wantErr: true},
		{name: "extra segments", input: "media/originals/uuid/nested/path.jpg", wantErr: true},
		{name: "dot segment", input: "media/originals/uuid/./file.jpg", wantErr: true},
		{name: "missing filename", input: "media/originals/uuid/", wantErr: true},
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

	importer := NewImporter(nil, nil, slog.Default())
	importer.SetUploadDir(t.TempDir())

	_, err := importer.extractMediaFiles(reader)
	if err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid media path") {
		t.Fatalf("expected invalid media path error, got: %v", err)
	}
}

func TestExtractMediaFiles_RejectsOversizedMediaEntry(t *testing.T) {
	reader := newZipReader(t, map[string][]byte{
		"media/originals/test-uuid/big.bin": bytes.Repeat([]byte("A"), maxZipMediaFileUncompressedBytes+1),
	})

	importer := NewImporter(nil, nil, slog.Default())
	importer.SetUploadDir(t.TempDir())

	_, err := importer.extractMediaFiles(reader)
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
