// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package contentpath_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestCategoryAndTagKeepTheLanguageBoundary pins the contract the public
// archive handlers rely on: a category or tag is found only under the language
// it belongs to, so a caller that got a row back never needs to compare the
// row's language to the route's again (the frontend's two such comparisons were
// unreachable, 2026-11-03 review R2 P3-10 and P3-11).
//
// Bug state: return the row whatever its language from Category or Tag and the
// other-language lookups below succeed.
func TestCategoryAndTagKeepTheLanguageBoundary(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	q := store.New(db)
	ctx := context.Background()
	now := time.Now()
	if _, err := q.CreateCategory(ctx, store.CreateCategoryParams{Name: "News", Slug: "news", LanguageCode: "en", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateTag(ctx, store.CreateTagParams{Name: "Go", Slug: "go", LanguageCode: "en", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	category, err := contentpath.Category(ctx, q, "news", "en")
	if err != nil || category.LanguageCode != "en" {
		t.Fatalf("Category in its own language: %+v %v", category, err)
	}
	tag, err := contentpath.Tag(ctx, q, "go", "en")
	if err != nil || tag.LanguageCode != "en" {
		t.Fatalf("Tag in its own language: %+v %v", tag, err)
	}
	if got, err := contentpath.Category(ctx, q, "news", "ru"); !errors.Is(err, sql.ErrNoRows) || got.ID != 0 {
		t.Errorf("Category under another language = %+v %v, want sql.ErrNoRows", got, err)
	}
	if got, err := contentpath.Tag(ctx, q, "go", "ru"); !errors.Is(err, sql.ErrNoRows) || got.ID != 0 {
		t.Errorf("Tag under another language = %+v %v, want sql.ErrNoRows", got, err)
	}
}
