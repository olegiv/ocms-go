// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/config"
)

func TestNewDocsHandler(t *testing.T) {
	cfg := &config.Config{
		Env:        "development",
		ServerPort: 8080,
		DBPath:     "./data/test.db",
	}
	startTime := time.Now()

	h := NewDocsHandler(nil, cfg, nil, startTime)
	if h == nil {
		t.Fatal("NewDocsHandler returned nil")
	}
	if h.cfg != cfg {
		t.Error("cfg not set correctly")
	}
	if h.docsDir != DocsDir {
		t.Errorf("docsDir = %q; want %q", h.docsDir, DocsDir)
	}
	if h.startTime != startTime {
		t.Error("startTime not set correctly")
	}
}

func TestDocsPageData(t *testing.T) {
	data := DocsPageData{
		System: DocsSystemInfo{
			GoVersion:      "go1.24.0",
			Environment:    "production",
			ServerPort:     8080,
			DBPath:         "./data/ocms.db",
			ActiveTheme:    "default",
			CacheType:      "Memory",
			EnabledModules: 3,
			TotalModules:   5,
			Uptime:         "1h30m0s",
		},
		Endpoints: []DocsEndpointGroup{
			{
				Name: "Health Check",
				Endpoints: []DocsEndpoint{
					{Method: "GET", Path: "/health", Description: "Overall health status", Auth: "None"},
				},
			},
		},
		Guides: []DocsGuide{
			{Slug: "webhooks", Title: "Webhooks"},
		},
	}

	if data.System.GoVersion != "go1.24.0" {
		t.Errorf("GoVersion = %q; want go1.24.0", data.System.GoVersion)
	}
	if len(data.Endpoints) != 1 {
		t.Errorf("Endpoints count = %d; want 1", len(data.Endpoints))
	}
	if len(data.Guides) != 1 {
		t.Errorf("Guides count = %d; want 1", len(data.Guides))
	}
}

func TestIsValidDocsSlug(t *testing.T) {
	tests := []struct {
		slug string
		want bool
	}{
		{"webhooks", true},
		{"reverse-proxy", true},
		{"multi-language", true},
		{"deploy-ubuntu-plesk", true},
		{"PHASE1", true},
		{"test_file", true},
		{"CamelCase", true},
		{"", false},
		{"../etc/passwd", false},
		{"path/traversal", false},
		{"file.md", false},
		{"hello world", false},
		{"special@char", false},
		{"file\x00null", false},
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			got := isValidDocsSlug(tt.slug)
			if got != tt.want {
				t.Errorf("isValidDocsSlug(%q) = %v; want %v", tt.slug, got, tt.want)
			}
		})
	}
}

func TestSlugToTitle(t *testing.T) {
	tests := []struct {
		slug string
		want string
	}{
		{"webhooks", "Webhooks"},
		{"reverse-proxy", "Reverse Proxy"},
		{"multi-language", "Multi Language"},
		{"deploy-ubuntu-plesk", "Deploy Ubuntu Plesk"},
		{"login-security", "Login Security"},
		{"import-export", "Import Export"},
		{"i18n", "I18n"},
		{"csrf", "Csrf"},
		{"hcaptcha", "Hcaptcha"},
		{"developer-module", "Developer Module"},
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			got := slugToTitle(tt.slug)
			if got != tt.want {
				t.Errorf("slugToTitle(%q) = %q; want %q", tt.slug, got, tt.want)
			}
		})
	}
}

