// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
)

// edit saves one more revision of a draft through the real service.
func (f *fixture) edit(t testing.TB, saved authoring.SaveResult, label string) (authoring.SaveResult, authoring.UpdateInput) {
	t.Helper()
	in := authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: saved.Revision, Changes: draft.Changes{Summary: draft.Set(label)}}
	next, err := f.svc.Update(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	return next, in
}

func (f *fixture) versionCount(t testing.TB, pageID int64) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM page_versions WHERE page_id=? AND legacy=0", pageID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) recentMutations(t testing.TB) int64 {
	t.Helper()
	n, err := f.q.CountRecentDraftMutations(f.ctx, store.CountRecentDraftMutationsParams{UserID: f.user.ID, Since: f.svc.Now().Add(-1 << 40)})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Fix invariant: after retention a replay of a request whose revision is gone
// is answered request_expired, never an internal error, never re-executed, and
// it still counts toward the hourly quota; the current draft stays fully usable.
// Bug state: retention that leaves the replay receipt pointing at the pruned
// revision, so the receipt read fails with sql.ErrNoRows.
func TestRetentionLeavesReplayAndLiveDraftUsable(t *testing.T) {
	f := setup(t)
	f.svc.Now = func() time.Time { return time.Now() }
	created, err := f.svc.Create(f.ctx, f.actor, input("Long-lived draft"))
	if err != nil {
		t.Fatal(err)
	}
	var firstEdit authoring.UpdateInput
	saved := created
	for i := range 25 {
		var in authoring.UpdateInput
		saved, in = f.edit(t, saved, fmt.Sprintf("Edit %d", i))
		if i == 0 {
			firstEdit = in // writes revision 2, which retention will remove
		}
	}
	if saved.Revision != 26 {
		t.Fatalf("revision %d", saved.Revision)
	}
	lastEditInput := authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: saved.Revision, Changes: draft.Changes{Summary: draft.Set("Latest edit")}}
	latest, err := f.svc.Update(f.ctx, f.actor, lastEditInput)
	if err != nil {
		t.Fatal(err)
	}
	quotaBefore := f.recentMutations(t)

	result, err := pagewrite.PruneAIDraftRevisions(f.ctx, f.db)
	if err != nil || result.Versions != 7 {
		t.Fatalf("prune = %+v, %v", result, err)
	}
	if got := f.versionCount(t, saved.PageID); got != pagewrite.AIDraftRevisionsKept {
		t.Fatalf("versions kept = %d", got)
	}
	if got := f.recentMutations(t); got != quotaBefore {
		t.Fatalf("retention changed the quota count: %d -> %d", quotaBefore, got)
	}

	// A replay of a removed revision is a tombstone: expired, not an error, and
	// it writes nothing.
	revisionsBefore := f.versionCount(t, saved.PageID)
	if _, err = f.svc.Update(f.ctx, f.actor, firstEdit); code(err) != "request_expired" {
		t.Fatalf("replay of a pruned revision: %v", err)
	}
	if f.versionCount(t, saved.PageID) != revisionsBefore {
		t.Fatal("expired replay wrote a revision")
	}
	// A replay of a kept revision still returns its receipt.
	replayed, err := f.svc.Update(f.ctx, f.actor, lastEditInput)
	if err != nil || replayed.Revision != latest.Revision {
		t.Fatalf("replay of a kept revision: %+v %v", replayed, err)
	}
	// The draft itself reads and edits as before.
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: saved.PageID})
	if err != nil || detail.Revision != latest.Revision {
		t.Fatalf("get after retention: %+v %v", detail, err)
	}
	if _, err = f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: saved.PageID, BaseRevision: latest.Revision, Changes: draft.Changes{Summary: draft.Set("After retention")}}); err != nil {
		t.Fatalf("update after retention: %v", err)
	}
	if listed, err := f.svc.List(f.ctx, f.actor, authoring.ListInput{}); err != nil || listed.Total != 1 {
		t.Fatalf("list after retention: %+v %v", listed, err)
	}
}

