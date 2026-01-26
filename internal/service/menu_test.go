// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestBuildMenuTree(t *testing.T) {
	svc := &MenuService{}

	now := time.Now()

	// Create flat list of menu items
	items := []store.ListMenuItemsWithPageRow{
		{
			ID:        1,
			MenuID:    1,
			Title:     "Home",
			Url:       sql.NullString{String: "/", Valid: true},
			Target:    sql.NullString{String: "_self", Valid: true},
			IsActive:  true,
			Position:  0,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        2,
			MenuID:    1,
			Title:     "About",
			Url:       sql.NullString{String: "/about", Valid: true},
			Target:    sql.NullString{String: "_self", Valid: true},
			IsActive:  true,
			Position:  1,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        3,
			MenuID:    1,
			ParentID:  sql.NullInt64{Int64: 2, Valid: true},
			Title:     "Team",
			Url:       sql.NullString{String: "/about/team", Valid: true},
			Target:    sql.NullString{String: "_self", Valid: true},
			IsActive:  true,
			Position:  0,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        4,
			MenuID:    1,
			ParentID:  sql.NullInt64{Int64: 2, Valid: true},
			Title:     "History",
			Url:       sql.NullString{String: "/about/history", Valid: true},
			Target:    sql.NullString{String: "_self", Valid: true},
			IsActive:  true,
			Position:  1,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        5,
			MenuID:    1,
			Title:     "Contact",
			Url:       sql.NullString{String: "/contact", Valid: true},
			Target:    sql.NullString{String: "_self", Valid: true},
			IsActive:  true,
			Position:  2,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	// Build tree
	tree := svc.buildMenuTree(items)

	// Verify root items
	if len(tree) != 3 {
		t.Fatalf("len(tree) = %d, want 3", len(tree))
	}

	// Verify first root item
	if tree[0].Title != "Home" {
		t.Errorf("tree[0].Title = %q, want %q", tree[0].Title, "Home")
	}
	if tree[0].URL != "/" {
		t.Errorf("tree[0].URL = %q, want %q", tree[0].URL, "/")
	}

	// Verify second root item has children
	if tree[1].Title != "About" {
		t.Errorf("tree[1].Title = %q, want %q", tree[1].Title, "About")
	}
	if len(tree[1].Children) != 2 {
		t.Fatalf("len(tree[1].Children) = %d, want 2", len(tree[1].Children))
	}

	// Verify children
	if tree[1].Children[0].Title != "Team" {
		t.Errorf("tree[1].Children[0].Title = %q, want %q", tree[1].Children[0].Title, "Team")
	}
	if tree[1].Children[1].Title != "History" {
		t.Errorf("tree[1].Children[1].Title = %q, want %q", tree[1].Children[1].Title, "History")
	}

	// Verify third root item
	if tree[2].Title != "Contact" {
		t.Errorf("tree[2].Title = %q, want %q", tree[2].Title, "Contact")
	}
}

func TestBuildMenuTreeWithInactiveItems(t *testing.T) {
	svc := &MenuService{}

	now := time.Now()

	// Create flat list with inactive items
	items := []store.ListMenuItemsWithPageRow{
		{
			ID:        1,
			MenuID:    1,
			Title:     "Home",
			Url:       sql.NullString{String: "/", Valid: true},
			IsActive:  true,
			Position:  0,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        2,
			MenuID:    1,
			Title:     "Hidden",
			Url:       sql.NullString{String: "/hidden", Valid: true},
			IsActive:  false, // Inactive
			Position:  1,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        3,
			MenuID:    1,
			Title:     "Contact",
			Url:       sql.NullString{String: "/contact", Valid: true},
			IsActive:  true,
			Position:  2,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	// Build tree
	tree := svc.buildMenuTree(items)

	// Verify inactive items are filtered out
	if len(tree) != 2 {
		t.Fatalf("len(tree) = %d, want 2 (inactive items should be filtered)", len(tree))
	}

	// Verify correct items remain
	if tree[0].Title != "Home" {
		t.Errorf("tree[0].Title = %q, want %q", tree[0].Title, "Home")
	}
	if tree[1].Title != "Contact" {
		t.Errorf("tree[1].Title = %q, want %q", tree[1].Title, "Contact")
	}
}

func TestBuildMenuTreeWithPageLinks(t *testing.T) {
	svc := &MenuService{}

	now := time.Now()

	// Create item linked to a page
	items := []store.ListMenuItemsWithPageRow{
		{
			ID:        1,
			MenuID:    1,
			Title:     "About Page",
			PageID:    sql.NullInt64{Int64: 42, Valid: true},
			PageSlug:  sql.NullString{String: "about-us", Valid: true},
			IsActive:  true,
			Position:  0,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	// Build tree
	tree := svc.buildMenuTree(items)

	if len(tree) != 1 {
		t.Fatalf("len(tree) = %d, want 1", len(tree))
	}

	// Verify page link is resolved
	if tree[0].URL != "/about-us" {
		t.Errorf("tree[0].URL = %q, want %q", tree[0].URL, "/about-us")
	}
	if tree[0].PageSlug != "about-us" {
		t.Errorf("tree[0].PageSlug = %q, want %q", tree[0].PageSlug, "about-us")
	}
}