func TestDocsHandler_ListGuides(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test markdown files
	testFiles := map[string]string{
		"webhooks.md":           "# Webhooks\nContent here",
		"media.md":              "# Media\nContent here",
		"PHASE1.md":             "# Phase 1\nShould be filtered",
		"PHASE2.md":             "# Phase 2\nShould be filtered",
		"reverse-proxy.md":      "# Reverse Proxy\nContent",
		"not-markdown.txt":      "This is not markdown",
		"deploy-ubuntu-plesk.md": "# Deploy\nContent",
	}

	for name, content := range testFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("failed to create test file %s: %v", name, err)
		}
	}

	h := &DocsHandler{docsDir: tmpDir}
	guides := h.listGuides()

	// Should have 4 guides (webhooks, media, reverse-proxy, deploy-ubuntu-plesk)
	// PHASE*.md and .txt files should be excluded
	if len(guides) != 4 {
		t.Errorf("listGuides() returned %d guides; want 4", len(guides))
		for _, g := range guides {
			t.Logf("  guide: %s (%s)", g.Title, g.Slug)
		}
	}

	// Check PHASE files are excluded
	for _, g := range guides {
		if g.Slug == "PHASE1" || g.Slug == "PHASE2" {
			t.Errorf("PHASE file %q should be excluded", g.Slug)
		}
	}

	// Guides should be sorted by title
	for i := 1; i < len(guides); i++ {
		if guides[i-1].Title > guides[i].Title {
			t.Errorf("guides not sorted: %q > %q", guides[i-1].Title, guides[i].Title)
		}
	}
}

func TestDocsHandler_ListGuides_MissingDir(t *testing.T) {
	h := &DocsHandler{docsDir: "/nonexistent/path"}
	guides := h.listGuides()

	if guides != nil {
		t.Errorf("listGuides() for missing dir = %v; want nil", guides)
	}
}

func TestDocsHandler_GetSystemInfo(t *testing.T) {
	cfg := &config.Config{
		Env:         "production",
		ServerPort:  9090,
		DBPath:      "/var/data/ocms.db",
		ActiveTheme: "custom",
	}

	h := NewDocsHandler(nil, cfg, nil, time.Now().Add(-time.Hour))
	info := h.getSystemInfo()

	if info.GoVersion == "" {
		t.Error("GoVersion should not be empty")
	}
	if info.Environment != "production" {
		t.Errorf("Environment = %q; want production", info.Environment)
	}
	if info.ServerPort != 9090 {
		t.Errorf("ServerPort = %d; want 9090", info.ServerPort)
	}
	if info.DBPath != "/var/data/ocms.db" {
		t.Errorf("DBPath = %q; want /var/data/ocms.db", info.DBPath)
	}
	if info.ActiveTheme != "custom" {
		t.Errorf("ActiveTheme = %q; want custom", info.ActiveTheme)
	}
	if info.CacheType != "Memory" {
		t.Errorf("CacheType = %q; want Memory", info.CacheType)
	}
	if info.Uptime == "" {
		t.Error("Uptime should not be empty")
	}
}

func TestDocsHandler_GetSystemInfo_Redis(t *testing.T) {
	cfg := &config.Config{
		Env:      "development",
		RedisURL: "redis://localhost:6379",
	}

	h := NewDocsHandler(nil, cfg, nil, time.Now())
	info := h.getSystemInfo()

	if info.CacheType != "Redis" {
		t.Errorf("CacheType = %q; want Redis", info.CacheType)
	}
}

func TestDocsHandler_GetEndpoints(t *testing.T) {
	cfg := &config.Config{Env: "development"}
	h := NewDocsHandler(nil, cfg, nil, time.Now())

	groups := h.getEndpoints("en")

	if len(groups) != 4 {
		t.Fatalf("getEndpoints() returned %d groups; want 4", len(groups))
	}

	// Verify each group has endpoints
	for _, g := range groups {
		if g.Name == "" {
			t.Error("endpoint group name should not be empty")
		}
		if len(g.Endpoints) == 0 {
			t.Errorf("endpoint group %q should have endpoints", g.Name)
		}
		for _, ep := range g.Endpoints {
			if ep.Method == "" {
				t.Errorf("endpoint in %q has empty method", g.Name)
			}
			if ep.Path == "" {
				t.Errorf("endpoint in %q has empty path", g.Name)
			}
		}
	}
}

func TestDocsGuideData(t *testing.T) {
	data := DocsGuideData{
		Title:   "Test Guide",
		Content: "<h1>Test</h1>",
	}

	if data.Title != "Test Guide" {
		t.Errorf("Title = %q; want Test Guide", data.Title)
	}
	if data.Content != "<h1>Test</h1>" {
		t.Errorf("Content = %q; want <h1>Test</h1>", data.Content)
	}
}
