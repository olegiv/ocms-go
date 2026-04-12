// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveNonImageFile_AllowsDoubleDotInFilename(t *testing.T) {
	s := NewSource()

	tempDir := t.TempDir()
	srcPath := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(srcPath, []byte("example payload"), 0o600); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("failed to open source file: %v", err)
	}
	defer func() { _ = src.Close() }()

	uploadDir := filepath.Join(tempDir, "uploads")
	fileUUID := "abc123"
	filename := "report..pdf"

	if err := s.saveNonImageFile(src, uploadDir, fileUUID, filename); err != nil {
		t.Fatalf("saveNonImageFile() returned error for valid filename: %v", err)
	}

	destPath := filepath.Join(uploadDir, "originals", fileUUID, filename)
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}

	if string(content) != "example payload" {
		t.Fatalf("destination content = %q, want %q", string(content), "example payload")
	}
}
