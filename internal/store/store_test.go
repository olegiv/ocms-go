// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/model"
)

// testDB creates a temporary test database.
func testDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "ocms-test-*.db")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	db, err := NewDB(dbPath)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("NewDB: %v", err)
	}

	if err := Migrate(db); err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Migrate: %v", err)
	}

	return db, func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}
}

// testSetup creates a test database and returns common test dependencies.
func testSetup(t *testing.T) (*sql.DB, func(), context.Context, *Queries) {
	t.Helper()
	db, cleanup := testDB(t)
	return db, cleanup, context.Background(), New(db)
}

// createTestUser creates a user with default values for testing.
func createTestUser(t *testing.T, q *Queries, ctx context.Context, email string) User {
	t.Helper()
	now := time.Now()
	user, err := q.CreateUser(ctx, CreateUserParams{
		Email:        email,
		PasswordHash: "hash",
		Role:         "editor",
		Name:         "Test User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", email, err)
	}
	return user
}

// getDefaultLangCode returns the default language code for testing.
func getDefaultLangCode(t *testing.T, q *Queries, ctx context.Context) string {
	t.Helper()
	lang, err := q.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("GetDefaultLanguage: %v", err)
	}
	return lang.Code
}

// createTestPage creates a page with default values for testing.
func createTestPage(t *testing.T, q *Queries, ctx context.Context, authorID int64, slug string) Page {
	t.Helper()
	now := time.Now()
	page, err := q.CreatePage(ctx, CreatePageParams{
		Title:        "Test Page",
		Slug:         slug,
		Body:         "<p>Test content</p>",
		Status:       "published",
		AuthorID:     authorID,
		LanguageCode: getDefaultLangCode(t, q, ctx),
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreatePage(%s): %v", slug, err)
	}
	return page
}

// createTestEntity is a generic helper for creating test entities.
func createTestEntity[T any](t *testing.T, entityName, slug string, createFn func() (T, error)) T {
	t.Helper()
	entity, err := createFn()
	if err != nil {
		t.Fatalf("Create%s(%s): %v", entityName, slug, err)
	}
	return entity
}

// createTestTag creates a tag with default values for testing.
func createTestTag(t *testing.T, q *Queries, ctx context.Context, slug string) Tag {
	t.Helper()
	now := time.Now()
	langCode := getDefaultLangCode(t, q, ctx)
	return createTestEntity(t, "Tag", slug, func() (Tag, error) {
		return q.CreateTag(ctx, CreateTagParams{
			Name: "Test Tag", Slug: slug, LanguageCode: langCode, CreatedAt: now, UpdatedAt: now,
		})
	})
}

// createTestMenu creates a menu with default values for testing.
func createTestMenu(t *testing.T, q *Queries, ctx context.Context, slug string) Menu {
	t.Helper()
	now := time.Now()
	langCode := getDefaultLangCode(t, q, ctx)
	return createTestEntity(t, "Menu", slug, func() (Menu, error) {
		return q.CreateMenu(ctx, CreateMenuParams{
			Name: "Test Menu", Slug: slug, LanguageCode: langCode, CreatedAt: now, UpdatedAt: now,
		})
	})
}

// createTestForm creates a form with default values for testing.
func createTestForm(t *testing.T, q *Queries, ctx context.Context, slug string) Form {
	t.Helper()
	now := time.Now()
	form, err := q.CreateForm(ctx, CreateFormParams{
		Name:         "Test Form",
		Slug:         slug,
		Title:        "Test Form",
		IsActive:     true,
		LanguageCode: getDefaultLangCode(t, q, ctx),
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateForm(%s): %v", slug, err)
	}
	return form
}

func TestCreateUser(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	now := time.Now()
	user, err := q.CreateUser(ctx, CreateUserParams{
		Email: "test@example.com", PasswordHash: "hashed-password",
		Role: "editor", Name: "Test User", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if user.ID == 0 {
		t.Error("user.ID should not be 0")
	}
	if user.Email != "test@example.com" {
		t.Errorf("Email = %q, want %q", user.Email, "test@example.com")
	}
	if user.Role != "editor" {
		t.Errorf("Role = %q, want %q", user.Role, "editor")
	}
	if user.Name != "Test User" {
		t.Errorf("Name = %q, want %q", user.Name, "Test User")
	}
}

func TestGetUserByEmail(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	created := createTestUser(t, q, ctx, "find@example.com")

	found, err := q.GetUserByEmail(ctx, "find@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	if found.ID != created.ID {
		t.Errorf("ID = %d, want %d", found.ID, created.ID)
	}
	if found.Email != "find@example.com" {
		t.Errorf("Email = %q, want %q", found.Email, "find@example.com")
	}
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	_, err := q.GetUserByEmail(ctx, "nonexistent@example.com")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetUserByID(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	created := createTestUser(t, q, ctx, "byid@example.com")

	found, err := q.GetUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	if found.Email != "byid@example.com" {
		t.Errorf("Email = %q, want %q", found.Email, "byid@example.com")
	}
}

func TestUpdateUser(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	created := createTestUser(t, q, ctx, "update@example.com")

	updated, err := q.UpdateUser(ctx, UpdateUserParams{
		ID:        created.ID,
		Email:     "updated@example.com",
		Role:      "admin",
		Name:      "Updated Name",
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	if updated.Email != "updated@example.com" {
		t.Errorf("Email = %q, want %q", updated.Email, "updated@example.com")
	}
	if updated.Role != "admin" {
		t.Errorf("Role = %q, want %q", updated.Role, "admin")
	}
	if updated.Name != "Updated Name" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated Name")
	}
}

func TestDeleteUser(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	created := createTestUser(t, q, ctx, "delete@example.com")

	if err := q.DeleteUser(ctx, created.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, err := q.GetUserByID(ctx, created.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestListUsers(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		createTestUser(t, q, ctx, "user"+string(rune('0'+i))+"@example.com")
	}

	users, err := q.ListUsers(ctx, ListUsersParams{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 {
		t.Errorf("len(users) = %d, want 3", len(users))
	}

	users2, err := q.ListUsers(ctx, ListUsersParams{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("ListUsers page 2: %v", err)
	}
	if len(users2) != 2 {
		t.Errorf("len(users2) = %d, want 2", len(users2))
	}
}

func TestCountUsers(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	for i := 0; i < 3; i++ {
		createTestUser(t, q, ctx, "count"+string(rune('0'+i))+"@example.com")
	}

	count, err = q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

// TestSeedKeepsDefaultAdminAbsentWhileAnotherAdminExists fails when seeding
// recreates admin@example.com (with its public password) after the operator
// renamed or replaced it, as long as OCMS_DO_SEED stays on.
func TestSeedKeepsDefaultAdminAbsentWhileAnotherAdminExists(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	now := time.Now()
	if _, err := q.CreateUser(ctx, CreateUserParams{
		Email: "owner@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Owner", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if _, err := q.GetUserByEmail(ctx, DefaultAdminEmail); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("default admin was seeded although an administrator exists (err = %v)", err)
	}
	if n, err := q.CountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("users = %d, %v; want 1", n, err)
	}
}

// TestSeedToleratesDemotedDefaultAdmin fails when a database whose only
// account is admin@example.com demoted to editor can no longer start: the
// seed must skip rather than hit the unique email constraint.
func TestSeedToleratesDemotedDefaultAdmin(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	now := time.Now()
	if _, err := q.CreateUser(ctx, CreateUserParams{
		Email: DefaultAdminEmail, PasswordHash: "x", Role: model.RoleEditor, Name: "Demoted", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed must skip a demoted default admin, got: %v", err)
	}
	user, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil || user.Role != model.RoleEditor {
		t.Fatalf("default-email account changed: role %q, err %v", user.Role, err)
	}
	if n, err := q.CountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("users = %d, %v; want 1", n, err)
	}
}

func TestSeed(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	admin, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if admin.Role != "admin" {
		t.Errorf("Role = %q, want admin", admin.Role)
	}
	if admin.Name != DefaultAdminName {
		t.Errorf("Name = %q, want %q", admin.Name, DefaultAdminName)
	}

	// Second seed should skip (no error, no duplicate)
	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Second Seed: %v", err)
	}

	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (seed should skip if exists)", count)
	}
}

func TestSeedDisabled(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	// Seed with doSeed=false should skip and create nothing
	if err := Seed(ctx, db, false); err != nil {
		t.Fatalf("Seed with doSeed=false: %v", err)
	}

	// No admin user should exist
	_, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err == nil {
		t.Error("expected no admin user when doSeed=false, but found one")
	}

	// No menus should exist
	menuCount, err := q.CountMenus(ctx)
	if err != nil {
		t.Fatalf("CountMenus: %v", err)
	}
	if menuCount != 0 {
		t.Errorf("menu count = %d, want 0 when doSeed=false", menuCount)
	}
}

func TestHasDefaultAdminCredentials(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	hasDefaults, err := HasDefaultAdminCredentials(ctx, q)
	if err != nil {
		t.Fatalf("HasDefaultAdminCredentials (before seed): %v", err)
	}
	if hasDefaults {
		t.Fatal("expected no default admin credentials before seed")
	}

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	hasDefaults, err = HasDefaultAdminCredentials(ctx, q)
	if err != nil {
		t.Fatalf("HasDefaultAdminCredentials (after seed): %v", err)
	}
	if !hasDefaults {
		t.Fatal("expected seeded default admin credentials to be detected")
	}

	now := time.Now()
	newHash, err := auth.HashPassword("totally-new-password-123!")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	admin, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if err := q.UpdateUserPassword(ctx, UpdateUserPasswordParams{
		PasswordHash: newHash,
		UpdatedAt:    now,
		ID:           admin.ID,
	}); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}

	hasDefaults, err = HasDefaultAdminCredentials(ctx, q)
	if err != nil {
		t.Fatalf("HasDefaultAdminCredentials (after rotation): %v", err)
	}
	if hasDefaults {
		t.Fatal("expected default admin credentials to be false after password rotation")
	}
}

func TestUpdateUserPassword_BumpsSessionVersion(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	before, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	newHash, err := auth.HashPassword("rotated-password-1234")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := q.UpdateUserPassword(ctx, UpdateUserPasswordParams{
		PasswordHash: newHash,
		UpdatedAt:    time.Now(),
		ID:           before.ID,
	}); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}

	after, err := q.GetUserByID(ctx, before.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if after.SessionVersion != before.SessionVersion+1 {
		t.Fatalf("session_version = %d, want %d (before+1)", after.SessionVersion, before.SessionVersion+1)
	}
}

func TestUpdateUserPasswordHash_DoesNotBumpSessionVersion(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	before, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	rehash, err := auth.HashPassword(DefaultAdminPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := q.UpdateUserPasswordHash(ctx, UpdateUserPasswordHashParams{
		PasswordHash: rehash,
		UpdatedAt:    time.Now(),
		ID:           before.ID,
	}); err != nil {
		t.Fatalf("UpdateUserPasswordHash: %v", err)
	}

	after, err := q.GetUserByID(ctx, before.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if after.SessionVersion != before.SessionVersion {
		t.Fatalf("session_version = %d, want %d (unchanged)", after.SessionVersion, before.SessionVersion)
	}
}

// Page CRUD Tests

func TestCreatePage(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()

	page, err := q.CreatePage(ctx, CreatePageParams{
		Title: "Test Page", Slug: "test-page", Body: "<p>Hello World</p>",
		Status: "draft", AuthorID: user.ID, LanguageCode: langCode,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	if page.ID == 0 {
		t.Error("page.ID should not be 0")
	}
	if page.Title != "Test Page" {
		t.Errorf("Title = %q, want %q", page.Title, "Test Page")
	}
	if page.Slug != "test-page" {
		t.Errorf("Slug = %q, want %q", page.Slug, "test-page")
	}
	if page.Status != "draft" {
		t.Errorf("Status = %q, want %q", page.Status, "draft")
	}
}

func TestGetPageByID(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	created := createTestPage(t, q, ctx, user.ID, "find-me")

	found, err := q.GetPageByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPageByID: %v", err)
	}

	if found.ID != created.ID {
		t.Errorf("ID = %d, want %d", found.ID, created.ID)
	}
}

func TestGetPageBySlug(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	createTestPage(t, q, ctx, user.ID, "slug-test")

	found, err := q.GetPageBySlug(ctx, "slug-test")
	if err != nil {
		t.Fatalf("GetPageBySlug: %v", err)
	}

	if found.Slug != "slug-test" {
		t.Errorf("Slug = %q, want %q", found.Slug, "slug-test")
	}
}

func TestUpdatePage(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	created := createTestPage(t, q, ctx, user.ID, "original-slug")

	updated, err := q.UpdatePage(ctx, UpdatePageParams{
		ID:           created.ID,
		Title:        "Updated Title",
		Slug:         "updated-slug",
		Body:         "<p>Updated</p>",
		Status:       "published",
		LanguageCode: created.LanguageCode,
		UpdatedAt:    time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}

	if updated.Title != "Updated Title" {
		t.Errorf("Title = %q, want %q", updated.Title, "Updated Title")
	}
	if updated.Slug != "updated-slug" {
		t.Errorf("Slug = %q, want %q", updated.Slug, "updated-slug")
	}
	if updated.Status != "published" {
		t.Errorf("Status = %q, want %q", updated.Status, "published")
	}
}

func TestDeletePage(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	created := createTestPage(t, q, ctx, user.ID, "delete-me")

	if err := q.DeletePage(ctx, created.ID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	_, err := q.GetPageByID(ctx, created.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestListPages(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	for i := 0; i < 5; i++ {
		createTestPage(t, q, ctx, user.ID, "page-"+string(rune('0'+i)))
	}

	pages, err := q.ListPages(ctx, ListPagesParams{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if len(pages) != 3 {
		t.Errorf("len(pages) = %d, want 3", len(pages))
	}
}

func TestCountPages(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	count, err := q.CountPages(ctx)
	if err != nil {
		t.Fatalf("CountPages: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	user := createTestUser(t, q, ctx, "author@example.com")
	for i := 0; i < 3; i++ {
		createTestPage(t, q, ctx, user.ID, "page-"+string(rune('0'+i)))
	}

	count, err = q.CountPages(ctx)
	if err != nil {
		t.Fatalf("CountPages: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestPublishPage(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()

	scheduledAt := sql.NullTime{Time: now.Add(-1 * time.Hour), Valid: true}
	created, err := q.CreatePage(ctx, CreatePageParams{
		Title: "Publish Test", Slug: "publish-test", Body: "<p>Content</p>",
		Status: "draft", AuthorID: user.ID, LanguageCode: langCode,
		ScheduledAt: scheduledAt,
		CreatedAt:   now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	publishTime := time.Now()
	published, err := q.PublishPage(ctx, PublishPageParams{
		ID:          created.ID,
		PublishedAt: sql.NullTime{Time: publishTime, Valid: true},
		UpdatedAt:   publishTime,
	})
	if err != nil {
		t.Fatalf("PublishPage: %v", err)
	}

	if published.Status != "published" {
		t.Errorf("Status = %q, want %q", published.Status, "published")
	}
	if !published.PublishedAt.Valid {
		t.Error("PublishedAt should be valid after publishing")
	}
	if published.ScheduledAt.Valid {
		t.Error("ScheduledAt should be cleared after manual publish")
	}
}

func TestUnpublishPageClearsScheduledAt(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()
	publishTime := time.Now()
	scheduledAt := sql.NullTime{Time: now.Add(-1 * time.Hour), Valid: true}

	created, err := q.CreatePage(ctx, CreatePageParams{
		Title: "Unpublish Test", Slug: "unpublish-test", Body: "<p>Content</p>",
		Status: "published", AuthorID: user.ID, LanguageCode: langCode,
		ScheduledAt: scheduledAt,
		PublishedAt: sql.NullTime{Time: publishTime, Valid: true},
		CreatedAt:   now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	unpublished, err := q.UnpublishPage(ctx, UnpublishPageParams{
		ID:        created.ID,
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("UnpublishPage: %v", err)
	}

	if unpublished.Status != "draft" {
		t.Errorf("Status = %q, want %q", unpublished.Status, "draft")
	}
	if unpublished.PublishedAt.Valid {
		t.Error("PublishedAt should be null after unpublish")
	}
	if unpublished.ScheduledAt.Valid {
		t.Error("ScheduledAt should be cleared after manual unpublish")
	}
}

// TestPublishToggleQueriesMatchOnlyTheOtherStatus checks that the admin
// publish toggle's queries change only a page with the status the toggle
// read: publishing a published page, or unpublishing a draft, matches no
// row. Without that, a toggle racing the scheduler published the page a
// second time and sent page.published twice.
//
// Bug state: drop the status condition from PublishUnpublishedPage or
// UnpublishPage and this fails.
func TestPublishToggleQueriesMatchOnlyTheOtherStatus(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()
	create := func(slug, status string) Page {
		t.Helper()
		page, err := q.CreatePage(ctx, CreatePageParams{
			Title: slug, Slug: slug, Body: "b", Status: status, AuthorID: user.ID, LanguageCode: langCode,
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		return page
	}

	published := create("already-published", "published")
	if _, err := q.PublishUnpublishedPage(ctx, PublishUnpublishedPageParams{
		ID: published.ID, PublishedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("PublishUnpublishedPage(published page) error = %v, want sql.ErrNoRows", err)
	}
	draft := create("still-draft", "draft")
	if _, err := q.UnpublishPage(ctx, UnpublishPageParams{ID: draft.ID, UpdatedAt: now}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UnpublishPage(draft) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := q.PublishUnpublishedPage(ctx, PublishUnpublishedPageParams{
		ID: draft.ID, PublishedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now,
	}); err != nil || got.Status != "published" {
		t.Errorf("PublishUnpublishedPage(draft) = %q, %v; want it published", got.Status, err)
	}
}

// Phase 2 Tests

// Tag CRUD Tests

func TestCreateTag(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()
	tag, err := q.CreateTag(ctx, CreateTagParams{
		Name: "Test Tag", Slug: "test-tag", LanguageCode: langCode,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	if tag.ID == 0 {
		t.Error("tag.ID should not be 0")
	}
	if tag.Name != "Test Tag" {
		t.Errorf("Name = %q, want %q", tag.Name, "Test Tag")
	}
	if tag.Slug != "test-tag" {
		t.Errorf("Slug = %q, want %q", tag.Slug, "test-tag")
	}
}

func TestGetTagBySlug(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	created := createTestTag(t, q, ctx, "find-tag")

	found, err := q.GetTagBySlug(ctx, "find-tag")
	if err != nil {
		t.Fatalf("GetTagBySlug: %v", err)
	}

	if found.ID != created.ID {
		t.Errorf("ID = %d, want %d", found.ID, created.ID)
	}
}

func TestPageTagAssociation(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "author@example.com")
	page := createTestPage(t, q, ctx, user.ID, "tagged-page")
	tag1 := createTestTag(t, q, ctx, "tag-1")
	tag2 := createTestTag(t, q, ctx, "tag-2")

	if err := q.AddTagToPage(ctx, AddTagToPageParams{PageID: page.ID, TagID: tag1.ID}); err != nil {
		t.Fatalf("AddTagToPage: %v", err)
	}
	if err := q.AddTagToPage(ctx, AddTagToPageParams{PageID: page.ID, TagID: tag2.ID}); err != nil {
		t.Fatalf("AddTagToPage: %v", err)
	}

	tags, err := q.GetTagsForPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("GetTagsForPage: %v", err)
	}
	if len(tags) != 2 {
		t.Errorf("len(tags) = %d, want 2", len(tags))
	}
}

// Category CRUD Tests

func TestCreateCategory(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()
	cat, err := q.CreateCategory(ctx, CreateCategoryParams{
		Name: "Test Category", Slug: "test-category",
		Description: sql.NullString{String: "A test category", Valid: true},
		ParentID:    sql.NullInt64{Valid: false}, LanguageCode: langCode, Position: 0,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if cat.ID == 0 {
		t.Error("cat.ID should not be 0")
	}
	if cat.Name != "Test Category" {
		t.Errorf("Name = %q, want %q", cat.Name, "Test Category")
	}
}

func TestCategoryHierarchy(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()
	parent, err := q.CreateCategory(ctx, CreateCategoryParams{
		Name: "Parent", Slug: "parent", LanguageCode: langCode,
		Position: 0, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateCategory parent: %v", err)
	}

	child, err := q.CreateCategory(ctx, CreateCategoryParams{
		Name: "Child", Slug: "child", LanguageCode: langCode,
		ParentID: sql.NullInt64{Int64: parent.ID, Valid: true},
		Position: 0, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateCategory child: %v", err)
	}

	children, err := q.ListChildCategories(ctx, sql.NullInt64{Int64: parent.ID, Valid: true})
	if err != nil {
		t.Fatalf("ListChildCategories: %v", err)
	}
	if len(children) != 1 {
		t.Errorf("len(children) = %d, want 1", len(children))
	}
	if children[0].ID != child.ID {
		t.Errorf("child.ID = %d, want %d", children[0].ID, child.ID)
	}
}

// Menu CRUD Tests

func TestCreateMenu(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()
	menu, err := q.CreateMenu(ctx, CreateMenuParams{
		Name: "Main Menu", Slug: "main", LanguageCode: langCode,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	if menu.ID == 0 {
		t.Error("menu.ID should not be 0")
	}
	if menu.Name != "Main Menu" {
		t.Errorf("Name = %q, want %q", menu.Name, "Main Menu")
	}
}

func TestMenuItems(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	menu := createTestMenu(t, q, ctx, "test-menu")
	now := time.Now()

	item1, err := q.CreateMenuItem(ctx, CreateMenuItemParams{
		MenuID: menu.ID, Title: "Home", Url: sql.NullString{String: "/", Valid: true},
		Target: sql.NullString{String: "_self", Valid: true}, Position: 0, IsActive: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem: %v", err)
	}

	item2, err := q.CreateMenuItem(ctx, CreateMenuItemParams{
		MenuID: menu.ID, Title: "About", Url: sql.NullString{String: "/about", Valid: true},
		Target: sql.NullString{String: "_self", Valid: true}, Position: 1, IsActive: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem: %v", err)
	}

	items, err := q.ListMenuItems(ctx, menu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("len(items) = %d, want 2", len(items))
	}
	if items[0].ID != item1.ID {
		t.Errorf("first item ID = %d, want %d", items[0].ID, item1.ID)
	}
	if items[1].ID != item2.ID {
		t.Errorf("second item ID = %d, want %d", items[1].ID, item2.ID)
	}
}

// Form CRUD Tests

func TestCreateForm(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	langCode := getDefaultLangCode(t, q, ctx)
	now := time.Now()
	form, err := q.CreateForm(ctx, CreateFormParams{
		Name: "Contact Form", Slug: "contact", Title: "Contact Us",
		Description:    sql.NullString{String: "Get in touch", Valid: true},
		SuccessMessage: sql.NullString{String: "Thank you!", Valid: true},
		EmailTo:        sql.NullString{String: "test@example.com", Valid: true},
		IsActive:       true,
		LanguageCode:   langCode,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	if form.ID == 0 {
		t.Error("form.ID should not be 0")
	}
	if form.Name != "Contact Form" {
		t.Errorf("Name = %q, want %q", form.Name, "Contact Form")
	}
}

func TestFormFields(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	form := createTestForm(t, q, ctx, "test-form")
	now := time.Now()

	_, err := q.CreateFormField(ctx, CreateFormFieldParams{
		FormID: form.ID, Type: "text", Name: "name", Label: "Your Name",
		IsRequired: true, Position: 0, LanguageCode: form.LanguageCode,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFormField: %v", err)
	}

	_, err = q.CreateFormField(ctx, CreateFormFieldParams{
		FormID: form.ID, Type: "email", Name: "email", Label: "Your Email",
		IsRequired: true, Position: 1, LanguageCode: form.LanguageCode,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFormField: %v", err)
	}

	fields, err := q.GetFormFields(ctx, form.ID)
	if err != nil {
		t.Fatalf("GetFormFields: %v", err)
	}
	if len(fields) != 2 {
		t.Errorf("len(fields) = %d, want 2", len(fields))
	}
}

func TestFormSubmission(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	form := createTestForm(t, q, ctx, "submission-test")
	now := time.Now()

	sub, err := q.CreateFormSubmission(ctx, CreateFormSubmissionParams{
		FormID: form.ID, Data: `{"name":"John","email":"john@example.com"}`,
		IpAddress:    sql.NullString{String: "127.0.0.1", Valid: true},
		UserAgent:    sql.NullString{String: "Mozilla/5.0", Valid: true},
		IsRead:       false,
		LanguageCode: form.LanguageCode,
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateFormSubmission: %v", err)
	}
	if sub.ID == 0 {
		t.Error("sub.ID should not be 0")
	}
	if sub.IsRead {
		t.Error("IsRead should be false initially")
	}

	if err := q.MarkSubmissionRead(ctx, sub.ID); err != nil {
		t.Fatalf("MarkSubmissionRead: %v", err)
	}

	found, err := q.GetFormSubmissionByID(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetFormSubmissionByID: %v", err)
	}
	if !found.IsRead {
		t.Error("IsRead should be true after marking")
	}
}

func TestCountUnreadSubmissions(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	form := createTestForm(t, q, ctx, "count-test")
	now := time.Now()

	for i := 0; i < 3; i++ {
		_, err := q.CreateFormSubmission(ctx, CreateFormSubmissionParams{
			FormID:       form.ID,
			Data:         `{"test":"data"}`,
			IsRead:       false,
			LanguageCode: form.LanguageCode,
			CreatedAt:    now,
		})
		if err != nil {
			t.Fatalf("CreateFormSubmission: %v", err)
		}
	}

	count, err := q.CountUnreadSubmissions(ctx, form.ID)
	if err != nil {
		t.Fatalf("CountUnreadSubmissions: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}

	allCount, err := q.CountAllUnreadSubmissions(ctx)
	if err != nil {
		t.Fatalf("CountAllUnreadSubmissions: %v", err)
	}
	if allCount != 3 {
		t.Errorf("allCount = %d, want 3", allCount)
	}
}

// TestNewDBKeepsInMemoryDatabasesOnOneConnection checks that an in-memory
// database keeps a single connection that never expires. Each connection to
// ":memory:" opens its own empty database, so with the default pool of 25 a
// query on a second connection found no tables (review of the 2026-10-17
// audit fixes); a file database keeps the configured pool.
//
// Bug state: drop the isInMemory branch in NewDBWithConfig and this fails.
func TestNewDBKeepsInMemoryDatabasesOnOneConnection(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB(:memory:): %v", err)
	}
	defer func() { _ = db.Close() }()
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("in-memory database allows %d connections, want 1", got)
	}
	if _, err := db.Exec(`CREATE TABLE probe (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	// Queries from many goroutines would open more connections, each with
	// an empty database, if the pool allowed it.
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			var n int
			errs <- db.QueryRow(`SELECT COUNT(*) FROM probe`).Scan(&n)
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Errorf("query on the in-memory database: %v", err)
		}
	}

	file, err := NewDB(t.TempDir() + "/file.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if got, want := file.Stats().MaxOpenConnections, DefaultDBConfig().MaxOpenConns; got != want {
		t.Errorf("file database allows %d connections, want the configured %d", got, want)
	}

	for path, want := range map[string]bool{
		":memory:":                        true,
		"file::memory:":                   true,
		"file:probe?mode=memory":          true,
		"file::memory:?cache=shared":      false,
		"file:x?mode=memory&cache=shared": false,
		"./data/ocms.db":                  false,
	} {
		if got := isInMemory(path); got != want {
			t.Errorf("isInMemory(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestNewDBStoresTimesInSQLiteFormat checks the text a time.Time is stored
// as: "2006-01-02 15:04:05.999999999+00:00", in UTC, which SQLite's date
// functions read, round-trips to the same instant, and leaves rows written in the
// driver's old default, time.Time.String() with a monotonic suffix,
// readable, for file and in-memory databases. Every time.Time the app bound
// used to be stored in that old default (review of the 2026-10-17 audit
// fixes).
//
// Bug state: drop _time_format=sqlite or _timezone=UTC from the DSN in
// NewDBWithConfig and this fails, in any local zone: the stored time is in
// CEST.
func TestNewDBStoresTimesInSQLiteFormat(t *testing.T) {
	for name, path := range map[string]string{"file": t.TempDir() + "/times.db", "memory": ":memory:"} {
		t.Run(name, func(t *testing.T) { checkStoredTimeFormat(t, path) })
	}
}

// checkStoredTimeFormat runs TestNewDBStoresTimesInSQLiteFormat on the
// database at path.
func checkStoredTimeFormat(t *testing.T, path string) {
	db, err := NewDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE probe (id INTEGER PRIMARY KEY, at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(time.FixedZone("CEST", 2*3600))
	if _, err := db.Exec(`INSERT INTO probe (id, at) VALUES (1, ?)`, now); err != nil {
		t.Fatal(err)
	}
	var raw string
	var normalized sql.NullString
	if err := db.QueryRow(`SELECT CAST(at AS TEXT), datetime(at) FROM probe WHERE id = 1`).Scan(&raw, &normalized); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)?\+00:00$`).MatchString(raw) {
		t.Errorf("a time is stored as %q, want 2006-01-02 15:04:05.999999999+00:00 (UTC)", raw)
	}
	if !normalized.Valid {
		t.Errorf("SQLite's datetime() cannot read the stored time %q", raw)
	}
	var back time.Time
	if err := db.QueryRow(`SELECT at FROM probe WHERE id = 1`).Scan(&back); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(now) {
		t.Errorf("the stored time reads back as %v, want %v", back, now)
	}

	// A row written in the old default is still read.
	old := "2026-10-18 05:41:31.179863 +0200 CEST m=+0.088685751"
	if _, err := db.Exec(`INSERT INTO probe (id, at) VALUES (2, ?)`, old); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT at FROM probe WHERE id = 2`).Scan(&back); err != nil {
		t.Fatalf("reading a time stored in the old format: %v", err)
	}
	if want := time.Date(2026, 10, 18, 3, 41, 31, 179863000, time.UTC); !back.Equal(want) {
		t.Errorf("the old-format time reads back as %v, want %v", back, want)
	}
}

// TestOldAndNewTimeFormatsOrderTogether checks that after the UTC time
// migration, rows written in the old format (time.Time.String(), local
// time) and rows written now sort and filter together by instant, within
// the same second too, so a cutoff passed as a time.Time deletes the right
// rows. Without the migration the old local-time text and the new UTC text
// would sort by their wall clocks.
//
// Bug state: set _time_format=datetime in NewDBWithConfig, which drops the
// fraction and the offset, or skip the migration, and this fails.
func TestOldAndNewTimeFormatsOrderTogether(t *testing.T) {
	db, err := NewDB(t.TempDir() + "/order.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE probe (label TEXT, at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("CEST", 2*3600)
	base := time.Date(2026, 10, 18, 5, 41, 31, 0, zone)
	rows := []struct {
		label string
		at    any
	}{
		{"a-old", "2026-10-18 05:41:30.9 +0200 CEST m=+1.0"},
		{"b-new", base},
		{"c-old", "2026-10-18 05:41:31.25 +0200 CEST m=+2.0"},
		{"d-new", base.Add(500 * time.Millisecond)},
		{"e-old", "2026-10-18 05:41:31.75 +0200 CEST"},
		{"f-sql", "2026-10-18 03:41:32"}, // CURRENT_TIMESTAMP text: UTC, no offset
		{"g-new", base.Add(2 * time.Second)},
	}
	for i := len(rows) - 1; i >= 0; i-- { // insert out of order
		if _, err := db.Exec(`INSERT INTO probe (label, at) VALUES (?, ?)`, rows[i].label, rows[i].at); err != nil {
			t.Fatal(err)
		}
	}
	runTimeMigration(t, db)
	got, err := db.Query(`SELECT label FROM probe ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Close() }()
	var order []string
	for got.Next() {
		var label string
		if err := got.Scan(&label); err != nil {
			t.Fatal(err)
		}
		order = append(order, label)
	}
	if err := got.Err(); err != nil {
		t.Fatal(err)
	}
	if want := "a-old b-new c-old d-new e-old f-sql g-new"; strings.Join(order, " ") != want {
		t.Errorf("rows sort as %q, want %q", strings.Join(order, " "), want)
	}
	// Cut off between c-old (.25) and d-new (.5).
	if _, err := db.Exec(`DELETE FROM probe WHERE at < ?`, base.Add(400*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM probe`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 4 {
		t.Errorf("a cutoff at .4 left %d rows, want d-new, e-old, f-sql and g-new", left)
	}
}

// runTimeMigration runs migrateTimesToUTC on db in a transaction.
func runTimeMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateTimesToUTC(context.Background(), tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("migrateTimesToUTC: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestMigrateTimesToUTC checks the migration that moves stored times to UTC,
// run by Migrate over a table that exists before it, as module tables do:
// old time.Time.String() text, with a named or a numeric zone, and text with
// a non-UTC offset becomes UTC text in the stored layout, at full precision,
// in DATETIME, TIMESTAMP and DATE columns; UTC text, SQLite's own
// CURRENT_TIMESTAMP text, NULL, values that do not parse, which are logged,
// and columns not declared as times are left alone. A second run changes
// nothing, and rolling it back only forgets it.
//
// Bug state: register the migration without its up function, drop the GLOB
// for "+02:00" offsets in migrateTimeColumn, drop the offset-only layout in
// parseStoredTime, or format with .Format(time.DateTime), and this fails.
func TestMigrateTimesToUTC(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	db, err := NewDB(t.TempDir() + "/migrate.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE "mod-probe" (id INTEGER PRIMARY KEY, at DATETIME, stamp TIMESTAMP, day DATE, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ stored, want string }{
		{"2026-10-18 05:41:31.179863 +0200 CEST m=+0.088685751", "2026-10-18 03:41:31.179863+00:00"},
		{"2026-01-10 12:00:00 +0100 CET", "2026-01-10 11:00:00+00:00"},
		{"2026-03-01 07:30:00.123456789 -0500 EST m=+1.5", "2026-03-01 12:30:00.123456789+00:00"},
		{"2026-10-18 09:26:31 +0545 +0545 m=+0.1", "2026-10-18 03:41:31+00:00"},
		{"2026-10-18 05:41:31.5+02:00", "2026-10-18 03:41:31.5+00:00"},
		{"2026-10-18 01:41:31.5-02:00", "2026-10-18 03:41:31.5+00:00"},
		{"2026-10-18T05:41:31+02:00", "2026-10-18 03:41:31+00:00"},
		{"2026-10-18 03:41:31.5+00:00", "2026-10-18 03:41:31.5+00:00"},
		{"2026-10-18 03:41:31", "2026-10-18 03:41:31"},
		{"not a time", "not a time"},
		{"2026-13-45 05:41:31 +0200 CEST", "2026-13-45 05:41:31 +0200 CEST"},
	}
	for i, c := range cases {
		if _, err := db.Exec(`INSERT INTO "mod-probe" (id, at, stamp, day, note) VALUES (?, ?, ?, ?, ?)`, i+1, c.stored, c.stored, c.stored, c.stored); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO "mod-probe" (id) VALUES (100)`); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		for i, c := range cases {
			var at, stamp, day, note string
			if err := db.QueryRow(`SELECT CAST(at AS TEXT), CAST(stamp AS TEXT), CAST(day AS TEXT), note FROM "mod-probe" WHERE id = ?`, i+1).Scan(&at, &stamp, &day, &note); err != nil {
				t.Fatal(err)
			}
			for column, got := range map[string]string{"DATETIME": at, "TIMESTAMP": stamp, "DATE": day} {
				if got != c.want {
					t.Errorf("%s: in a %s column, %q became %q, want %q", when, column, c.stored, got, c.want)
				}
			}
			if note != c.stored {
				t.Errorf("%s: the TEXT column changed from %q to %q", when, c.stored, note)
			}
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	check("after Migrate")
	for _, want := range []string{"left times that do not parse", `table=mod-probe column=day count=1 example="2026-13-45`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the values left unparsed were not logged with %q:\n%s", want, logs.String())
		}
	}
	runTimeMigration(t, db)
	check("after a second run")

	version, err := goose.NumericComponent(timeMigrationName)
	if err != nil {
		t.Fatal(err)
	}
	applied := func() bool {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied`, version).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !applied() {
		t.Fatalf("Migrate did not record the UTC time migration (%s)", timeMigrationName)
	}
	if err := RunMigrationCommand(context.Background(), db, "down"); err != nil {
		t.Fatalf("rolling back the UTC time migration: %v", err)
	}
	if applied() {
		t.Error("rolling back left the UTC time migration recorded")
	}
	check("after rolling back")
	if err := Migrate(db); err != nil || !applied() {
		t.Errorf("Migrate after a rollback: err=%v, recorded=%t", err, applied())
	}
}
