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
)

// TestChangedPagesListsOnlyPagesWithANewRevision checks the list callers run
// after-commit side effects for: pages that gained a revision, in ID order, as
// committed. A page the transaction only tracked, with nothing changed, is not
// in it (a no-op save has no webhook or cache invalidation to send).
func TestChangedPagesListsOnlyPagesWithANewRevision(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	insert := func(slug string) store.Page {
		t.Helper()
		page, err := f.q.CreatePage(ctx, store.CreatePageParams{Title: slug, Slug: slug, Body: "body", Status: "draft", AuthorID: f.user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	second, third, untouched := insert("second"), insert("third"), insert("untouched")

	work, err := pagewrite.InTx(ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
		// Track in reverse ID order: the result must not follow tracking order.
		for _, id := range []int64{untouched.ID, third.ID, second.ID} {
			current, err := work.TrackSystem(ctx, id)
			if err != nil {
				return err
			}
			if id == untouched.ID {
				continue
			}
			params := pagewrite.ParamsFor(current)
			params.Title += " edited"
			if _, err = work.Q.UpdatePage(ctx, params); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := work.ChangedPages()
	if len(changed) != 2 || changed[0].ID != second.ID || changed[1].ID != third.ID {
		t.Fatalf("ChangedPages() = %+v, want pages %d and %d in ID order", changed, second.ID, third.ID)
	}
	for _, page := range changed {
		if page.Revision != 2 || page.Title != page.Slug+" edited" {
			t.Errorf("ChangedPages() returned %q at revision %d, want the committed edit at revision 2", page.Title, page.Revision)
		}
	}
}
