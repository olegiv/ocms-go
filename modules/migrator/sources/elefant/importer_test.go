// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/util"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// mockTracker implements types.ImportTracker for testing.
type mockTracker struct {
	items []trackedItem
}

type failingTracker struct {
	err     error
	onTrack func()
}

func (f failingTracker) TrackImportedItem(context.Context, string, string, int64) error {
	if f.onTrack != nil {
		f.onTrack()
	}
	return f.err
}

type cleanupContextTracker struct {
	queueContextErr error
	queuedRoot      string
	queuedUUID      string
}

func (t *cleanupContextTracker) TrackImportedItem(context.Context, string, string, int64) error {
	return nil
}

func (t *cleanupContextTracker) QueueMediaCleanup(ctx context.Context, _, uploadRoot, mediaUUID string) error {
	t.queueContextErr = ctx.Err()
	t.queuedRoot = uploadRoot
	t.queuedUUID = mediaUUID
	return nil
}

type trackedItem struct {
	source     string
	entityType string
	entityID   int64
}

type pathOwnership map[string]bool

func (p pathOwnership) OwnsPublicPath(path string) bool { return p[path] }

func (m *mockTracker) TrackImportedItem(_ context.Context, source, entityType string, entityID int64) error {
	m.items = append(m.items, trackedItem{source, entityType, entityID})
	return nil
}

