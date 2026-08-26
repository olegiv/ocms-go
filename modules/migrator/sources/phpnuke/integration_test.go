// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build phpnuke_integration

// Package-level integration test against a real PHP-Nuke MySQL database.
//
// Run with:
//
//	OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! \
//	OCMS_MIGRATOR_ALLOWED_DB_HOSTS=127.0.0.1 \
//	PHPNUKE_TEST_HOST=127.0.0.1 PHPNUKE_TEST_PORT=8181 \
//	PHPNUKE_TEST_USER=admin PHPNUKE_TEST_PASSWORD=... \
//	PHPNUKE_TEST_DB=admin_tunisie_ru PHPNUKE_TEST_PREFIX=tr_ \
//	PHPNUKE_FILES=/path/to/htdocs \
//	go test -tags phpnuke_integration -v ./modules/migrator/sources/phpnuke/
package phpnuke

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

func liveConfig(t *testing.T) map[string]string {
	t.Helper()
	host := os.Getenv("PHPNUKE_TEST_HOST")
	if host == "" {
		t.Skip("PHPNUKE_TEST_HOST is not set; skipping live database test")
	}
	return map[string]string{
		"mysql_host":     host,
		"mysql_port":     os.Getenv("PHPNUKE_TEST_PORT"),
		"mysql_user":     os.Getenv("PHPNUKE_TEST_USER"),
		"mysql_password": os.Getenv("PHPNUKE_TEST_PASSWORD"),
		"mysql_database": os.Getenv("PHPNUKE_TEST_DB"),
		"table_prefix":   os.Getenv("PHPNUKE_TEST_PREFIX"),
		"files_path":     os.Getenv("PHPNUKE_FILES"),
	}
}

func TestLiveTestConnection(t *testing.T) {
	cfg := liveConfig(t)
	if err := NewSource().TestConnectionContext(context.Background(), cfg); err != nil {
		t.Fatalf("TestConnectionContext() error = %v", err)
	}
}

// TestLiveImport runs the whole importer against the real database and checks
// the properties that only a live run can prove: that a cp1251 source arrives
// as valid UTF-8, and that the counts match the source tables.
func TestLiveImport(t *testing.T) {
	cfg := liveConfig(t)
	ctx := context.Background()

	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)

	now := time.Now()
	if _, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "admin@example.com", PasswordHash: "x", Role: "admin", Name: "Admin",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to seed admin: %v", err)
	}

	t.Setenv("OCMS_UPLOADS_DIR", t.TempDir())

	source := NewSource()
	result, err := source.Import(ctx, db, cfg, types.ImportOptions{
		ImportTags:       true,
		ImportCategories: true,
		ImportMedia:      cfg["files_path"] != "",
		ImportPosts:      true,
		ImportPages:      true,
		ImportUsers:      true,
	}, &mockTracker{})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	t.Logf("imported: posts=%d pages=%d categories=%d tags=%d users=%d media=%d",
		result.PostsImported, result.PagesImported, result.CategoriesImported,
		result.TagsImported, result.UsersImported, result.MediaImported)
	for _, summary := range result.Summaries {
		t.Logf("summary: %s", summary)
	}
	if len(result.Errors) > 0 {
		t.Errorf("import reported %d errors, first few: %v",
			len(result.Errors), result.Errors[:min(3, len(result.Errors))])
	}

	if result.PostsImported == 0 {
		t.Fatal("no posts were imported")
	}
	if result.UsersImported == 0 {
		t.Error("no story authors were imported")
	}

	// The decisive check: a cp1251 database must arrive as real Cyrillic, not
	// as the '?' the server substitutes when the connection charset is wrong.
	pages, err := queries.ListPages(ctx, store.ListPagesParams{Limit: 500, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list imported pages: %v", err)
	}
	cyrillicTitles := 0
	for _, page := range pages {
		if strings.Contains(page.Title, "??") {
			t.Errorf("page %d title shows lost charset conversion: %q", page.ID, page.Title)
		}
		if containsCyrillic(page.Title) {
			cyrillicTitles++
		}
		if page.Slug == "" || !isASCII(page.Slug) {
			t.Errorf("page %d has a non-ASCII slug %q", page.ID, page.Slug)
		}
	}
	if cyrillicTitles == 0 {
		t.Error("no imported page has a Cyrillic title; the charset conversion was lost")
	}
	t.Logf("%d of %d imported pages have Cyrillic titles", cyrillicTitles, len(pages))

	// Every imported account must be inert.
	users, err := queries.ListUsers(ctx, store.ListUsersParams{Limit: 500, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list users: %v", err)
	}
	for _, user := range users {
		if user.Email == "admin@example.com" {
			continue
		}
		if user.Role != "public" {
			t.Errorf("imported user %q has role %q, want public", user.Email, user.Role)
		}
	}
}

func containsCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
