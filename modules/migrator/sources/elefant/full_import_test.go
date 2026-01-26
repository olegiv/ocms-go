// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fullimport

package elefant

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// TestFullImport tests importing to the actual oCMS database.
// Run with: go test -tags=fullimport -v ./modules/migrator/sources/elefant/...
func TestFullImport(t *testing.T) {
	// Skip if env vars not set
	if os.Getenv("ELEFANT_HOST") == "" {
		t.Skip("ELEFANT_HOST not set, skipping")
	}

	dbPath := os.Getenv("OCMS_DB_PATH")
	if dbPath == "" {
		// Try common paths
		paths := []string{
			"./data/ocms.db",
			"../../data/ocms.db",
			"../../../data/ocms.db",
			"../../../../data/ocms.db",
			"../../../../../data/ocms.db",
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				dbPath = p
				break
			}
		}
		if dbPath == "" {
			t.Fatal("Could not find ocms.db - set OCMS_DB_PATH env var")
		}
	}

	// Open oCMS database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to open oCMS database: %v", err)
	}
	defer db.Close()

	// Verify database is accessible
	var userCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount); err != nil {
		t.Fatalf("Failed to query users: %v (is the database initialized?)", err)
	}
	t.Logf("Found %d users in oCMS database", userCount)

	source := NewSource()
	cfg := map[string]string{
		"mysql_host":     os.Getenv("ELEFANT_HOST"),
		"mysql_port":     os.Getenv("ELEFANT_PORT"),
		"mysql_user":     os.Getenv("ELEFANT_USER"),
		"mysql_password": os.Getenv("ELEFANT_PASSWORD"),
		"mysql_database": os.Getenv("ELEFANT_DB"),
		"table_prefix":   os.Getenv("ELEFANT_PREFIX"),
	}

	t.Logf("Importing from Elefant: %s:%s/%s (prefix: %s)",
		cfg["mysql_host"], cfg["mysql_port"], cfg["mysql_database"], cfg["table_prefix"])

	opts := types.ImportOptions{
		ImportTags:   true,
		ImportMedia:  false,
		ImportPosts:  true,
		SkipExisting: true,
	}

	result, err := source.Import(context.Background(), db, cfg, opts, nil)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	t.Logf("=== IMPORT RESULTS ===")
	t.Logf("Posts imported: %d", result.PostsImported)
	t.Logf("Tags imported:  %d", result.TagsImported)
	t.Logf("Posts skipped:  %d", result.PostsSkipped)
	t.Logf("Tags skipped:   %d", result.TagsSkipped)
	if len(result.Errors) > 0 {
		t.Logf("Errors (%d):", len(result.Errors))
		for i, e := range result.Errors {
			t.Logf("  [%d] %s", i, e)
		}
	}

	// Verify data in database
	var pageCount, tagCount int
	db.QueryRow("SELECT COUNT(*) FROM pages").Scan(&pageCount)
	db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&tagCount)
	t.Logf("=== DATABASE TOTALS ===")
	t.Logf("Total pages: %d", pageCount)
	t.Logf("Total tags:  %d", tagCount)

	// Show some imported pages
	rows, err := db.Query("SELECT id, title, slug, status FROM pages ORDER BY id DESC LIMIT 5")
	if err == nil {
		defer rows.Close()
		t.Logf("=== RECENT PAGES ===")
		for rows.Next() {
			var id int64
			var title, slug, status string
			rows.Scan(&id, &title, &slug, &status)
			t.Logf("  [%d] %s (%s) - %s", id, title[:min(40, len(title))], slug[:min(30, len(slug))], status)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