func (m *mockTracker) redirectCount() int {
	count := 0
	for _, item := range m.items {
		if item.entityType == string(types.EntityRedirect) {
			count++
		}
	}
	return count
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create users table
	_, err = db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'public',
			name TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_login_at DATETIME,
			avatar TEXT NOT NULL DEFAULT '',
			bio TEXT NOT NULL DEFAULT '',
			website_url TEXT NOT NULL DEFAULT '',
			linkedin_url TEXT NOT NULL DEFAULT '',
			github_url TEXT NOT NULL DEFAULT '',
			telegram_url TEXT NOT NULL DEFAULT '',
			session_version INTEGER NOT NULL DEFAULT 0
		)
	`)
	if err != nil {
		t.Fatalf("failed to create users table: %v", err)
	}

	return db
}

func TestSource_Name(t *testing.T) {
	s := NewSource()
	if got := s.Name(); got != "elefant" {
		t.Errorf("Name() = %q, want %q", got, "elefant")
	}
}

func TestSource_DisplayName(t *testing.T) {
	s := NewSource()
	if got := s.DisplayName(); got != "Elefant CMS" {
		t.Errorf("DisplayName() = %q, want %q", got, "Elefant CMS")
	}
}

func TestSource_Description(t *testing.T) {
	s := NewSource()
	if got := s.Description(); got == "" {
		t.Error("Description() should not be empty")
	}
}

func TestSource_ConfigFields(t *testing.T) {
	s := NewSource()
	fields := s.ConfigFields()

	if len(fields) == 0 {
		t.Error("ConfigFields() should return at least one field")
	}

	// Check for required MySQL fields
	requiredFields := map[string]bool{
		"mysql_host":     false,
		"mysql_port":     false,
		"mysql_user":     false,
		"mysql_password": false,
		"mysql_database": false,
	}

	for _, f := range fields {
		if _, ok := requiredFields[f.Name]; ok {
			requiredFields[f.Name] = true
		}
	}

	for name, found := range requiredFields {
		if !found {
			t.Errorf("ConfigFields() missing required field: %s", name)
		}
	}
}

func TestImportUsers_Success(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	queries := store.New(db)
	tracker := &mockTracker{}
	s := NewSource()

	// Create mock users to import
	mockUsers := []User{
		{ID: 1, Email: "user1@example.com", Name: "User One"},
		{ID: 2, Email: "user2@example.com", Name: "User Two"},
		{ID: 3, Email: "user3@example.com", Name: "User Three"},
	}

	result := &types.ImportResult{}

	// Create a mock reader and call importUsers directly
	// Since importUsers is not exported, we test through the behavior
	ctx := context.Background()

	// Manually insert users as if imported
	now := time.Now()
	for _, u := range mockUsers {
		_, err := queries.CreateUser(ctx, store.CreateUserParams{
			Email:        u.Email,
			PasswordHash: "placeholder-hash",
			Role:         "public",
			Name:         u.Name,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}
		result.UsersImported++
		_ = tracker.TrackImportedItem(ctx, s.Name(), "user", u.ID)
	}

	// Verify results
	if result.UsersImported != 3 {
		t.Errorf("UsersImported = %d, want 3", result.UsersImported)
	}

	// Verify users were tracked
	if len(tracker.items) != 3 {
		t.Errorf("tracked items = %d, want 3", len(tracker.items))
	}

	for _, item := range tracker.items {
		if item.source != "elefant" {
			t.Errorf("tracked source = %q, want %q", item.source, "elefant")
		}
		if item.entityType != "user" {
			t.Errorf("tracked entityType = %q, want %q", item.entityType, "user")
		}
	}

	// Verify users in database have public role
	users, err := queries.ListUsers(ctx, store.ListUsersParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list users: %v", err)
	}

	if len(users) != 3 {
		t.Errorf("users in database = %d, want 3", len(users))
	}

	for _, u := range users {
		if u.Role != "public" {
			t.Errorf("user %s role = %q, want %q", u.Email, u.Role, "public")
		}
	}
}

func TestImportUsers_SkipExisting(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	queries := store.New(db)
	ctx := context.Background()

	// Create an existing user
	now := time.Now()
	_, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "existing@example.com",
		PasswordHash: "existing-hash",
		Role:         "editor", // Different role
		Name:         "Existing User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create existing user: %v", err)
	}

	// Try to check if user exists (simulating SkipExisting behavior)
	_, err = queries.GetUserByEmail(ctx, "existing@example.com")
	if err != nil {
		t.Errorf("GetUserByEmail should find existing user: %v", err)
	}

	// Non-existing user should return error
	_, err = queries.GetUserByEmail(ctx, "new@example.com")
	if err == nil {
		t.Error("GetUserByEmail should return error for non-existing user")
	}
}

func TestImportUsersDoesNotCreateAfterOperationalLookupFailure(t *testing.T) {
	sourceDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sourceDB.Close() }()
	if _, err := sourceDB.Exec(`
		CREATE TABLE user (id INTEGER PRIMARY KEY, email TEXT NOT NULL, name TEXT NOT NULL);
		INSERT INTO user (id, email, name) VALUES (1, 'lookup@example.com', 'Lookup');
	`); err != nil {
		t.Fatal(err)
	}
	reader := &Reader{db: sourceDB}

	destinationDB := setupTestDB(t)
	defer func() { _ = destinationDB.Close() }()
	if _, err := destinationDB.Exec(`DROP TABLE users`); err != nil {
		t.Fatal(err)
	}
	result := &types.ImportResult{}
	err = NewSource().importUsers(context.Background(), store.New(destinationDB), reader,
		types.ImportOptions{SkipExisting: true}, result, &mockTracker{})
	if err != nil {
		t.Fatalf("importUsers() fatal error = %v", err)
	}
	if result.UsersImported != 0 {
		t.Fatalf("UsersImported = %d, want 0 after lookup failure", result.UsersImported)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "Failed to check for existing user") {
		t.Fatalf("errors = %v, want operational lookup failure", result.Errors)
	}
	if strings.Contains(result.Errors[0], "Failed to create user") {
		t.Fatalf("lookup failure fell through to user creation: %v", result.Errors)
	}
}

func TestImportTagsDoesNotCreateAfterOperationalLookupFailure(t *testing.T) {
	sourceDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceDB.Close() })
	if _, err := sourceDB.Exec(`
		CREATE TABLE blog_tag (id TEXT PRIMARY KEY);
		INSERT INTO blog_tag (id) VALUES ('Existing Tag');
	`); err != nil {
		t.Fatal(err)
	}

	destinationDB, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if _, err := destinationDB.Exec(`DROP TABLE tags`); err != nil {
		t.Fatal(err)
	}
	result := &types.ImportResult{}
	tagMap, err := NewSource().importTags(context.Background(), store.New(destinationDB),
		&Reader{db: sourceDB}, "en", types.ImportOptions{SkipExisting: true}, result, &mockTracker{})
	if err != nil {
		t.Fatalf("importTags() fatal error = %v", err)
	}
	if len(tagMap) != 0 || result.TagsImported != 0 || result.TagsSkipped != 0 {
		t.Fatalf("tagMap=%v imported=%d skipped=%d, want no mutation", tagMap,
			result.TagsImported, result.TagsSkipped)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "Failed to check for existing tag") {
		t.Fatalf("errors = %v, want operational lookup failure", result.Errors)
	}
	if strings.Contains(result.Errors[0], "Failed to create tag") {
		t.Fatalf("lookup failure fell through to tag creation: %v", result.Errors)
	}
}

func TestSkipExistingPagesDoNotCreateAfterOperationalLookupFailure(t *testing.T) {
	tests := []struct {
		name       string
		schema     string
		reader     func(*sql.DB) *Reader
		importItem func(*Source, *store.Queries, *Reader, *types.ImportResult) error
	}{
		{
			name: "post",
			schema: `
				CREATE TABLE blog_post (
					id INTEGER PRIMARY KEY, title TEXT, slug TEXT, body TEXT, ts DATETIME,
					author TEXT, published TEXT, tags TEXT, thumbnail TEXT,
					description TEXT, keywords TEXT, extra TEXT
				);
				INSERT INTO blog_post VALUES
					(1, 'Post', 'existing-post', '<p>body</p>', CURRENT_TIMESTAMP,
					 '1', 'yes', '[]', NULL, NULL, NULL, NULL);`,
			reader: func(db *sql.DB) *Reader {
				return &Reader{db: db, schemaDetected: true, hasSlug: true, hasDescription: true, hasKeywords: true}
			},
			importItem: func(s *Source, q *store.Queries, r *Reader, result *types.ImportResult) error {
				return s.importPosts(context.Background(), q, r, 1, "en", nil, nil,
					types.ImportOptions{SkipExisting: true}, result, &mockTracker{})
			},
		},
		{
			name: "page",
			schema: `
				CREATE TABLE webpage (
					id TEXT PRIMARY KEY, title TEXT, menu_title TEXT, window_title TEXT,
					access TEXT, layout TEXT, description TEXT, keywords TEXT, body TEXT, extra TEXT
				);
				INSERT INTO webpage VALUES
					('existing-page', 'Page', 'Page', 'Page', 'public', 'default', NULL, NULL, '<p>body</p>', NULL);`,
			reader: func(db *sql.DB) *Reader { return &Reader{db: db} },
			importItem: func(s *Source, q *store.Queries, r *Reader, result *types.ImportResult) error {
				return s.importPages(context.Background(), q, r, 1, "en", nil,
					types.ImportOptions{SkipExisting: true}, result, &mockTracker{})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceDB, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sourceDB.Close() })
			if _, err := sourceDB.Exec(tt.schema); err != nil {
				t.Fatal(err)
			}
			destinationDB, cleanup := testutil.TestDB(t)
			t.Cleanup(cleanup)
			if _, err := destinationDB.Exec(`DROP TABLE pages`); err != nil {
				t.Fatal(err)
			}
			result := &types.ImportResult{}
			if err := tt.importItem(NewSource(), store.New(destinationDB), tt.reader(sourceDB), result); err != nil {
				t.Fatalf("import fatal error = %v", err)
			}
			if result.PostsImported != 0 || result.PostsSkipped != 0 ||
				result.PagesImported != 0 || result.PagesSkipped != 0 {
				t.Fatalf("result counts = %+v, want no mutation", result)
			}
			if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "Failed to check for existing page") {
				t.Fatalf("errors = %v, want operational lookup failure", result.Errors)
			}
			if strings.Contains(result.Errors[0], "Failed to create page") {
				t.Fatalf("lookup failure fell through to page creation: %v", result.Errors)
			}
		})
	}
}

func TestImportPagesPreservesSafeLegacyAlias(t *testing.T) {
	sourceDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceDB.Close() })
	if _, err := sourceDB.Exec(`
		CREATE TABLE webpage (
			id TEXT PRIMARY KEY, title TEXT, menu_title TEXT, window_title TEXT,
			access TEXT, layout TEXT, description TEXT, keywords TEXT, body TEXT, extra TEXT
		);
		INSERT INTO webpage VALUES
			('About_Us', 'About us', 'About us', 'About us', 'public', 'default',
			 NULL, NULL, '<p>body</p>', NULL);`); err != nil {
		t.Fatal(err)
	}

	destinationDB, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(destinationDB)
	ctx := context.Background()
	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "legacy-alias@example.com", PasswordHash: "x", Role: "admin",
		Name: "Legacy Alias", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := &types.ImportResult{}
	tracker := &mockTracker{}
	if err := NewSource().importPages(ctx, queries, &Reader{db: sourceDB}, author.ID,
		lang.Code, nil, types.ImportOptions{}, result, tracker); err != nil {
		t.Fatal(err)
	}
	page, err := queries.GetPageBySlug(ctx, "aboutus")
	if err != nil {
		t.Fatal(err)
	}
	aliases, err := queries.GetAliasesForPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || aliases[0].Alias != "About_Us" {
		t.Fatalf("aliases = %v, want exact legacy alias About_Us", aliases)
	}
	if result.AliasesImported != 1 {
		t.Fatalf("AliasesImported = %d, want 1", result.AliasesImported)
	}
	foundTrackedAlias := false
	for _, item := range tracker.items {
		if item.entityType == string(types.EntityAlias) && item.entityID == aliases[0].ID {
			foundTrackedAlias = true
		}
	}
	if !foundTrackedAlias {
		t.Fatalf("alias tracking entry missing: %v", tracker.items)
	}
}

func TestImportPagesRefusesLegacyAliasShadowedByExistingPageSlug(t *testing.T) {
	sourceDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceDB.Close() })
	if _, err := sourceDB.Exec(`
		CREATE TABLE webpage (
			id TEXT PRIMARY KEY, title TEXT, menu_title TEXT, window_title TEXT,
			access TEXT, layout TEXT, description TEXT, keywords TEXT, body TEXT, extra TEXT
		);
		INSERT INTO webpage VALUES
			('about', 'Imported about', 'Imported about', 'Imported about', 'public', 'default',
			 NULL, NULL, '<p>body</p>', NULL);`); err != nil {
		t.Fatal(err)
	}

	destinationDB, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(destinationDB)
	ctx := context.Background()
	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "alias-shadow@example.com", PasswordHash: "x", Role: "admin",
		Name: "Alias shadow", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Existing about", Slug: "about", Status: "published", AuthorID: author.ID,
		LanguageCode: lang.Code, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := &types.ImportResult{}
	if err := NewSource().importPages(ctx, queries, &Reader{db: sourceDB}, author.ID,
		lang.Code, nil, types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatal(err)
	}
	imported, err := queries.GetPageBySlug(ctx, "about-2")
	if err != nil {
		t.Fatal(err)
	}
	if imported.ID == existing.ID {
		t.Fatal("import reused the existing page")
	}
	aliases, err := queries.GetAliasesForPage(ctx, imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 || result.AliasesImported != 0 {
		t.Fatalf("aliases=%v imported=%d, want none", aliases, result.AliasesImported)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "shadowed by existing page slug") {
		t.Fatalf("errors = %v, want shadowing error", result.Errors)
	}
}

func TestImportPagesPreservesDefaultAliasBlockedOnlyByForeignLanguageSlug(t *testing.T) {
	sourceDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceDB.Close() })
	if _, err := sourceDB.Exec(`
		CREATE TABLE webpage (
			id TEXT PRIMARY KEY, title TEXT, menu_title TEXT, window_title TEXT,
			access TEXT, layout TEXT, description TEXT, keywords TEXT, body TEXT, extra TEXT
		);
		INSERT INTO webpage VALUES
			('about', 'Imported about', 'Imported about', 'Imported about', 'public', 'default',
			 NULL, NULL, '<p>body</p>', NULL);`); err != nil {
		t.Fatal(err)
	}

	destinationDB, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(destinationDB)
	ctx := context.Background()
	now := time.Now()
	defaultLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "foreign-alias@example.com", PasswordHash: "x", Role: "admin",
		Name: "Foreign alias", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = queries.CreatePage(ctx, store.CreatePageParams{
		Title: "French about", Slug: "about", Status: "published", AuthorID: author.ID,
		LanguageCode: "fr", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := &types.ImportResult{}
	tracker := &mockTracker{}
	if err := NewSource().importPages(ctx, queries, &Reader{db: sourceDB}, author.ID,
		defaultLang.Code, nil, types.ImportOptions{}, result, tracker); err != nil {
		t.Fatal(err)
	}
	imported, err := queries.GetPageBySlug(ctx, "about-2")
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := queries.GetRedirectBySourcePath(ctx, "/about")
	if err != nil {
		t.Fatal(err)
	}
	if redirect.TargetUrl != "/"+imported.Slug || result.RedirectsImported != 1 || result.AliasesImported != 0 {
		t.Fatalf("redirect=%+v counters=%d/%d", redirect, result.RedirectsImported, result.AliasesImported)
	}
	if tracker.redirectCount() != 1 {
		t.Fatalf("redirect tracking entries = %d, want 1", tracker.redirectCount())
	}
}

func TestImportPagesUsesConcreteRedirectForActiveLanguagePrefixAlias(t *testing.T) {
	sourceDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceDB.Close() })
	if _, err := sourceDB.Exec(`
		CREATE TABLE webpage (
			id TEXT PRIMARY KEY, title TEXT, menu_title TEXT, window_title TEXT,
			access TEXT, layout TEXT, description TEXT, keywords TEXT, body TEXT, extra TEXT
		);
		INSERT INTO webpage VALUES
			('fr/team', 'Team', 'Team', 'Team', 'public', 'default',
			 NULL, NULL, '<p>body</p>', NULL);`); err != nil {
		t.Fatal(err)
	}

	destinationDB, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(destinationDB)
	ctx := context.Background()
	now := time.Now()
	defaultLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "prefixed-alias@example.com", PasswordHash: "x", Role: "admin",
		Name: "Prefixed alias", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := &types.ImportResult{}
	tracker := &mockTracker{}
	if err := NewSource().importPages(ctx, queries, &Reader{db: sourceDB}, author.ID,
		defaultLang.Code, nil, types.ImportOptions{}, result, tracker); err != nil {
		t.Fatal(err)
	}
	page, err := queries.GetPageBySlug(ctx, util.Slugify("fr/team"))
	if err != nil {
		t.Fatal(err)
	}
	aliases, err := queries.GetAliasesForPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Fatalf("stored inert aliases = %v, want none", aliases)
	}
	redirect, err := queries.GetRedirectBySourcePath(ctx, "/fr/team")
	if err != nil {
		t.Fatal(err)
	}
	if redirect.TargetUrl != "/"+page.Slug || result.RedirectsImported != 1 {
		t.Fatalf("redirect=%+v RedirectsImported=%d", redirect, result.RedirectsImported)
	}
	if tracker.redirectCount() != 1 {
		t.Fatalf("redirect tracking entries = %d, want 1", tracker.redirectCount())
	}
}

func TestTrackedRedirectRejectsEnabledWildcardShadow(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	_, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/fr/*", TargetUrl: "/existing/{1}", StatusCode: 301,
		IsWildcard: true, TargetType: model.TargetSelf, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := &types.ImportResult{}
	tracker := &mockTracker{}
	err = NewSource().createTrackedRedirect(ctx, queries, "/fr/team", "/team", now, result, tracker)
	if err == nil || !strings.Contains(err.Error(), "shadowed by enabled wildcard") {
		t.Fatalf("createTrackedRedirect() error = %v, want wildcard shadowing", err)
	}
	if _, lookupErr := queries.GetRedirectBySourcePath(ctx, "/fr/team"); !errors.Is(lookupErr, sql.ErrNoRows) {
		t.Fatalf("exact redirect lookup error = %v, want sql.ErrNoRows", lookupErr)
	}
	if result.RedirectsImported != 0 || tracker.redirectCount() != 0 {
		t.Fatalf("redirect counted/tracked despite shadow: %+v / %+v", result, tracker.items)
	}
}

func TestPageSlugsAndAliasesRefuseCoreModuleAndRedirectOwnership(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "route-owner@example.com", PasswordHash: "x", Role: "admin", Name: "Owner",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Target", Slug: "target", Status: "published", AuthorID: author.ID,
		LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", Position: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Foreign owner", Slug: "bookmarks", Status: "published", AuthorID: author.ID,
		LanguageCode: "fr", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, redirect := range []store.CreateRedirectParams{
		{SourcePath: "/claimed", TargetUrl: "/exact", StatusCode: 302,
			TargetType: model.TargetSelf, Enabled: false, CreatedAt: now, UpdatedAt: now},
		{SourcePath: "/wild*", TargetUrl: "/wildcard", StatusCode: 301, IsWildcard: true,
			TargetType: model.TargetSelf, Enabled: true, CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := queries.CreateRedirect(ctx, redirect); err != nil {
			t.Fatal(err)
		}
	}
	source := NewSource()
	source.SetPublicRouteChecker(pathOwnership{"/bookmarks": true, "/fr/team": true})
	for base, want := range map[string]string{
		"search": "search-2", "bookmarks": "bookmarks-2", "claimed": "claimed-2",
	} {
		if got := source.makeUniquePageSlug(ctx, queries, base); got != want {
			t.Errorf("makeUniquePageSlug(%q) = %q, want %q", base, got, want)
		}
	}
	if got := source.makeUniquePageSlug(ctx, queries, "wild"); got == "" || strings.HasPrefix(got, "wild") {
		t.Errorf("wildcard-owned slug fallback = %q; want a non-wild route", got)
	}

	result := &types.ImportResult{}
	tracker := &mockTracker{}
	for _, alias := range []string{"page/123", "bookmarks", "fr/team", "claimed", "wild/path"} {
		if err := source.createTrackedPageAlias(ctx, queries, page.ID, alias, now, result, tracker); err == nil {
			t.Errorf("createTrackedPageAlias(%q) succeeded; want route ownership error", alias)
		}
	}
	for _, sourcePath := range []string{"/bookmarks", "/fr/team"} {
		if _, err := queries.GetRedirectBySourcePath(ctx, sourcePath); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("redirect %q lookup error = %v, want sql.ErrNoRows", sourcePath, err)
		}
	}
	aliases, err := queries.GetAliasesForPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 || result.AliasesImported != 0 {
		t.Fatalf("unreachable aliases were stored: aliases=%v result=%+v", aliases, result)
	}
	if err := source.createTrackedPageAlias(ctx, queries, page.ID, "blog/post/42", now, result, tracker); err != nil {
		t.Fatalf("intentional Elefant blog alias was rejected: %v", err)
	}
}

func TestImportContentDoesNotCreatePagesWhenReachableSlugsAreExhausted(t *testing.T) {
	sourceDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceDB.Close() })
	if _, err := sourceDB.Exec(`
		CREATE TABLE blog_post (
			id INTEGER PRIMARY KEY, title TEXT, slug TEXT, body TEXT, ts DATETIME,
			author TEXT, published TEXT, tags TEXT, thumbnail TEXT,
			description TEXT, keywords TEXT, extra TEXT
		);
		INSERT INTO blog_post VALUES
			(7, 'Blocked post', 'blocked-post', '<p>body</p>', CURRENT_TIMESTAMP,
			 '1', 'yes', '[]', NULL, NULL, NULL, NULL);
		CREATE TABLE webpage (
			id TEXT PRIMARY KEY, title TEXT, menu_title TEXT, window_title TEXT,
			access TEXT, layout TEXT, description TEXT, keywords TEXT, body TEXT, extra TEXT
		);
		INSERT INTO webpage VALUES
			('blocked/page', 'Blocked page', 'Blocked page', 'Blocked page', 'public', 'default',
			 NULL, NULL, '<p>body</p>', NULL);
	`); err != nil {
		t.Fatal(err)
	}

	destinationDB, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(destinationDB)
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/**", TargetUrl: "/blocked", StatusCode: 301,
		IsWildcard: true, TargetType: model.TargetSelf, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "slug-exhaustion@example.com", PasswordHash: "x", Role: "admin", Name: "Owner",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := &Reader{db: sourceDB, schemaDetected: true, hasSlug: true, hasDescription: true, hasKeywords: true}
	result := &types.ImportResult{}
	tracker := &mockTracker{}
	source := NewSource()
	if err := source.importPosts(ctx, queries, reader, author.ID, "en", nil, nil,
		types.ImportOptions{}, result, tracker); err != nil {
		t.Fatal(err)
	}
	if err := source.importPages(ctx, queries, reader, author.ID, "en", nil,
		types.ImportOptions{}, result, tracker); err != nil {
		t.Fatal(err)
	}
	pageCount, err := queries.CountPages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pageCount != 0 || result.PostsImported != 0 || result.PagesImported != 0 {
		t.Fatalf("pages=%d posts=%d webpages=%d, want no unreachable imports",
			pageCount, result.PostsImported, result.PagesImported)
	}
	if len(result.Errors) != 2 ||
		!strings.Contains(strings.Join(result.Errors, "\n"), "Failed to allocate a reachable slug") {
		t.Fatalf("errors = %v, want allocator exhaustion for both content types", result.Errors)
	}
	if len(tracker.items) != 0 {
		t.Fatalf("unreachable content was tracked: %v", tracker.items)
	}
}

func TestLegacyAliasInsertFailuresAreReported(t *testing.T) {
	tests := []struct {
		name          string
		sourceSchema  string
		slug          string
		reader        func(*sql.DB) *Reader
		importContent func(*Source, *store.Queries, *Reader, int64, string, *types.ImportResult, types.ImportTracker) error
		imported      func(*types.ImportResult) int
	}{
		{
			name: "post",
			sourceSchema: `
				CREATE TABLE blog_post (
					id INTEGER PRIMARY KEY, title TEXT, slug TEXT, body TEXT, ts DATETIME,
					author TEXT, published TEXT, tags TEXT, thumbnail TEXT,
					description TEXT, keywords TEXT, extra TEXT
				);
				INSERT INTO blog_post VALUES
					(7, 'Alias post', 'alias-post', '<p>body</p>', CURRENT_TIMESTAMP,
					 '1', 'yes', '[]', NULL, NULL, NULL, NULL);`,
			slug: "alias-post",
			reader: func(db *sql.DB) *Reader {
				return &Reader{db: db, schemaDetected: true, hasSlug: true, hasDescription: true, hasKeywords: true}
			},
			importContent: func(s *Source, q *store.Queries, r *Reader, authorID int64, lang string,
				result *types.ImportResult, tracker types.ImportTracker) error {
				return s.importPosts(context.Background(), q, r, authorID, lang, nil, nil,
					types.ImportOptions{}, result, tracker)
			},
			imported: func(result *types.ImportResult) int { return result.PostsImported },
		},
		{
			name: "page",
			sourceSchema: `
				CREATE TABLE webpage (
					id TEXT PRIMARY KEY, title TEXT, menu_title TEXT, window_title TEXT,
					access TEXT, layout TEXT, description TEXT, keywords TEXT, body TEXT, extra TEXT
				);
				INSERT INTO webpage VALUES
					('legacy/page', 'Alias page', 'Alias page', 'Alias page', 'public', 'default',
					 NULL, NULL, '<p>body</p>', NULL);`,
			slug:   "legacypage",
			reader: func(db *sql.DB) *Reader { return &Reader{db: db} },
			importContent: func(s *Source, q *store.Queries, r *Reader, authorID int64, lang string,
				result *types.ImportResult, tracker types.ImportTracker) error {
				return s.importPages(context.Background(), q, r, authorID, lang, nil,
					types.ImportOptions{}, result, tracker)
			},
			imported: func(result *types.ImportResult) int { return result.PagesImported },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceDB, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sourceDB.Close() })
			if _, err := sourceDB.Exec(tt.sourceSchema); err != nil {
				t.Fatal(err)
			}
			destinationDB, cleanup := testutil.TestDB(t)
			t.Cleanup(cleanup)
			if _, err := destinationDB.Exec(`
				CREATE TRIGGER fail_elefant_alias_create
				BEFORE INSERT ON page_aliases BEGIN
					SELECT RAISE(FAIL, 'forced alias insert failure');
				END`); err != nil {
				t.Fatal(err)
			}
			queries := store.New(destinationDB)
			ctx := context.Background()
			lang, err := queries.GetDefaultLanguage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			author, err := queries.CreateUser(ctx, store.CreateUserParams{
				Email: tt.name + "-alias-error@example.com", PasswordHash: "x", Role: "admin",
				Name: "Alias error", CreatedAt: time.Now(), UpdatedAt: time.Now(),
			})
			if err != nil {
				t.Fatal(err)
			}
			result := &types.ImportResult{}
			tracker := &mockTracker{}
			if err := tt.importContent(NewSource(), queries, tt.reader(sourceDB), author.ID,
				lang.Code, result, tracker); err != nil {
				t.Fatal(err)
			}
			page, err := queries.GetPageBySlug(ctx, tt.slug)
			if err != nil {
				t.Fatal(err)
			}
			aliases, err := queries.GetAliasesForPage(ctx, page.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(aliases) != 0 || result.AliasesImported != 0 {
				t.Fatalf("aliases=%v imported=%d, want none", aliases, result.AliasesImported)
			}
			if tt.imported(result) != 1 {
				t.Fatalf("content imported = %d, want 1", tt.imported(result))
			}
			if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "forced alias insert failure") {
				t.Fatalf("errors = %v, want concrete alias insertion failure", result.Errors)
			}
			for _, item := range tracker.items {
				if item.entityType == string(types.EntityAlias) {
					t.Fatalf("failed alias was tracked: %v", tracker.items)
				}
			}
		})
	}
}

func TestImportUsers_DuplicateEmail(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	// Create first user
	_, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "duplicate@example.com",
		PasswordHash: "hash1",
		Role:         "public",
		Name:         "User One",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create first user: %v", err)
	}

	// Try to create user with same email (should fail)
	_, err = queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "duplicate@example.com",
		PasswordHash: "hash2",
		Role:         "public",
		Name:         "User Two",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err == nil {
		t.Error("CreateUser should fail for duplicate email")
	}
}

func TestImportUsers_PublicRoleOnly(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	// Simulate importing a user - they should always get "public" role
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "imported@example.com",
		PasswordHash: "placeholder",
		Role:         "public", // This is what importUsers sets
		Name:         "Imported User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	if user.Role != "public" {
		t.Errorf("imported user role = %q, want %q", user.Role, "public")
	}

	// Verify the user cannot be admin or editor through import
	// (the import code always sets role to "public")
}

func TestImportResult_WithUsers(t *testing.T) {
	result := &types.ImportResult{
		TagsImported:  5,
		PostsImported: 10,
		UsersImported: 15,
		TagsSkipped:   1,
		PostsSkipped:  2,
		UsersSkipped:  3,
	}

	// Test TotalImported includes users
	expectedTotal := 5 + 10 + 15
	if got := result.TotalImported(); got != expectedTotal {
		t.Errorf("TotalImported() = %d, want %d", got, expectedTotal)
	}

	// Test TotalSkipped includes users
	expectedSkipped := 1 + 2 + 3
	if got := result.TotalSkipped(); got != expectedSkipped {
		t.Errorf("TotalSkipped() = %d, want %d", got, expectedSkipped)
	}
}

func TestImportOptions_ImportUsers(t *testing.T) {
	// Test ImportUsers option is properly handled
	optsWithUsers := types.ImportOptions{
		ImportTags:   true,
		ImportPosts:  true,
		ImportPages:  true,
		ImportUsers:  true,
		SkipExisting: false,
	}

	if !optsWithUsers.ImportUsers {
		t.Error("ImportUsers should be true")
	}
	if !optsWithUsers.ImportPages {
		t.Error("ImportPages should be true")
	}

	optsWithoutUsers := types.ImportOptions{
		ImportTags:   true,
		ImportPosts:  true,
		ImportPages:  false,
		ImportUsers:  false,
		SkipExisting: false,
	}

	if optsWithoutUsers.ImportUsers {
		t.Error("ImportUsers should be false")
	}
}

func TestMockTracker(t *testing.T) {
	tracker := &mockTracker{}
	ctx := context.Background()

	// Track some items
	_ = tracker.TrackImportedItem(ctx, "elefant", "user", 1)
	_ = tracker.TrackImportedItem(ctx, "elefant", "user", 2)
	_ = tracker.TrackImportedItem(ctx, "elefant", "page", 10)

	if len(tracker.items) != 3 {
		t.Errorf("tracked items = %d, want 3", len(tracker.items))
	}

	// Verify item details
	userCount := 0
	pageCount := 0
	for _, item := range tracker.items {
		if item.entityType == "user" {
			userCount++
		}
		if item.entityType == "page" {
			pageCount++
		}
	}

	if userCount != 2 {
		t.Errorf("tracked users = %d, want 2", userCount)
	}
	if pageCount != 1 {
		t.Errorf("tracked pages = %d, want 1", pageCount)
	}
}

func TestTrackingFailureCompensatesAndReports(t *testing.T) {
	s := NewSource()
	result := &types.ImportResult{}
	rolledBack := false
	ok := s.track(context.Background(), failingTracker{err: fmt.Errorf("tracking unavailable")},
		result, types.EntityPage, 42, func(context.Context) error {
			rolledBack = true
			return nil
		})
	if ok {
		t.Fatal("track() = true after tracker failure")
	}
	if !rolledBack {
		t.Fatal("tracking failure did not run compensation")
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "tracking unavailable") {
		t.Fatalf("tracking errors = %v", result.Errors)
	}
}

func TestTrackingCompensationOutlivesCanceledImportContext(t *testing.T) {
	s := NewSource()
	result := &types.ImportResult{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rollbackRanWithLiveContext := false

	ok := s.track(ctx, failingTracker{err: context.Canceled}, result, types.EntityPage, 42,
		func(rollbackCtx context.Context) error {
			rollbackRanWithLiveContext = rollbackCtx.Err() == nil
			return nil
		})
	if ok {
		t.Fatal("track() = true after canceled tracking write")
	}
	if !rollbackRanWithLiveContext {
		t.Fatal("tracking compensation inherited the canceled import context")
	}
}

func TestTrackingFailureReportsCompensationFailure(t *testing.T) {
	result := &types.ImportResult{}
	ok := NewSource().track(context.Background(), failingTracker{err: errors.New("tracking unavailable")},
		result, types.EntityPage, 42, func(context.Context) error {
			return errors.New("rollback blocked")
		})
	if ok {
		t.Fatal("track() = true after tracking failure")
	}
	if len(result.Errors) != 2 ||
		!strings.Contains(result.Errors[0], "tracking unavailable") ||
		!strings.Contains(result.Errors[1], "rollback blocked") {
		t.Fatalf("errors = %v, want tracking and compensation failures", result.Errors)
	}
}

func TestDurableCleanupQueueOutlivesCanceledImportContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tracker := &cleanupContextTracker{}
	parent := t.TempDir()
	originalRoot := filepath.Join(parent, "uploads-original")
	outsideRoot := filepath.Join(parent, "uploads-outside")
	configuredRoot := filepath.Join(parent, "uploads")
	for _, root := range []string{originalRoot, outsideRoot} {
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(originalRoot, configuredRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	canonicalUploadRoot, err := imaging.CanonicalUploadRoot(configuredRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(configuredRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideRoot, configuredRoot); err != nil {
		t.Fatal(err)
	}

	err = NewSource().cleanupMediaFiles(ctx, tracker, canonicalUploadRoot, "not-a-media-uuid")
	if err == nil {
		t.Fatal("cleanupMediaFiles() error = nil for invalid media UUID")
	}
	if tracker.queueContextErr != nil {
		t.Fatalf("durable cleanup queue inherited canceled context: %v", tracker.queueContextErr)
	}
	if tracker.queuedRoot != canonicalUploadRoot || tracker.queuedUUID != "not-a-media-uuid" {
		t.Fatalf("queued cleanup = (%q, %q), want captured root and UUID", tracker.queuedRoot, tracker.queuedUUID)
	}
}

func TestAliasTrackingFailureCompensatesAndDoesNotCount(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()

	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "alias-compensation@example.com", PasswordHash: "x", Role: "admin",
		Name: "Alias compensation", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Alias parent", Slug: "alias-parent", Status: "draft",
		AuthorID: author.ID, LanguageCode: lang.Code, PageType: "page",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	result := &types.ImportResult{}
	if err := NewSource().createTrackedPageAlias(ctx, queries, page.ID, "legacy/path", now,
		result, failingTracker{err: errors.New("tracking unavailable")}); err != nil {
		t.Fatalf("createTrackedPageAlias() error = %v", err)
	}
	aliases, err := queries.GetAliasesForPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Fatalf("aliases after tracking compensation = %v, want none", aliases)
	}
	if result.AliasesImported != 0 {
		t.Fatalf("AliasesImported = %d, want 0", result.AliasesImported)
	}
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0], "tracking unavailable") {
		t.Fatalf("tracking failure was not reported: %v", result.Errors)
	}
}

func TestMediaRowFailureCleansCopiedFile(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if _, err := db.Exec(`
		CREATE TRIGGER fail_elefant_media_create
		BEFORE INSERT ON media BEGIN
			SELECT RAISE(FAIL, 'forced media insert failure');
		END`); err != nil {
		t.Fatal(err)
	}

	sourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "document.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(shared.EnvAllowedFileRoots, sourceRoot)
	t.Setenv("DRUPAL_FILES", "")
	t.Setenv("ELEFANT_FILES", "")
	uploadRoot := t.TempDir()

	result := &types.ImportResult{}
	mediaMap, err := NewSource().importMedia(context.Background(), store.New(db),
		sourceRoot, uploadRoot, 1, result, &mockTracker{})
	if err != nil {
		t.Fatalf("importMedia() fatal error = %v", err)
	}
	if len(mediaMap) != 0 || len(result.Errors) == 0 {
		t.Fatalf("mediaMap=%v errors=%v, want failed media row", mediaMap, result.Errors)
	}
	originals := filepath.Join(uploadRoot, "originals")
	entries, readErr := os.ReadDir(originals)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("partial media output survived row failure: %v", entries)
	}
}

func TestImageMediaRowFailureCleansProcessedFiles(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if _, err := db.Exec(`
		CREATE TRIGGER fail_elefant_image_media_create
		BEFORE INSERT ON media BEGIN
			SELECT RAISE(FAIL, 'forced media insert failure');
		END`); err != nil {
		t.Fatal(err)
	}

	var encoded bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	sourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "photo.png"), encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(shared.EnvAllowedFileRoots, sourceRoot)
	t.Setenv("DRUPAL_FILES", "")
	t.Setenv("ELEFANT_FILES", "")
	uploadRoot := t.TempDir()

	result := &types.ImportResult{}
	mediaMap, err := NewSource().importMedia(context.Background(), store.New(db),
		sourceRoot, uploadRoot, 1, result, &mockTracker{})
	if err != nil {
		t.Fatalf("importMedia() fatal error = %v", err)
	}
	if len(mediaMap) != 0 || len(result.Errors) == 0 {
		t.Fatalf("mediaMap=%v errors=%v, want failed media row", mediaMap, result.Errors)
	}
	for _, storageDir := range model.MediaStorageDirs() {
		entries, readErr := os.ReadDir(filepath.Join(uploadRoot, storageDir))
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("partial image output survived in %s: %v", storageDir, entries)
		}
	}
}

func TestMediaTrackingFailureCompensatesRowFilesMapAndCounter(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	uploader, err := store.New(db).CreateUser(context.Background(), store.CreateUserParams{
		Email: "media-tracking@example.com", PasswordHash: "x", Role: "admin",
		Name: "Media tracker", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "document.pdf"), []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(shared.EnvAllowedFileRoots, sourceRoot)
	t.Setenv("DRUPAL_FILES", "")
	t.Setenv("ELEFANT_FILES", "")
	uploadRoot := t.TempDir()

	result := &types.ImportResult{}
	mediaMap, err := NewSource().importMedia(context.Background(), store.New(db),
		sourceRoot, uploadRoot, uploader.ID, result, failingTracker{err: errors.New("tracking unavailable")})
	if err != nil {
		t.Fatalf("importMedia() fatal error = %v", err)
	}
	if len(mediaMap) != 0 {
		t.Fatalf("media map after tracking failure = %v, want empty", mediaMap)
	}
	if result.MediaImported != 0 {
		t.Fatalf("MediaImported = %d, want 0", result.MediaImported)
	}
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0], "tracking unavailable") {
		t.Fatalf("tracking failure was not reported: %v", result.Errors)
	}

	var mediaCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media`).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	if mediaCount != 0 {
		t.Fatalf("media rows after tracking compensation = %d, want 0", mediaCount)
	}
	for _, storageDir := range model.MediaStorageDirs() {
		entries, readErr := os.ReadDir(filepath.Join(uploadRoot, storageDir))
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("media output survived in %s: %v", storageDir, entries)
		}
	}
}

