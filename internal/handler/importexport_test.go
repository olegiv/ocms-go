// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/transfer"
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

func TestFindSuspiciousImportPages(t *testing.T) {
	data := &transfer.ExportData{
		Pages: []transfer.ExportPage{
			{Slug: "clean", Body: "<p>Hello</p>"},
			{Slug: "xss", Body: "<script>alert(1)</script>"},
			{Slug: "", Body: "<iframe src=\"https://evil.example\"></iframe>"},
		},
	}

	matches := findSuspiciousImportPages(data)
	if len(matches) != 2 {
		t.Fatalf("expected 2 suspicious pages, got %d", len(matches))
	}
	if matches[0].Slug != "xss" {
		t.Fatalf("expected first slug to be xss, got %q", matches[0].Slug)
	}
	if matches[1].Slug != "(empty-slug)" {
		t.Fatalf("expected empty slug placeholder, got %q", matches[1].Slug)
	}
}

func TestApplyImportPageSecurityPolicy_BlocksWhenEnabled(t *testing.T) {
	h := &ImportExportHandler{blockSuspiciousMarkup: true}
	data := &transfer.ExportData{
		Pages: []transfer.ExportPage{
			{Slug: "xss", Body: "<script>alert(1)</script>"},
		},
	}

	err := h.applyImportPageSecurityPolicy(nil, data, "import", true)
	if err == nil {
		t.Fatal("expected policy error for suspicious import page content")
	}
	if !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("expected blocked policy error, got %v", err)
	}
}

func TestApplyImportPageSecurityPolicy_AllowsWhenPagesNotImported(t *testing.T) {
	h := &ImportExportHandler{blockSuspiciousMarkup: true}
	data := &transfer.ExportData{
		Pages: []transfer.ExportPage{
			{Slug: "xss", Body: "<script>alert(1)</script>"},
		},
	}

	if err := h.applyImportPageSecurityPolicy(nil, data, "import", false); err != nil {
		t.Fatalf("expected no error when page import is disabled, got %v", err)
	}
}
