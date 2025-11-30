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

// Phase 2 Tests

// Tag CRUD Tests

func TestCreateTag(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()
	tag, err := q.CreateTag(ctx, CreateTagParams{
		Name:      "Test Tag",
		Slug:      "test-tag",
		CreatedAt: now,
		UpdatedAt: now,
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()
	created, err := q.CreateTag(ctx, CreateTagParams{
		Name:      "Find Tag",
		Slug:      "find-tag",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	found, err := q.GetTagBySlug(ctx, "find-tag")
	if err != nil {
		t.Fatalf("GetTagBySlug: %v", err)
	}

	if found.ID != created.ID {
		t.Errorf("ID = %d, want %d", found.ID, created.ID)
	}
}

func TestPageTagAssociation(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()

	// Create user
	user, _ := q.CreateUser(ctx, CreateUserParams{
		Email:        "author@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Author",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	// Create page
	page, _ := q.CreatePage(ctx, CreatePageParams{
		Title:     "Tagged Page",
		Slug:      "tagged-page",
		Body:      "<p>Content</p>",
		Status:    "published",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Create tags
	tag1, _ := q.CreateTag(ctx, CreateTagParams{Name: "Tag 1", Slug: "tag-1", CreatedAt: now, UpdatedAt: now})
	tag2, _ := q.CreateTag(ctx, CreateTagParams{Name: "Tag 2", Slug: "tag-2", CreatedAt: now, UpdatedAt: now})

	// Add tags to page
	err := q.AddTagToPage(ctx, AddTagToPageParams{PageID: page.ID, TagID: tag1.ID})
	if err != nil {
		t.Fatalf("AddTagToPage: %v", err)
	}
	err = q.AddTagToPage(ctx, AddTagToPageParams{PageID: page.ID, TagID: tag2.ID})
	if err != nil {
		t.Fatalf("AddTagToPage: %v", err)
	}

	// Get tags for page
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()
	cat, err := q.CreateCategory(ctx, CreateCategoryParams{
		Name:        "Test Category",
		Slug:        "test-category",
		Description: sql.NullString{String: "A test category", Valid: true},
		ParentID:    sql.NullInt64{Valid: false},
		Position:    0,
		CreatedAt:   now,
		UpdatedAt:   now,
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()

	// Create parent category
	parent, err := q.CreateCategory(ctx, CreateCategoryParams{
		Name:      "Parent",
		Slug:      "parent",
		Position:  0,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateCategory parent: %v", err)
	}

	// Create child category
	child, err := q.CreateCategory(ctx, CreateCategoryParams{
		Name:      "Child",
		Slug:      "child",
		ParentID:  sql.NullInt64{Int64: parent.ID, Valid: true},
		Position:  0,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateCategory child: %v", err)
	}

	// List children
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()
	menu, err := q.CreateMenu(ctx, CreateMenuParams{
		Name:      "Main Menu",
		Slug:      "main",
		CreatedAt: now,
		UpdatedAt: now,
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()

	// Create menu
	menu, _ := q.CreateMenu(ctx, CreateMenuParams{
		Name:      "Test Menu",
		Slug:      "test-menu",
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Create menu items
	item1, err := q.CreateMenuItem(ctx, CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "Home",
		Url:       sql.NullString{String: "/", Valid: true},
		Target:    sql.NullString{String: "_self", Valid: true},
		Position:  0,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem: %v", err)
	}

	item2, err := q.CreateMenuItem(ctx, CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "About",
		Url:       sql.NullString{String: "/about", Valid: true},
		Target:    sql.NullString{String: "_self", Valid: true},
		Position:  1,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem: %v", err)
	}

	// List menu items
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()
	form, err := q.CreateForm(ctx, CreateFormParams{
		Name:           "Contact Form",
		Slug:           "contact",
		Title:          "Contact Us",
		Description:    sql.NullString{String: "Get in touch", Valid: true},
		SuccessMessage: sql.NullString{String: "Thank you!", Valid: true},
		EmailTo:        sql.NullString{String: "test@example.com", Valid: true},
		IsActive:       true,
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
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()

	// Create form
	form, _ := q.CreateForm(ctx, CreateFormParams{
		Name:      "Test Form",
		Slug:      "test-form",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Create fields
	_, err := q.CreateFormField(ctx, CreateFormFieldParams{
		FormID:     form.ID,
		Type:       "text",
		Name:       "name",
		Label:      "Your Name",
		IsRequired: true,
		Position:   0,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateFormField: %v", err)
	}

	_, err = q.CreateFormField(ctx, CreateFormFieldParams{
		FormID:     form.ID,
		Type:       "email",
		Name:       "email",
		Label:      "Your Email",
		IsRequired: true,
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateFormField: %v", err)
	}

	// Get fields
	fields, err := q.GetFormFields(ctx, form.ID)
	if err != nil {
		t.Fatalf("GetFormFields: %v", err)
	}

	if len(fields) != 2 {
		t.Errorf("len(fields) = %d, want 2", len(fields))
	}
}

func TestFormSubmission(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()

	// Create form
	form, _ := q.CreateForm(ctx, CreateFormParams{
		Name:      "Submission Test",
		Slug:      "submission-test",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Create submission
	sub, err := q.CreateFormSubmission(ctx, CreateFormSubmissionParams{
		FormID:    form.ID,
		Data:      `{"name":"John","email":"john@example.com"}`,
		IpAddress: sql.NullString{String: "127.0.0.1", Valid: true},
		UserAgent: sql.NullString{String: "Mozilla/5.0", Valid: true},
		IsRead:    false,
		CreatedAt: now,
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

	// Mark as read
	err = q.MarkSubmissionRead(ctx, sub.ID)
	if err != nil {
		t.Fatalf("MarkSubmissionRead: %v", err)
	}

	// Verify read status
	found, err := q.GetFormSubmissionByID(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetFormSubmissionByID: %v", err)
	}
	if !found.IsRead {
		t.Error("IsRead should be true after marking")
	}
}

func TestCountUnreadSubmissions(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	ctx := context.Background()
	q := New(db)

	now := time.Now()

	// Create form
	form, _ := q.CreateForm(ctx, CreateFormParams{
		Name:      "Count Test",
		Slug:      "count-test",
		Title:     "Test",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Create submissions
	for i := 0; i < 3; i++ {
		_, err := q.CreateFormSubmission(ctx, CreateFormSubmissionParams{
			FormID:    form.ID,
			Data:      `{"test":"data"}`,
			IsRead:    false,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateFormSubmission: %v", err)
		}
	}

	// Count unread
	count, err := q.CountUnreadSubmissions(ctx, form.ID)
	if err != nil {
		t.Fatalf("CountUnreadSubmissions: %v", err)
	}

	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}

	// Count all unread
	allCount, err := q.CountAllUnreadSubmissions(ctx)
	if err != nil {
		t.Fatalf("CountAllUnreadSubmissions: %v", err)
	}

	if allCount != 3 {
		t.Errorf("allCount = %d, want 3", allCount)
	}
}