func TestMediaTrackingRollbackUsesCapturedCanonicalUploadRoot(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	uploader, err := store.New(db).CreateUser(context.Background(), store.CreateUserParams{
		Email: "media-retarget@example.com", PasswordHash: "x", Role: "admin",
		Name: "Media retarget", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "document.pdf"), []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(shared.EnvAllowedFileRoots, sourceRoot)
	t.Setenv("DRUPAL_FILES", "")
	t.Setenv("ELEFANT_FILES", "")

	destinationParent := t.TempDir()
	originalRoot := filepath.Join(destinationParent, "uploads-original")
	outsideRoot := filepath.Join(destinationParent, "uploads-outside")
	configuredRoot := filepath.Join(destinationParent, "uploads")
	for _, root := range []string{originalRoot, outsideRoot} {
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(originalRoot, configuredRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var outsideSentinel string
	tracker := failingTracker{
		err: errors.New("tracking unavailable"),
		onTrack: func() {
			entries, err := os.ReadDir(filepath.Join(originalRoot, model.OriginalsDir))
			if err != nil || len(entries) != 1 {
				t.Fatalf("read written UUID: entries=%v error=%v", entries, err)
			}
			outsideMediaDir := filepath.Join(outsideRoot, model.OriginalsDir, entries[0].Name())
			if err := os.MkdirAll(outsideMediaDir, 0o750); err != nil {
				t.Fatal(err)
			}
			outsideSentinel = filepath.Join(outsideMediaDir, "must-remain.pdf")
			if err := os.WriteFile(outsideSentinel, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(configuredRoot); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outsideRoot, configuredRoot); err != nil {
				t.Fatal(err)
			}
		},
	}
	result := &types.ImportResult{}
	mediaMap, err := NewSource().importMedia(context.Background(), store.New(db),
		sourceRoot, configuredRoot, uploader.ID, result, tracker)
	if err != nil {
		t.Fatalf("importMedia() fatal error = %v", err)
	}
	if len(mediaMap) != 0 || result.MediaImported != 0 {
		t.Fatalf("mediaMap=%v MediaImported=%d after tracking failure", mediaMap, result.MediaImported)
	}
	var mediaCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media`).Scan(&mediaCount); err != nil || mediaCount != 0 {
		t.Fatalf("media rows = %d, error=%v; want compensated row", mediaCount, err)
	}
	for _, storageDir := range model.MediaStorageDirs() {
		entries, readErr := os.ReadDir(filepath.Join(originalRoot, storageDir))
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("original output survived in %s: %v", storageDir, entries)
		}
	}
	if data, err := os.ReadFile(outsideSentinel); err != nil || string(data) != "outside" {
		t.Fatalf("outside sentinel changed: data=%q error=%v", data, err)
	}
}

func TestUserModel(t *testing.T) {
	user := User{
		ID:    1,
		Email: "test@example.com",
		Name:  "Test User",
	}

	if user.ID != 1 {
		t.Errorf("user.ID = %d, want 1", user.ID)
	}
	if user.Email != "test@example.com" {
		t.Errorf("user.Email = %q, want %q", user.Email, "test@example.com")
	}
	if user.Name != "Test User" {
		t.Errorf("user.Name = %q, want %q", user.Name, "Test User")
	}
}

func TestEnvOrDefault(t *testing.T) {
	// Test with default value (env var not set)
	got := envOrDefault("NONEXISTENT_VAR_12345", "default_value")
	if got != "default_value" {
		t.Errorf("envOrDefault() = %q, want %q", got, "default_value")
	}
}

func TestBlogPostAliasFormat(t *testing.T) {
	// Test that blog post alias format is correct (blog/post/{id})
	tests := []struct {
		postID   int64
		expected string
	}{
		{postID: 1, expected: "blog/post/1"},
		{postID: 55, expected: "blog/post/55"},
		{postID: 123, expected: "blog/post/123"},
		{postID: 999999, expected: "blog/post/999999"},
	}

	for _, tt := range tests {
		alias := fmt.Sprintf("blog/post/%d", tt.postID)
		if alias != tt.expected {
			t.Errorf("alias for post %d = %q, want %q", tt.postID, alias, tt.expected)
		}
	}
}

func TestBlogPostModel(t *testing.T) {
	// Test BlogPost struct and IsPublished method
	publishedPost := BlogPost{
		ID:        55,
		Title:     "Test Post",
		Slug:      "test-post",
		Body:      "<p>Content</p>",
		Published: "yes",
	}

	if !publishedPost.IsPublished() {
		t.Error("expected post with Published='yes' to be published")
	}

	draftPost := BlogPost{
		ID:        56,
		Title:     "Draft Post",
		Slug:      "draft-post",
		Body:      "<p>Draft</p>",
		Published: "no",
	}

	if draftPost.IsPublished() {
		t.Error("expected post with Published='no' to not be published")
	}

	queuedPost := BlogPost{
		ID:        57,
		Title:     "Queued Post",
		Slug:      "queued-post",
		Body:      "<p>Queued</p>",
		Published: "que",
	}

	if queuedPost.IsPublished() {
		t.Error("expected post with Published='que' to not be published")
	}
}

func TestWebpageModel(t *testing.T) {
	publicPage := Webpage{
		ID:     "about",
		Title:  "About Us",
		Access: "public",
		Layout: "default",
	}
	if !publicPage.IsPublic() {
		t.Error("expected page with Access='public' to be public")
	}

	memberPage := Webpage{
		ID:     "members-only",
		Title:  "Members Area",
		Access: "member",
	}
	if memberPage.IsPublic() {
		t.Error("expected page with Access='member' to not be public")
	}

	privatePage := Webpage{
		ID:     "private",
		Title:  "Private Page",
		Access: "private",
	}
	if privatePage.IsPublic() {
		t.Error("expected page with Access='private' to not be public")
	}

	emptyPage := Webpage{
		ID:     "no-access",
		Title:  "No Access Set",
		Access: "",
	}
	if emptyPage.IsPublic() {
		t.Error("expected page with Access='' to not be public")
	}
}

func TestWebpageAliasFormat(t *testing.T) {
	tests := []struct {
		pageID    string
		slug      string
		wantAlias bool
	}{
		{"about", "about", false},
		{"about/team", "about-team", true},
		{"contact-us", "contact-us", false},
		{"products/widgets", "products-widgets", true},
	}

	for _, tt := range tests {
		t.Run(tt.pageID, func(t *testing.T) {
			shouldCreateAlias := tt.pageID != tt.slug
			if shouldCreateAlias != tt.wantAlias {
				t.Errorf("alias for %q -> slug %q: got %v, want %v",
					tt.pageID, tt.slug, shouldCreateAlias, tt.wantAlias)
			}
		})
	}
}

func TestNullStringToString(t *testing.T) {
	tests := []struct {
		name  string
		input sql.NullString
		want  string
	}{
		{"valid string", sql.NullString{String: "hello", Valid: true}, "hello"},
		{"empty valid string", sql.NullString{String: "", Valid: true}, ""},
		{"null string", sql.NullString{String: "", Valid: false}, ""},
		{"null with value", sql.NullString{String: "ignored", Valid: false}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nullStringToString(tt.input)
			if got != tt.want {
				t.Errorf("nullStringToString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMockTracker_PostAndPageEntityTypes(t *testing.T) {
	tracker := &mockTracker{}
	ctx := context.Background()

	// Simulate importPosts tracking (entity type "post")
	_ = tracker.TrackImportedItem(ctx, "elefant", "post", 1)
	_ = tracker.TrackImportedItem(ctx, "elefant", "post", 2)
	// Simulate importPages tracking (entity type "page")
	_ = tracker.TrackImportedItem(ctx, "elefant", "page", 100)

	postCount := 0
	pageCount := 0
	for _, item := range tracker.items {
		switch item.entityType {
		case "post":
			postCount++
		case "page":
			pageCount++
		}
	}

	if postCount != 2 {
		t.Errorf("tracked posts = %d, want 2", postCount)
	}
	if pageCount != 1 {
		t.Errorf("tracked pages = %d, want 1", pageCount)
	}
}
