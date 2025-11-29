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

// Page CRUD Tests

func TestCreatePage(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// First create a user (author)
	now := time.Now()
	user, err := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Create page
	page, err := q.CreatePage(ctx, CreatePageParams{
		Title:     "Test Page",
		Slug:      "test-page",
		Body:      "<p>Hello World</p>",
		Status:    "draft",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user and page
	now := time.Now()
	user, _ := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	created, err := q.CreatePage(ctx, CreatePageParams{
		Title:     "Find Me",
		Slug:      "find-me",
		Body:      "<p>Content</p>",
		Status:    "published",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Find by ID
	found, err := q.GetPageByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPageByID: %v", err)
	}

	if found.ID != created.ID {
		t.Errorf("ID = %d, want %d", found.ID, created.ID)
	}
	if found.Title != "Find Me" {
		t.Errorf("Title = %q, want %q", found.Title, "Find Me")
	}
}

func TestGetPageBySlug(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user and page
	now := time.Now()
	user, _ := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	_, err := q.CreatePage(ctx, CreatePageParams{
		Title:     "Slug Test",
		Slug:      "slug-test",
		Body:      "<p>Content</p>",
		Status:    "published",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Find by slug
	found, err := q.GetPageBySlug(ctx, "slug-test")
	if err != nil {
		t.Fatalf("GetPageBySlug: %v", err)
	}

	if found.Slug != "slug-test" {
		t.Errorf("Slug = %q, want %q", found.Slug, "slug-test")
	}
}

func TestUpdatePage(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user and page
	now := time.Now()
	user, _ := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	created, err := q.CreatePage(ctx, CreatePageParams{
		Title:     "Original Title",
		Slug:      "original-slug",
		Body:      "<p>Original</p>",
		Status:    "draft",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Update page
	updated, err := q.UpdatePage(ctx, UpdatePageParams{
		ID:        created.ID,
		Title:     "Updated Title",
		Slug:      "updated-slug",
		Body:      "<p>Updated</p>",
		Status:    "published",
		UpdatedAt: time.Now(),
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user and page
	now := time.Now()
	user, _ := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	created, err := q.CreatePage(ctx, CreatePageParams{
		Title:     "Delete Me",
		Slug:      "delete-me",
		Body:      "<p>Content</p>",
		Status:    "draft",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Delete page
	err = q.DeletePage(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	// Verify deleted
	_, err = q.GetPageByID(ctx, created.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestListPages(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user
	now := time.Now()
	user, _ := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	// Create multiple pages
	for i := 0; i < 5; i++ {
		_, err := q.CreatePage(ctx, CreatePageParams{
			Title:     "Page " + string(rune('0'+i)),
			Slug:      "page-" + string(rune('0'+i)),
			Body:      "<p>Content</p>",
			Status:    "published",
			AuthorID:  user.ID,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
	}

	// List with pagination
	pages, err := q.ListPages(ctx, ListPagesParams{
		Limit:  3,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}

	if len(pages) != 3 {
		t.Errorf("len(pages) = %d, want 3", len(pages))
	}
}

func TestCountPages(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Count empty
	count, err := q.CountPages(ctx)
	if err != nil {
		t.Fatalf("CountPages: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	// Create user and pages
	now := time.Now()
	user, _ := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	for i := 0; i < 3; i++ {
		_, err := q.CreatePage(ctx, CreatePageParams{
			Title:     "Page " + string(rune('0'+i)),
			Slug:      "page-" + string(rune('0'+i)),
			Body:      "<p>Content</p>",
			Status:    "published",
			AuthorID:  user.ID,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
	}

	// Count again
	count, err = q.CountPages(ctx)
	if err != nil {
		t.Fatalf("CountPages: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestPublishPage(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	// Create user and page
	now := time.Now()
	user, _ := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	created, err := q.CreatePage(ctx, CreatePageParams{
		Title:     "Publish Test",
		Slug:      "publish-test",
		Body:      "<p>Content</p>",
		Status:    "draft",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Publish the page
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
}
