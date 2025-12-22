package handler

import (
	"context"
	"database/sql"
	"testing"

	"ocms-go/internal/store"
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

	queries := store.New(db)

	tag, err := queries.CreateTag(context.Background(), store.CreateTagParams{
		Name: "Test Tag",
		Slug: "test-tag",
	})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	if tag.Name != "Test Tag" {
		t.Errorf("Name = %q, want %q", tag.Name, "Test Tag")
	}
	if tag.Slug != "test-tag" {
		t.Errorf("Slug = %q, want %q", tag.Slug, "test-tag")
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
		Name:       "English Tag",
		Slug:       "english-tag",
		LanguageID: sql.NullInt64{Int64: lang.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	if !tag.LanguageID.Valid || tag.LanguageID.Int64 != lang.ID {
		t.Errorf("LanguageID = %v, want %d", tag.LanguageID, lang.ID)
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

	queries := store.New(db)

	tag, err := queries.CreateTag(context.Background(), store.CreateTagParams{
		Name: "Original",
		Slug: "original",
	})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	_, err = queries.UpdateTag(context.Background(), store.UpdateTagParams{
		ID:   tag.ID,
		Name: "Updated",
		Slug: "updated",
	})
	if err != nil {
		t.Fatalf("UpdateTag failed: %v", err)
	}

	updated, err := queries.GetTagByID(context.Background(), tag.ID)
	if err != nil {
		t.Fatalf("GetTagByID failed: %v", err)
	}

	if updated.Name != "Updated" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated")
	}
}

func TestTagDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	tag, err := queries.CreateTag(context.Background(), store.CreateTagParams{
		Name: "To Delete",
		Slug: "to-delete",
	})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	if err := queries.DeleteTag(context.Background(), tag.ID); err != nil {
		t.Fatalf("DeleteTag failed: %v", err)
	}

	_, err = queries.GetTagByID(context.Background(), tag.ID)
	if err == nil {
		t.Error("expected error when getting deleted tag")
	}
}

func TestTagSlugExists(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	_, err := queries.CreateTag(context.Background(), store.CreateTagParams{
		Name: "Existing",
		Slug: "existing-tag",
	})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}

	t.Run("exists", func(t *testing.T) {
		count, err := queries.TagSlugExists(context.Background(), "existing-tag")
		if err != nil {
			t.Fatalf("TagSlugExists failed: %v", err)
		}
		if count == 0 {
			t.Error("expected slug to exist")
		}
	})

	t.Run("not exists", func(t *testing.T) {
		count, err := queries.TagSlugExists(context.Background(), "nonexistent")
		if err != nil {
			t.Fatalf("TagSlugExists failed: %v", err)
		}
		if count != 0 {
			t.Error("expected slug to not exist")
		}
	})
}

// Category Tests

func TestCategoryCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	cat, err := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "Test Category",
		Slug: "test-category",
	})
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}

	if cat.Name != "Test Category" {
		t.Errorf("Name = %q, want %q", cat.Name, "Test Category")
	}
	if cat.Slug != "test-category" {
		t.Errorf("Slug = %q, want %q", cat.Slug, "test-category")
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

	cat, err := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "Original Cat",
		Slug: "original-cat",
	})
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}

	_, err = queries.UpdateCategory(context.Background(), store.UpdateCategoryParams{
		ID:   cat.ID,
		Name: "Updated Cat",
		Slug: "updated-cat",
	})
	if err != nil {
		t.Fatalf("UpdateCategory failed: %v", err)
	}

	updated, err := queries.GetCategoryByID(context.Background(), cat.ID)
	if err != nil {
		t.Fatalf("GetCategoryByID failed: %v", err)
	}

	if updated.Name != "Updated Cat" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated Cat")
	}
}

func TestCategoryDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	cat, err := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "To Delete Cat",
		Slug: "to-delete-cat",
	})
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}

	if err := queries.DeleteCategory(context.Background(), cat.ID); err != nil {
		t.Fatalf("DeleteCategory failed: %v", err)
	}

	_, err = queries.GetCategoryByID(context.Background(), cat.ID)
	if err == nil {
		t.Error("expected error when getting deleted category")
	}
}

func TestCategorySlugExists(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	_, err := queries.CreateCategory(context.Background(), store.CreateCategoryParams{
		Name: "Existing Cat",
		Slug: "existing-cat",
	})
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}

	t.Run("exists", func(t *testing.T) {
		count, err := queries.CategorySlugExists(context.Background(), "existing-cat")
		if err != nil {
			t.Fatalf("CategorySlugExists failed: %v", err)
		}
		if count == 0 {
			t.Error("expected slug to exist")
		}
	})

	t.Run("not exists", func(t *testing.T) {
		count, err := queries.CategorySlugExists(context.Background(), "nonexistent-cat")
		if err != nil {
			t.Fatalf("CategorySlugExists failed: %v", err)
		}
		if count != 0 {
			t.Error("expected slug to not exist")
		}
	})
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
