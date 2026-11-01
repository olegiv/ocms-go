// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package editorial_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/editorial"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestEditorialMigrationRollbackAndExistingPagePreservation(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q, ctx := store.New(db), context.Background()
	now := time.Now()
	user, err := q.CreateUser(ctx, store.CreateUserParams{Email: "migration@example.test", Role: "admin", Name: "Migration fixture", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	page, err := q.CreatePage(ctx, store.CreatePageParams{Title: "Preserved", Slug: "preserved", Body: "<p>Existing content</p>", Status: "published", AuthorID: user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if err = editorial.New(q).SaveGuide(ctx, user.ID, "", "Disposable custom guidance"); err != nil {
		t.Fatal(err)
	}
	// Target this migration even when later authoring migrations exist.
	if err = store.RunMigrationCommand(ctx, db, "down-to", "20261101209999"); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetEditorialGuide(ctx, ""); err == nil {
		t.Fatal("editorial down migration left its table")
	}
	for range 2 {
		if err = store.Migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	current, err := q.GetPageByID(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("editorial migration changed an existing page")
	}
	guide, err := editorial.New(q).Guide(ctx, "en")
	if err != nil || strings.Contains(guide.Body, "Disposable") {
		t.Fatalf("guidance seed did not recover after up %+v %v", guide, err)
	}
	presets, err := editorial.New(q).Presets(ctx, "en")
	if err != nil || len(presets) != 6 {
		t.Fatalf("repeated migration duplicated presets %+v %v", presets, err)
	}
}
