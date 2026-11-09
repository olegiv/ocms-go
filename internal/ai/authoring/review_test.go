// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestEditorialAcknowledgmentsAreRevisionScopedAndDoNotAlterEvidence(t *testing.T) {
	f := setup(t)
	in := input("Review this article")
	in.Draft.Reported = draft.Set(draft.AgentReport{Model: draft.Set("Unverified model"), ClaimsToVerify: draft.Set([]string{"Verify this claim"}), Notes: draft.Set("Verified <script>spoof()</script>")})
	saved, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.q.GetPageByID(f.ctx, saved.PageID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := review.Load(f.ctx, f.q, p, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Claims) != 1 || before.Claims[0].Checked || before.Fact.UserID.Int64 != f.user.ID || before.Fact.Source != "api" {
		t.Fatal("review lost recorded identity/report separation")
	}
	if err = review.SetClaim(f.ctx, f.q, f.user.ID, p.ID, p.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	after, err := review.Load(f.ctx, f.q, p, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Claims[0].Checked || !reflect.DeepEqual(before.Fact, after.Fact) || !reflect.DeepEqual(before.Validated, after.Validated) || !reflect.DeepEqual(before.Reported, after.Reported) {
		t.Fatal("acknowledgment changed provenance or agent assertions")
	}
	state, err := f.q.GetPageReviewState(f.ctx, p.ID)
	if err != nil || state.UncheckedClaims != 0 {
		t.Fatal("review count did not reflect acknowledgment")
	}
	changed, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: p.ID, BaseRevision: p.Revision, Changes: draft.Changes{Summary: draft.Set("A new revision needs its own review")}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.q.GetPageByID(f.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentReview, err := review.Load(f.ctx, f.q, current, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Revision != 2 || len(currentReview.Claims) != 1 || currentReview.Claims[0].Checked {
		t.Fatal("new revision inherited old claim checks")
	}
	var conflict *pagewrite.Conflict
	if err = review.SetClaim(f.ctx, f.q, f.user.ID, p.ID, p.Revision, 0, true); !errors.As(err, &conflict) {
		t.Fatal("stale claim acknowledgment was accepted")
	}
	if err = review.SetClaim(f.ctx, f.q, f.user.ID, p.ID, current.Revision, 99, true); err == nil {
		t.Fatal("nonexistent claim accepted")
	}
	checks, err := f.q.ListClaimChecks(f.ctx, before.Fact.ID)
	if err != nil || len(checks) != 1 {
		t.Fatal("historical acknowledgment was lost")
	}
}

func TestPublicationValidationAllowsWarningsButRejectsInvalidContentAndAIAccessBypass(t *testing.T) {
	f := setup(t)
	in := input("Publishable draft")
	in.Draft.BodyMarkdown = "A safe [external reference](https://example.org)."
	in.Draft.Reported = draft.Set(draft.AgentReport{ClaimsToVerify: draft.Set([]string{"Unreviewed claim"})})
	saved, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.q.GetPageByID(f.ctx, saved.PageID)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.ValidatePublication(f.ctx, f.q, p); err != nil {
		t.Fatalf("warnings/unchecked claims blocked valid draft: %v", err)
	}
	unchanged, err := f.q.GetPageByID(f.ctx, p.ID)
	if err != nil || unchanged.Body != p.Body || unchanged.Revision != p.Revision {
		t.Fatal("publication checks rewrote authoritative content")
	}
	base := p.Revision
	work, err := pagewrite.InTx(f.ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human", Transport: "admin"}, func(tx *pagewrite.Tx) error {
		current, e := tx.Track(f.ctx, p.ID, &base, true)
		if e != nil {
			return e
		}
		params := pagewrite.ParamsFor(current)
		params.Title = ""
		params.Body = "<script>alert(1)</script>"
		if _, e = tx.Q.UpdatePage(f.ctx, params); e != nil {
			return e
		}
		return tx.Q.SetPageAIAccess(f.ctx, store.SetPageAIAccessParams{PageID: p.ID, AiEditable: false, SetBy: f.user.ID, SetAt: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = review.ValidatePublication(f.ctx, f.q, work.Pages[p.ID]); !errors.Is(err, review.ErrHardValidation) {
		t.Fatal("disabling AI access bypassed hard validation")
	}
}

func TestEditorialTranslationFreshnessUsesSameFingerprintAsAuthoring(t *testing.T) {
	f := setup(t)
	source, err := f.svc.Create(f.ctx, f.actor, input("Translation source"))
	if err != nil {
		t.Fatal(err)
	}
	in := authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: source.PageID, SourceRevision: source.Revision, LanguageCode: "ru", Draft: input("Russian review fixture").Draft}
	translated, err := f.svc.Translate(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.q.GetPageByID(f.ctx, source.PageID)
	if err != nil {
		t.Fatal(err)
	}
	assertFreshness := func(want string) {
		t.Helper()
		items, e := review.Translations(f.ctx, f.q, p)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range items {
			if item.PageID == translated.PageID {
				if item.Freshness != want {
					t.Fatalf("freshness %q, want %q", item.Freshness, want)
				}
				return
			}
		}
		t.Fatal("translation omitted")
	}
	assertFreshness("current")
	_, err = f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: p.ID, BaseRevision: p.Revision, Changes: draft.Changes{Summary: draft.Set("Changed translated source text")}})
	if err != nil {
		t.Fatal(err)
	}
	p, err = f.q.GetPageByID(f.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertFreshness("stale")
}

func TestDeletedReviewerDoesNotEraseClaimAcknowledgment(t *testing.T) {
	f := setup(t)
	in := input("Retain review history")
	in.Draft.Reported = draft.Set(draft.AgentReport{ClaimsToVerify: draft.Set([]string{"Check this"})})
	saved, err := f.svc.Create(f.ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := f.q.CreateUser(f.ctx, store.CreateUserParams{Email: "reviewer@example.test", Name: "Reviewer", Role: "editor", PasswordHash: "unused", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err = review.SetClaim(f.ctx, f.q, reviewer.ID, saved.PageID, saved.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec("DELETE FROM users WHERE id=?", reviewer.ID); err != nil {
		t.Fatal(err)
	}
	p, err := f.q.GetPageByID(f.ctx, saved.PageID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := review.Load(f.ctx, f.q, p, f.user.ID)
	if err != nil || len(r.Claims) != 1 || !r.Claims[0].Checked || r.Claims[0].CheckedBy != "" {
		t.Fatal("deleted reviewer erased or misattributed acknowledgment")
	}
}
