// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package elefant

import (
	"os"
	"testing"
)

// TestImportFromElefant tests importing from Elefant CMS.
// Run with: go test -tags=integration -v ./modules/migrator/sources/elefant/...
func TestImportFromElefant(t *testing.T) {
	// Skip if env vars not set
	if os.Getenv("ELEFANT_HOST") == "" {
		t.Skip("ELEFANT_HOST not set, skipping integration test")
	}

	source := NewSource()

	cfg := map[string]string{
		"mysql_host":     os.Getenv("ELEFANT_HOST"),
		"mysql_port":     os.Getenv("ELEFANT_PORT"),
		"mysql_user":     os.Getenv("ELEFANT_USER"),
		"mysql_password": os.Getenv("ELEFANT_PASSWORD"),
		"mysql_database": os.Getenv("ELEFANT_DB"),
		"table_prefix":   os.Getenv("ELEFANT_PREFIX"),
	}

	t.Logf("Config: host=%s, port=%s, db=%s, prefix=%s",
		cfg["mysql_host"], cfg["mysql_port"], cfg["mysql_database"], cfg["table_prefix"])

	// Test connection
	t.Run("TestConnection", func(t *testing.T) {
		if err := source.TestConnection(cfg); err != nil {
			t.Fatalf("TestConnection failed: %v", err)
		}
		t.Log("Connection successful")
	})

	// Test reading posts (this exercises schema detection)
	t.Run("ReadPosts", func(t *testing.T) {
		dsn := source.buildDSN(cfg)
		reader, err := NewReader(dsn, cfg["table_prefix"])
		if err != nil {
			t.Fatalf("NewReader failed: %v", err)
		}
		defer func() { _ = reader.Close() }()

		posts, err := reader.GetBlogPosts()
		if err != nil {
			t.Fatalf("GetBlogPosts failed: %v", err)
		}

		t.Logf("Found %d posts", len(posts))
		t.Logf("Schema detection: hasSlug=%v, hasDescription=%v, hasKeywords=%v",
			reader.hasSlug, reader.hasDescription, reader.hasKeywords)

		// Show first few posts
		for i, p := range posts {
			if i >= 3 {
				break
			}
			t.Logf("Post %d: ID=%d, Title=%q, Slug=%q", i, p.ID, p.Title, p.Slug)
		}
	})
}

// TestSlugGeneration tests that slugs are generated from titles.
func TestSlugGeneration(t *testing.T) {
	// Skip if env vars not set
	if os.Getenv("ELEFANT_HOST") == "" {
		t.Skip("ELEFANT_HOST not set, skipping integration test")
	}

	source := NewSource()
	cfg := map[string]string{
		"mysql_host":     os.Getenv("ELEFANT_HOST"),
		"mysql_port":     os.Getenv("ELEFANT_PORT"),
		"mysql_user":     os.Getenv("ELEFANT_USER"),
		"mysql_password": os.Getenv("ELEFANT_PASSWORD"),
		"mysql_database": os.Getenv("ELEFANT_DB"),
		"table_prefix":   os.Getenv("ELEFANT_PREFIX"),
	}

	dsn := source.buildDSN(cfg)
	reader, err := NewReader(dsn, cfg["table_prefix"])
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}
	defer func() { _ = reader.Close() }()

	posts, err := reader.GetBlogPosts()
	if err != nil {
		t.Fatalf("GetBlogPosts failed: %v", err)
	}

	// Verify slugs are empty (old schema) and can be generated from title
	for i, p := range posts {
		if i >= 5 {
			break
		}

		// Simulate slug generation as importer would do
		slug := p.Slug
		if slug == "" {
			slug = generateSlugFromTitle(p.Title)
		}

		t.Logf("Post %d: Title=%q -> Slug=%q", p.ID, p.Title, slug)

		if slug == "" {
			t.Errorf("Post %d: failed to generate slug from title %q", p.ID, p.Title)
		}
	}
}

// generateSlugFromTitle mimics util.Slugify for testing.
func generateSlugFromTitle(title string) string {
	// Simple slugify for testing
	result := ""
	for _, r := range title {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result += string(r)
		} else if (r >= 'A' && r <= 'Z') {
			result += string(r + 32) // lowercase
		} else if r == ' ' || r == '-' || r == '_' {
			if len(result) > 0 && result[len(result)-1] != '-' {
				result += "-"
			}
		}
	}
	// Trim trailing dash
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	return result
}
