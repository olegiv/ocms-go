// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestCompoundWritersRequireTrackedPages(t *testing.T) {
	f := setup(t)
	version, versionErr := f.q.GetPageVersionByRevision(t.Context(), store.GetPageVersionByRevisionParams{PageID: f.page.ID, Revision: sql.NullInt64{Int64: f.page.Revision, Valid: true}})
	if versionErr != nil {
		t.Fatal(versionErr)
	}
	// A legacy restore changes only content, so it isolates the helper's tracking
	// requirement from reference and route validation.
	version.Legacy = true
	version.Title = "Restored title"
	for _, tracked := range []bool{false, true} {
		_, restoreErr := pagewrite.InTx(t.Context(), f.db, provenance.Principal{UserID: f.user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
			if tracked {
				if _, err := work.TrackSystem(t.Context(), f.page.ID); err != nil {
					return err
				}
			}
			return work.Restore(t.Context(), f.page, version, "Restored body")
		})
		current, loadErr := f.q.GetPageByID(t.Context(), f.page.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if !tracked {
			if !errors.Is(restoreErr, pagewrite.ErrPageNotTracked) || current.Title != f.page.Title || current.Revision != f.page.Revision {
				t.Fatalf("untracked restore: error=%v, title=%q, revision=%d", restoreErr, current.Title, current.Revision)
			}
			var records int
			if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM content_provenance WHERE page_id = ?", f.page.ID).Scan(&records); err != nil {
				t.Fatal(err)
			}
			if records != 1 {
				t.Fatalf("refusal changed provenance: %d records", records)
			}
		} else if restoreErr != nil || current.Title != version.Title || current.Body != "Restored body" || current.Revision != f.page.Revision+1 {
			t.Fatalf("tracked restore: error=%v, page=%+v", restoreErr, current)
		}
	}

	_, trackingErr := pagewrite.InTx(t.Context(), f.db, provenance.Principal{Source: "system"}, func(work *pagewrite.Tx) error {
		for _, id := range []int64{0, f.page.ID, f.page.ID + 100} {
			if err := work.RequireTracked(id); !errors.Is(err, pagewrite.ErrPageNotTracked) || work.IsTracked(id) {
				t.Fatalf("precondition took a late snapshot or accepted %d: %v", id, err)
			}
		}
		for _, kind := range []string{"tag", "category", "form"} {
			if err := work.RequireTrackedEntity(kind, f.page.ID); err != nil {
				t.Fatalf("non-page translation %s: %v", kind, err)
			}
		}
		if err := work.RequireTrackedEntity("page", f.page.ID); !errors.Is(err, pagewrite.ErrPageNotTracked) {
			t.Fatalf("untracked translation endpoint: %v", err)
		}
		if _, err := work.TrackSystem(t.Context(), f.page.ID); err != nil {
			return err
		}
		if err := work.RequireTrackedEntity("page", f.page.ID, f.page.ID+100); !errors.Is(err, pagewrite.ErrPageNotTracked) {
			t.Fatalf("second translation endpoint was not checked: %v", err)
		}
		return work.RequireTrackedEntity("page", f.page.ID)
	})
	if trackingErr != nil {
		t.Fatal(trackingErr)
	}
}
