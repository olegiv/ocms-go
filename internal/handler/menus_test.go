// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewMenusHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewMenusHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewMenusHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestMenuCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)

	menu, err := queries.CreateMenu(context.Background(), store.CreateMenuParams{Name: "Main Menu", Slug: "main-menu"})
	if err != nil {
		t.Fatalf("CreateMenu failed: %v", err)
	}
	// Verify both fields in single block
	if got, want := menu.Name, "Main Menu"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	} else if got, want := menu.Slug, "main-menu"; got != want {
		t.Errorf("Slug = %q, want %q", got, want)
	}
}

func TestMenuList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create test menus
	menus := []string{"header", "footer", "sidebar"}
	for _, slug := range menus {
		_, err := queries.CreateMenu(context.Background(), store.CreateMenuParams{
			Name: slug + " Menu",
			Slug: slug,
		})
		if err != nil {
			t.Fatalf("CreateMenu failed: %v", err)
		}
	}

	result, err := queries.ListMenus(context.Background())
	if err != nil {
		t.Fatalf("ListMenus failed: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("got %d menus, want 3", len(result))
	}
}

func TestMenuGetBySlug(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	_, err := queries.CreateMenu(context.Background(), store.CreateMenuParams{
		Name: "Test Menu",
		Slug: "test-menu",
	})
	if err != nil {
		t.Fatalf("CreateMenu failed: %v", err)
	}

	menu, err := queries.GetMenuBySlug(context.Background(), "test-menu")
	if err != nil {
		t.Fatalf("GetMenuBySlug failed: %v", err)
	}

	if menu.Slug != "test-menu" {
		t.Errorf("Slug = %q, want %q", menu.Slug, "test-menu")
	}
}

func TestMenuUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)
	ctx := context.Background()

	// Setup: create original menu
	original, err := queries.CreateMenu(ctx, store.CreateMenuParams{Name: "Original Menu", Slug: "original-menu"})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	// Execute: update the menu
	_, err = queries.UpdateMenu(ctx, store.UpdateMenuParams{ID: original.ID, Name: "Updated Menu", Slug: "updated-menu"})
	if err != nil {
		t.Fatalf("UpdateMenu: %v", err)
	}

	// Verify: check the update was persisted
	result, err := queries.GetMenuByID(ctx, original.ID)
	if err != nil {
		t.Fatalf("GetMenuByID: %v", err)
	}
	if result.Name != "Updated Menu" {
		t.Errorf("got Name=%q, want Updated Menu", result.Name)
	}
}

func TestMenuDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)
	ctx := context.Background()

	// Setup: create a menu to delete
	toDelete, err := queries.CreateMenu(ctx, store.CreateMenuParams{Name: "To Delete", Slug: "to-delete"})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	// Execute: delete it
	err = queries.DeleteMenu(ctx, toDelete.ID)
	if err != nil {
		t.Fatalf("DeleteMenu: %v", err)
	}

	// Verify: confirm it no longer exists
	_, err = queries.GetMenuByID(ctx, toDelete.ID)
	if err == nil {
		t.Error("menu should not exist after deletion")
	}
}

func TestMenuItemCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	menu, err := queries.CreateMenu(context.Background(), store.CreateMenuParams{
		Name: "Item Test Menu",
		Slug: "item-test-menu",
	})
	if err != nil {
		t.Fatalf("CreateMenu failed: %v", err)
	}

	item, err := queries.CreateMenuItem(context.Background(), store.CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "Home",
		Url:       sql.NullString{String: "/", Valid: true},
		Target:    sql.NullString{String: "_self", Valid: true},
		CssClass:  sql.NullString{String: "", Valid: true},
		Position:  0,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem failed: %v", err)
	}

	if item.Title != "Home" {
		t.Errorf("Title = %q, want %q", item.Title, "Home")
	}
	if item.MenuID != menu.ID {
		t.Errorf("MenuID = %d, want %d", item.MenuID, menu.ID)
	}
}

