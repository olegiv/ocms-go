// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"context"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestTaxonomyDeletionRevisionsAssignmentsAtomically(t *testing.T) {
	for _, kind := range []string{"tag", "category"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			now := time.Now()
			var id int64
			if kind == "tag" {
				term, err := f.q.CreateTag(ctx, store.CreateTagParams{Name: "Term", Slug: "term", LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
				id = term.ID
			} else {
				term, err := f.q.CreateCategory(ctx, store.CreateCategoryParams{Name: "Term", Slug: "term", LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
				id = term.ID
			}
			principal := provenance.Principal{UserID: f.user.ID, Source: "human"}
			_, err := pagewrite.InTx(ctx, f.db, principal, func(work *pagewrite.Tx) error {
				if _, err := work.Track(ctx, f.page.ID, &f.page.Revision, true); err != nil {
					return err
				}
				if kind == "tag" {
					return work.Q.AddTagToPage(ctx, store.AddTagToPageParams{PageID: f.page.ID, TagID: id})
				}
				return work.Q.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: f.page.ID, CategoryID: id})
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`CREATE TRIGGER fail_term_provenance BEFORE INSERT ON content_provenance
				BEGIN SELECT RAISE(FAIL, 'injected term snapshot failure'); END`); err != nil {
				t.Fatal(err)
			}
			remove := func() error {
				_, removeErr := pagewrite.InTx(ctx, f.db, principal, func(work *pagewrite.Tx) error { return work.DeleteTerm(ctx, kind, id) })
				return removeErr
			}
			if err = remove(); err == nil {
				t.Fatal("injected provenance failure succeeded")
			}
			testutil.AssertPageRevision(t, f.q, f.page.ID, 2)
			if _, err = f.db.Exec(`DROP TRIGGER fail_term_provenance`); err != nil {
				t.Fatal(err)
			}
			if err = remove(); err != nil {
				t.Fatal(err)
			}
			testutil.AssertPageRevision(t, f.q, f.page.ID, 3)
			var remaining int
			if kind == "tag" {
				tags, err := f.q.GetTagsForPage(ctx, f.page.ID)
				if err != nil {
					t.Fatal(err)
				}
				remaining = len(tags)
			} else {
				categories, err := f.q.GetCategoriesForPage(ctx, f.page.ID)
				if err != nil {
					t.Fatal(err)
				}
				remaining = len(categories)
			}
			if remaining != 0 {
				t.Fatal("deleted term assignment survived")
			}
		})
	}
}
