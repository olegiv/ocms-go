// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewTaxonomyHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewTaxonomyHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewTaxonomyHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

// Tag Tests

func TestTagCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)
	q := store.New(db)

	tag, err := q.CreateTag(context.Background(), store.CreateTagParams{Name: "Test Tag", Slug: "test-tag"})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if tag.Name != "Test Tag" || tag.Slug != "test-tag" {
		t.Errorf("got Name=%q Slug=%q, want Test Tag/test-tag", tag.Name, tag.Slug)
	}
}

func TestTagWithLanguage(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Get the default English language
	lang, err := queries.GetDefaultLanguage(context.Background())
	if err != nil {
		t.Fatalf("GetDefaultLanguage failed: %v", err)
	}

	tag, err := queries.CreateTag(context.Background(), store.CreateTagParams{
		Name:         "English Tag",
		Slug:         "english-tag",
		LanguageCode: lang.Code,
	})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	if tag.LanguageCode != lang.Code {
		t.Errorf("LanguageCode = %q, want %q", tag.LanguageCode, lang.Code)
	}
}

func TestTagList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create test tags
	for i := 1; i <= 5; i++ {
		_, err := queries.CreateTag(context.Background(), store.CreateTagParams{
			Name: "Tag " + string(rune('A'+i-1)),
			Slug: "tag-" + string(rune('a'+i-1)),
		})
		if err != nil {
			t.Fatalf("CreateTag failed: %v", err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		tags, err := queries.ListTags(context.Background(), store.ListTagsParams{
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListTags failed: %v", err)
		}
		if len(tags) != 5 {
			t.Errorf("got %d tags, want 5", len(tags))
		}
	})

	t.Run("with pagination", func(t *testing.T) {
		tags, err := queries.ListTags(context.Background(), store.ListTagsParams{
			Limit:  2,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListTags failed: %v", err)
		}
		if len(tags) != 2 {
			t.Errorf("got %d tags, want 2", len(tags))
		}
	})

	t.Run("count", func(t *testing.T) {
		count, err := queries.CountTags(context.Background())
		if err != nil {
			t.Fatalf("CountTags failed: %v", err)
		}
		if count != 5 {
			t.Errorf("count = %d, want 5", count)
		}
	})
}

func TestTagUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)
	q := store.New(db)
	ctx := context.Background()

	// Create tag, then update it, then verify
	tag, err := q.CreateTag(ctx, store.CreateTagParams{Name: "Original", Slug: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.UpdateTag(ctx, store.UpdateTagParams{ID: tag.ID, Name: "Updated", Slug: "updated"}); err != nil {
		t.Fatal(err)
	}
	if updated, err := q.GetTagByID(ctx, tag.ID); err != nil {
		t.Fatal(err)
	} else if updated.Name != "Updated" {
		t.Errorf("got %q, want Updated", updated.Name)
	}
}

func TestTagDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)
	q := store.New(db)
	ctx := context.Background()

	tag, err := q.CreateTag(ctx, store.CreateTagParams{Name: "To Delete", Slug: "to-delete"})
	if err != nil {
		t.Fatal(err)
	}
	// Delete and verify it's gone
	if err := q.DeleteTag(ctx, tag.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetTagByID(ctx, tag.ID); err == nil {
		t.Error("tag should be deleted")
	}
}

func TestTagSlugExists(t *testing.T) {
	db, _ := testHandlerSetup(t)
	q := store.New(db)
	ctx := context.Background()

	if _, err := q.CreateTag(ctx, store.CreateTagParams{Name: "Existing", Slug: "existing-tag"}); err != nil {
		t.Fatal(err)
	}

	// Test existing slug
	if count, err := q.TagSlugExists(ctx, "existing-tag"); err != nil {
		t.Fatalf("check existing: %v", err)
	} else if count == 0 {
		t.Error("slug should exist")
	}

	// Test non-existing slug
	if count, err := q.TagSlugExists(ctx, "nonexistent"); err != nil {
		t.Fatalf("check nonexistent: %v", err)
	} else if count != 0 {
		t.Error("slug should not exist")
	}
}

// Category Tests

func TestCategoryCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)
	ctx := context.Background()

	cat, err := queries.CreateCategory(ctx, store.CreateCategoryParams{Name: "Test Category", Slug: "test-category"})
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case cat.Name != "Test Category":
		t.Errorf("Name = %q, want Test Category", cat.Name)
	case cat.Slug != "test-category":
		t.Errorf("Slug = %q, want test-category", cat.Slug)
	}
}

func TestCategoryWithParent(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create parent category
	parent, err := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "Parent",
		Slug: "parent",
	})
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}

	// Create child category
	child, err := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name:     "Child",
		Slug:     "child",
		ParentID: sql.NullInt64{Int64: parent.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}

	if !child.ParentID.Valid || child.ParentID.Int64 != parent.ID {
		t.Errorf("ParentID = %v, want %d", child.ParentID, parent.ID)
	}
}

func TestCategoryList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create test categories
	for i := 1; i <= 5; i++ {
		_, err := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
			Name:     "Category " + string(rune('A'+i-1)),
			Slug:     "category-" + string(rune('a'+i-1)),
			Position: int64(i),
		})
		if err != nil {
			t.Fatalf("CreateCategory failed: %v", err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		cats, err := queries.ListCategories(context.Background())
		if err != nil {
			t.Fatalf("ListCategories failed: %v", err)
		}
		if len(cats) != 5 {
			t.Errorf("got %d categories, want 5", len(cats))
		}
	})

	t.Run("count", func(t *testing.T) {
		count, err := queries.CountCategories(context.Background())
		if err != nil {
			t.Fatalf("CountCategories failed: %v", err)
		}
		if count != 5 {
			t.Errorf("count = %d, want 5", count)
		}
	})
}

func TestCategoryUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)

	cat, createErr := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "Original Cat", Slug: "original-cat",
	})
	if createErr != nil {
		t.Fatalf("create: %v", createErr)
	}

	_, updateErr := queries.UpdateCategory(context.Background(), store.UpdateCategoryParams{
		ID: cat.ID, Name: "Updated Cat", Slug: "updated-cat",
	})
	if updateErr != nil {
		t.Fatalf("update: %v", updateErr)
	}

	updated, getErr := queries.GetCategoryByID(context.Background(), cat.ID)
	if getErr != nil {
		t.Fatalf("get: %v", getErr)
	}
	if updated.Name != "Updated Cat" {
		t.Errorf("Name = %q, want Updated Cat", updated.Name)
	}
}

func TestCategoryDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)

	cat, createErr := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "To Delete Cat", Slug: "to-delete-cat",
	})
	if createErr != nil {
		t.Fatalf("create: %v", createErr)
	}

	deleteErr := queries.DeleteCategory(context.Background(), cat.ID)
	if deleteErr != nil {
		t.Fatalf("delete: %v", deleteErr)
	}

	_, getErr := queries.GetCategoryByID(context.Background(), cat.ID)
	if getErr == nil {
		t.Error("category should not exist after deletion")
	}
}

func TestCategorySlugExists(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)

	_, createErr := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "Existing Cat", Slug: "existing-cat",
	})
	if createErr != nil {
		t.Fatal(createErr)
	}

	testCases := []struct {
		slug   string
		exists bool
	}{
		{"existing-cat", true},
		{"nonexistent-cat", false},
	}
	for _, tc := range testCases {
		count, err := queries.CategorySlugExists(context.Background(), tc.slug)
		if err != nil {
			t.Errorf("CategorySlugExists(%q): %v", tc.slug, err)
		} else if (count > 0) != tc.exists {
			t.Errorf("slug %q: got exists=%v, want %v", tc.slug, count > 0, tc.exists)
		}
	}
}

// Page-Tag/Category Association Tests

func TestPageTagAssociations(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin",
		Role:  "admin",
	})

	queries := store.New(db)

	// Create a page
	page, err := queries.CreatePage(context.Background(), store.CreatePageParams{
		Title:    "Test Page",
		Slug:     "test-page",
		Body:     "Content",
		Status:   "published",
		AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}

	// Create tags
	tag1, err := queries.CreateTag(context.Background(), store.CreateTagParams{
		Name: "Tag 1",
		Slug: "tag-1",
	})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	tag2, err := queries.CreateTag(context.Background(), store.CreateTagParams{
		Name: "Tag 2",
		Slug: "tag-2",
	})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	// Associate tags with page via raw SQL
	_, err = db.Exec("INSERT INTO page_tags (page_id, tag_id) VALUES (?, ?)", page.ID, tag1.ID)
	if err != nil {
		t.Fatalf("Insert page_tag failed: %v", err)
	}

	_, err = db.Exec("INSERT INTO page_tags (page_id, tag_id) VALUES (?, ?)", page.ID, tag2.ID)
	if err != nil {
		t.Fatalf("Insert page_tag failed: %v", err)
	}

	// Get page tags
	tags, err := queries.GetTagsForPage(context.Background(), page.ID)
	if err != nil {
		t.Fatalf("GetTagsForPage failed: %v", err)
	}

	if len(tags) != 2 {
		t.Errorf("got %d tags, want 2", len(tags))
	}
}

func TestPageCategoryAssociations(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin",
		Role:  "admin",
	})

	queries := store.New(db)

	// Create a page
	page, err := queries.CreatePage(context.Background(), store.CreatePageParams{
		Title:    "Test Page Cat",
		Slug:     "test-page-cat",
		Body:     "Content",
		Status:   "published",
		AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}

	// Create category
	cat, err := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "Test Category",
		Slug: "test-cat",
	})
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}

	// Associate category with page via raw SQL
	_, err = db.Exec("INSERT INTO page_categories (page_id, category_id) VALUES (?, ?)", page.ID, cat.ID)
	if err != nil {
		t.Fatalf("Insert page_category failed: %v", err)
	}

	// Get page categories
	cats, err := queries.GetCategoriesForPage(context.Background(), page.ID)
	if err != nil {
		t.Fatalf("GetCategoriesForPage failed: %v", err)
	}

	if len(cats) != 1 {
		t.Errorf("got %d categories, want 1", len(cats))
	}
}

func TestCategoryTreeNode(t *testing.T) {
	node := CategoryTreeNode{
		Category: store.Category{
			ID:   1,
			Name: "Parent",
			Slug: "parent",
		},
		Children: []CategoryTreeNode{
			{
				Category: store.Category{
					ID:   2,
					Name: "Child",
					Slug: "child",
				},
				Depth: 1,
			},
		},
		Depth: 0,
	}

	if node.Category.ID != 1 {
		t.Errorf("ID = %d, want 1", node.Category.ID)
	}
	if len(node.Children) != 1 {
		t.Errorf("Children length = %d, want 1", len(node.Children))
	}
	if node.Children[0].Depth != 1 {
		t.Errorf("Child depth = %d, want 1", node.Children[0].Depth)
	}
}