func TestMenuItemWithParent(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	menu, err := queries.CreateMenu(context.Background(), store.CreateMenuParams{
		Name: "Nested Menu",
		Slug: "nested-menu",
	})
	if err != nil {
		t.Fatalf("CreateMenu failed: %v", err)
	}

	parent, err := queries.CreateMenuItem(context.Background(), store.CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "Parent",
		Url:       sql.NullString{String: "/parent", Valid: true},
		Target:    sql.NullString{String: "_self", Valid: true},
		CssClass:  sql.NullString{String: "", Valid: true},
		Position:  0,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem failed: %v", err)
	}

	child, err := queries.CreateMenuItem(context.Background(), store.CreateMenuItemParams{
		MenuID:    menu.ID,
		ParentID:  sql.NullInt64{Int64: parent.ID, Valid: true},
		Title:     "Child",
		Url:       sql.NullString{String: "/child", Valid: true},
		Target:    sql.NullString{String: "_self", Valid: true},
		CssClass:  sql.NullString{String: "", Valid: true},
		Position:  0,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem failed: %v", err)
	}

	if !child.ParentID.Valid || child.ParentID.Int64 != parent.ID {
		t.Errorf("ParentID = %v, want %d", child.ParentID, parent.ID)
	}
}

func TestMenuItemList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	menu, err := queries.CreateMenu(context.Background(), store.CreateMenuParams{
		Name: "List Items Menu",
		Slug: "list-items-menu",
	})
	if err != nil {
		t.Fatalf("CreateMenu failed: %v", err)
	}

	// Create menu items
	items := []string{"Home", "About", "Contact"}
	for i, title := range items {
		_, err := queries.CreateMenuItem(context.Background(), store.CreateMenuItemParams{
			MenuID:    menu.ID,
			Title:     title,
			Url:       sql.NullString{String: "/" + title, Valid: true},
			Target:    sql.NullString{String: "_self", Valid: true},
			CssClass:  sql.NullString{String: "", Valid: true},
			Position:  int64(i),
			IsActive:  true,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateMenuItem failed: %v", err)
		}
	}

	result, err := queries.ListMenuItems(context.Background(), menu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems failed: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("got %d items, want 3", len(result))
	}
}

func TestMenuItemUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	menu, err := queries.CreateMenu(context.Background(), store.CreateMenuParams{
		Name: "Update Item Menu",
		Slug: "update-item-menu",
	})
	if err != nil {
		t.Fatalf("CreateMenu failed: %v", err)
	}

	item, err := queries.CreateMenuItem(context.Background(), store.CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "Original Title",
		Url:       sql.NullString{String: "/original", Valid: true},
		Target:    sql.NullString{String: "_self", Valid: true},
		CssClass:  sql.NullString{String: "", Valid: true},
		Position:  0,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem failed: %v", err)
	}

	_, err = queries.UpdateMenuItem(context.Background(), store.UpdateMenuItemParams{
		ID:        item.ID,
		Title:     "Updated Title",
		Url:       sql.NullString{String: "/updated", Valid: true},
		Target:    sql.NullString{String: "_self", Valid: true},
		CssClass:  sql.NullString{String: "", Valid: true},
		Position:  1,
		IsActive:  true,
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateMenuItem failed: %v", err)
	}

	updated, err := queries.GetMenuItemByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetMenuItemByID failed: %v", err)
	}

	if updated.Title != "Updated Title" {
		t.Errorf("Title = %q, want %q", updated.Title, "Updated Title")
	}
}

func TestMenuItemDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)
	now := time.Now()

	menu, err := queries.CreateMenu(context.Background(), store.CreateMenuParams{
		Name: "Delete Item Menu",
		Slug: "delete-item-menu",
	})
	if err != nil {
		t.Fatalf("CreateMenu failed: %v", err)
	}

	item, err := queries.CreateMenuItem(context.Background(), store.CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "To Delete",
		Url:       sql.NullString{String: "/delete", Valid: true},
		Target:    sql.NullString{String: "_self", Valid: true},
		CssClass:  sql.NullString{String: "", Valid: true},
		Position:  0,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem failed: %v", err)
	}

	if err := queries.DeleteMenuItem(context.Background(), item.ID); err != nil {
		t.Fatalf("DeleteMenuItem failed: %v", err)
	}

	_, err = queries.GetMenuItemByID(context.Background(), item.ID)
	if err == nil {
		t.Error("expected error when getting deleted menu item")
	}
}

func TestMenuSlugExists(t *testing.T) {
	db, _ := testHandlerSetup(t)
	queries := store.New(db)
	ctx := context.Background()

	// Create menu to test against
	_, err := queries.CreateMenu(ctx, store.CreateMenuParams{Name: "Existing Menu", Slug: "existing-menu"})
	if err != nil {
		t.Fatalf("setup CreateMenu: %v", err)
	}

	// Subtest for existing slug
	t.Run("exists", func(t *testing.T) {
		count, checkErr := queries.MenuSlugExists(ctx, "existing-menu")
		if checkErr != nil {
			t.Fatal(checkErr)
		}
		if count == 0 {
			t.Error("slug should exist")
		}
	})

	// Subtest for non-existing slug
	t.Run("not exists", func(t *testing.T) {
		count, checkErr := queries.MenuSlugExists(ctx, "nonexistent-menu")
		if checkErr != nil {
			t.Fatal(checkErr)
		}
		if count != 0 {
			t.Error("slug should not exist")
		}
	})
}