// Fix invariant: a revision recorded as a reference by a draft's current
// evidence outlives retention, so inheriting references keeps working. Bug
// state: pruning the cited draft's old revision made every later update of the
// citing draft fail with an internal error.
func TestRetentionKeepsReferencedRevisionForLaterUpdates(t *testing.T) {
	f := setup(t)
	cited, err := f.svc.Create(f.ctx, f.actor, input("Cited example"))
	if err != nil {
		t.Fatal(err)
	}
	citing := input("Citing draft")
	citing.Draft.References = draft.Set([]draft.Reference{{Ref: fmt.Sprint(cited.PageID), Role: "example"}})
	citer, err := f.svc.Create(f.ctx, f.actor, citing)
	if err != nil {
		t.Fatal(err)
	}
	// The citing draft recorded revision 1 of the cited draft. The cited draft
	// then moves on far enough that revision 1 is outside its newest 20.
	latest := cited
	for i := range 24 {
		latest, _ = f.edit(t, latest, fmt.Sprintf("Cited edit %d", i))
	}
	if result, pruneErr := pagewrite.PruneAIDraftRevisions(f.ctx, f.db); pruneErr != nil || result.Versions != 4 {
		t.Fatalf("prune = %+v, %v (revision 1 is referenced and must stay)", result, pruneErr)
	}
	var kept int
	if err = f.db.QueryRow("SELECT COUNT(*) FROM page_versions WHERE page_id=? AND revision=1", cited.PageID).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("referenced revision kept = %d, %v", kept, err)
	}
	// A body change without new references re-reads the example's recorded revision.
	in := authoring.UpdateInput{RequestID: uuid.NewString(), PageID: citer.PageID, BaseRevision: citer.Revision, Changes: draft.Changes{BodyMarkdown: draft.Set("## Rewritten\n\nA different safe paragraph.")}}
	if _, err = f.svc.Update(f.ctx, f.actor, in); err != nil {
		t.Fatalf("update of the citing draft after retention: %v", err)
	}
}

// A share link pins its revision through the real issuing path; a revision that
// is gone reads as unavailable everywhere (404 session preview, preview_unavailable
// from the tool, 409 in the admin UI), never as an internal error. Revoking the
// link releases the pin.
func TestShareLinkPinsItsRevisionAndPrunedRevisionsAreUnavailable(t *testing.T) {
	f := setup(t)
	created, err := f.svc.Create(f.ctx, f.actor, input("Shared draft"))
	if err != nil {
		t.Fatal(err)
	}
	saved := created
	for i := range 25 {
		saved, _ = f.edit(t, saved, fmt.Sprintf("Edit %d", i))
	}
	if _, err = f.svc.CreatePreviewLink(f.ctx, f.actor, authoring.PreviewInput{PageID: saved.PageID, Revision: 3}); err != nil {
		t.Fatal(err)
	}

	// Revision 26 is current: revisions 1..6 are old, and the pinned 3 stays.
	if result, pruneErr := pagewrite.PruneAIDraftRevisions(f.ctx, f.db); pruneErr != nil || result.Versions != 5 {
		t.Fatalf("prune = %+v, %v", result, pruneErr)
	}
	if _, err = preview.Load(f.ctx, f.q, saved.PageID, 3); err != nil {
		t.Fatalf("pinned revision lost: %v", err)
	}
	if _, err = preview.Load(f.ctx, f.q, saved.PageID, 2); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatalf("pruned revision: %v", err)
	}
	if _, err = f.svc.CreatePreviewLink(f.ctx, f.actor, authoring.PreviewInput{PageID: saved.PageID, Revision: 2}); code(err) != "preview_unavailable" {
		t.Fatalf("share link for a pruned revision: %v", err)
	}
	if _, err = f.svc.CreatePreviewLink(f.ctx, f.actor, authoring.PreviewInput{PageID: saved.PageID, Revision: saved.Revision}); err != nil {
		t.Fatalf("share link for a kept revision: %v", err)
	}

	if _, err = f.db.Exec("UPDATE preview_links SET revoked_at=unixepoch() WHERE revision=3"); err != nil {
		t.Fatal(err)
	}
	if result, pruneErr := pagewrite.PruneAIDraftRevisions(f.ctx, f.db); pruneErr != nil || result.Versions != 1 {
		t.Fatalf("prune after revocation = %+v, %v", result, pruneErr)
	}
	if _, err = preview.Load(f.ctx, f.q, saved.PageID, 3); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatalf("revoked pin still kept revision 3: %v", err)
	}
}

// If a recorded reference revision is missing anyway, the agent gets the
// existing validation error, not an internal one.
func TestMissingReferencedRevisionIsAValidationErrorNotAnInternalError(t *testing.T) {
	f := setup(t)
	cited, err := f.svc.Create(f.ctx, f.actor, input("Cited example"))
	if err != nil {
		t.Fatal(err)
	}
	citing := input("Citing draft")
	citing.Draft.References = draft.Set([]draft.Reference{{Ref: fmt.Sprint(cited.PageID), Role: "example"}})
	citer, err := f.svc.Create(f.ctx, f.actor, citing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec("DELETE FROM page_versions WHERE page_id=? AND revision=1", cited.PageID); err != nil {
		t.Fatal(err)
	}
	in := authoring.UpdateInput{RequestID: uuid.NewString(), PageID: citer.PageID, BaseRevision: citer.Revision, Changes: draft.Changes{BodyMarkdown: draft.Set("## Rewritten\n\nAnother safe paragraph.")}}
	if _, err = f.svc.Update(f.ctx, f.actor, in); code(err) != "validation_error" {
		t.Fatalf("missing referenced revision: %v", err)
	}
}
