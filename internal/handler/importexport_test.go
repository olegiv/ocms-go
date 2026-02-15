// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadImportFileContent(t *testing.T) {
	content, err := readImportFileContent(strings.NewReader("abc"), 3)
	if err != nil {
		t.Fatalf("expected successful read, got %v", err)
	}
	if string(content) != "abc" {
		t.Fatalf("expected content to match, got %q", string(content))
	}
}

func TestReadImportFileContent_TooLarge(t *testing.T) {
	_, err := readImportFileContent(bytes.NewReader([]byte("abcd")), 3)
	if err == nil {
		t.Fatal("expected size-limit error")
	}
	if !strings.Contains(err.Error(), "file is too large") {
		t.Fatalf("expected too-large error, got %v", err)
	}
}
