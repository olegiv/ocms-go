package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// testDB creates a temporary test database.
func testDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	// Create temp file for test database
	f, err := os.CreateTemp("", "ocms-test-*.db")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	dbPath := f.Name()
	f.Close()

	// Open database
	db, err := NewDB(dbPath)
	if err != nil {
		os.Remove(dbPath)
		t.Fatalf("NewDB: %v", err)
	}

	// Run migrations
	if err := Migrate(db); err != nil {
		db.Close()
		os.Remove(dbPath)
		t.Fatalf("Migrate: %v", err)
	}

	// Return cleanup function
	cleanup := func() {
		db.Close()
		os.Remove(dbPath)
	}

	return db, cleanup
}

func TestCreateUser(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()
	user, err := q.CreateUser(ctx, CreateUserParams{
		Email:        "test@example.com",
		PasswordHash: "hashed-password",
		Role:         "editor",
		Name:         "Test User",
		CreatedAt:    now,
		UpdatedAt:    now,
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user first
	now := time.Now()
	created, err := q.CreateUser(ctx, CreateUserParams{
		Email:        "find@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Find Me",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Find by email
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	_, err := q.GetUserByEmail(ctx, "nonexistent@example.com")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetUserByID(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user first
	now := time.Now()
	created, err := q.CreateUser(ctx, CreateUserParams{
		Email:        "byid@example.com",
		PasswordHash: "hash",
		Role:         "editor",
		Name:         "By ID",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Find by ID
	found, err := q.GetUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	if found.Email != "byid@example.com" {
		t.Errorf("Email = %q, want %q", found.Email, "byid@example.com")
	}
}

func TestUpdateUser(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user first
	now := time.Now()
	created, err := q.CreateUser(ctx, CreateUserParams{
		Email:        "update@example.com",
		PasswordHash: "hash",
		Role:         "editor",
		Name:         "Original Name",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Update user
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user first
	now := time.Now()
	created, err := q.CreateUser(ctx, CreateUserParams{
		Email:        "delete@example.com",
		PasswordHash: "hash",
		Role:         "editor",
		Name:         "Delete Me",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Delete user
	err = q.DeleteUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Verify deleted
	_, err = q.GetUserByID(ctx, created.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestListUsers(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create multiple users
	now := time.Now()
	for i := 0; i < 5; i++ {
		_, err := q.CreateUser(ctx, CreateUserParams{
			Email:        "user" + string(rune('0'+i)) + "@example.com",
			PasswordHash: "hash",
			Role:         "editor",
			Name:         "User " + string(rune('0'+i)),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
	}

	// List with pagination
	users, err := q.ListUsers(ctx, ListUsersParams{
		Limit:  3,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	if len(users) != 3 {
		t.Errorf("len(users) = %d, want 3", len(users))
	}

	// List second page
	users2, err := q.ListUsers(ctx, ListUsersParams{
		Limit:  3,
		Offset: 3,
	})
	if err != nil {
		t.Fatalf("ListUsers page 2: %v", err)
	}

	if len(users2) != 2 {
		t.Errorf("len(users2) = %d, want 2", len(users2))
	}
}

func TestCountUsers(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Count empty
	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	// Create users
	now := time.Now()
	for i := 0; i < 3; i++ {
		_, err := q.CreateUser(ctx, CreateUserParams{
			Email:        "count" + string(rune('0'+i)) + "@example.com",
			PasswordHash: "hash",
			Role:         "editor",
			Name:         "Count User",
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
	}

	// Count again
	count, err = q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestSeed(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// First seed should create admin
	err := Seed(ctx, db)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Verify admin exists
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
	err = Seed(ctx, db)
	if err != nil {
		t.Fatalf("Second Seed: %v", err)
	}

	// Should still be only 1 user
	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (seed should skip if exists)", count)
	}
}
