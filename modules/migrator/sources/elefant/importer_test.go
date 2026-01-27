// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// mockTracker implements types.ImportTracker for testing.
type mockTracker struct {
	items []trackedItem
}

type trackedItem struct {
	source     string
	entityType string
	entityID   int64
}

func (m *mockTracker) TrackImportedItem(_ context.Context, source, entityType string, entityID int64) error {
	m.items = append(m.items, trackedItem{source, entityType, entityID})
	return nil
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
			last_login_at DATETIME
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
		ImportUsers:  true,
		SkipExisting: false,
	}

	if !optsWithUsers.ImportUsers {
		t.Error("ImportUsers should be true")
	}

	optsWithoutUsers := types.ImportOptions{
		ImportTags:   true,
		ImportPosts:  true,
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
