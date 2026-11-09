// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func staleReviewFixture(t *testing.T) (*fixture, store.Page, store.Page) {
	t.Helper()
	f := setup(t)
	source, err := f.svc.Create(f.ctx, f.actor, input("Review source"))
	if err != nil {
		t.Fatal(err)
	}
	targetInput := input("Reviewed translation").Draft
	targetInput.Reported = draft.Set(draft.AgentReport{ClaimsToVerify: draft.Set([]string{"Check the translation"})})
	target, err := f.svc.Translate(f.ctx, f.actor, authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: source.PageID, SourceRevision: source.Revision, LanguageCode: "ru", Draft: targetInput})
	if err != nil {
		t.Fatal(err)
	}
	sourcePage, err := f.q.GetPageByID(f.ctx, source.PageID)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: sourcePage.ID, BaseRevision: sourcePage.Revision, Changes: draft.Changes{Summary: draft.Set("Reviewed source text changed")}})
	if err != nil {
		t.Fatal(err)
	}
	sourcePage, err = f.q.GetPageByID(f.ctx, changed.PageID)
	if err != nil {
		t.Fatal(err)
	}
	targetPage, err := f.q.GetPageByID(f.ctx, target.PageID)
	if err != nil {
		t.Fatal(err)
	}
	return f, sourcePage, targetPage
}

func reviewInput(source, target store.Page) review.TranslationReviewInput {
	return review.TranslationReviewInput{PageID: target.ID, Revision: target.Revision, SourcePageID: source.ID, SourceRevision: source.Revision}
}

func TestTranslationReviewRefreshesBasisWithoutRewritingContentOrHistory(t *testing.T) {
	f, source, target := staleReviewFixture(t)
	before, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.ID})
	if err != nil || before.TranslationStatus != "stale" {
		t.Fatalf("initial status: %+v %v", before, err)
	}
	if err = review.SetClaim(f.ctx, f.q, f.user.ID, target.ID, target.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	factBefore, err := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: target.ID, Revision: target.Revision})
	if err != nil {
		t.Fatal(err)
	}
	panel, err := review.Load(f.ctx, f.q, target, f.user.ID)
	if err != nil || panel.TranslationSource == nil || panel.TranslationSource.Revision != source.Revision {
		t.Fatalf("source for review: %+v %v", panel, err)
	}
	reviewed, err := review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, target))
	if err != nil {
		t.Fatal(err)
	}
	want := target
	want.Revision, want.UpdatedAt = reviewed.Revision, reviewed.UpdatedAt
	if reviewed.Revision != target.Revision+1 || !reflect.DeepEqual(reviewed, want) {
		t.Fatal("review altered article fields or lifecycle")
	}
	testutil.AssertPageRevision(t, f.q, target.ID, reviewed.Revision)
	after, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.ID})
	if err != nil || after.TranslationStatus != "current" || after.TranslationBasis.SourceRevision != source.Revision || after.TranslationBasis.Fingerprint != provenance.PageFingerprint(source) {
		t.Fatalf("freshness after review: %+v %v", after, err)
	}
	panel, err = review.Load(f.ctx, f.q, reviewed, f.user.ID)
	if err != nil || panel.Fact.Source != "human" || panel.Fact.Transport != "translation_review" || panel.Fact.UserID.Int64 != f.user.ID || panel.Validated.TranslationReviewedRevision != target.Revision || len(panel.Claims) != 1 || panel.Claims[0].Checked {
		t.Fatalf("review attribution: %+v %v", panel, err)
	}
	factAfter, err := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: target.ID, Revision: target.Revision})
	if err != nil || !reflect.DeepEqual(factBefore, factAfter) {
		t.Fatal("old provenance changed")
	}
	checks, err := f.q.ListClaimChecks(f.ctx, factBefore.ID)
	if err != nil || len(checks) != 1 {
		t.Fatal("old acknowledgment was lost")
	}
	unchangedSource, err := f.q.GetPageByID(f.ctx, source.ID)
	if err != nil || !reflect.DeepEqual(source, unchangedSource) {
		t.Fatal("review changed the source")
	}
	if err = review.ValidatePublication(f.ctx, f.q, reviewed); err != nil {
		t.Fatalf("valid reviewed draft failed existing publication checks: %v", err)
	}
	// A source edit requires a new review even when the target is unchanged.
	changed, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: source.ID, BaseRevision: source.Revision, Changes: draft.Changes{MetaTitle: draft.SetNullable("Changed source SEO")}})
	if err != nil {
		t.Fatal(err)
	}
	after, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.ID})
	if err != nil || after.TranslationStatus != "stale" {
		t.Fatalf("source changes stopped invalidating review: %+v %v", after, err)
	}
	source.Revision = changed.Revision
	reviewedAgain, err := review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, reviewed))
	if err != nil || reviewedAgain.Revision != reviewed.Revision+1 {
		t.Fatalf("changed source review did not add a revision: %+v %v", reviewedAgain, err)
	}
	// A later translation edit keeps the source basis but requires its own review.
	updated, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: reviewedAgain.ID, BaseRevision: reviewedAgain.Revision, Changes: draft.Changes{Summary: draft.Set("Updated translation summary")}})
	if err != nil {
		t.Fatal(err)
	}
	after, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.ID})
	if err != nil || after.TranslationStatus != "current" {
		t.Fatalf("ordinary update lost reviewed basis: %+v %v", after, err)
	}
	reviewedAgain.Revision = updated.Revision
	reviewed, err = review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, reviewedAgain))
	if err != nil || reviewed.Revision != updated.Revision+1 {
		t.Fatalf("changed translation review did not add a revision: %+v %v", reviewed, err)
	}
}

func TestTranslationReviewPreservesEvidence(t *testing.T) {
	f, source, target := staleReviewFixture(t)
	var words []string
	for i := range 100 {
		words = append(words, fmt.Sprintf("word%d", i))
	}
	exampleInput := input("Grounding example")
	exampleInput.Draft.BodyMarkdown = strings.Join(words, " ")
	example, err := f.svc.Create(f.ctx, f.actor, exampleInput)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: target.ID, BaseRevision: target.Revision,
		Changes: draft.Changes{
			BodyMarkdown: draft.Set("This [external link](https://example.org/) is evidence."),
			References:   draft.Set([]draft.Reference{{Ref: strconv.FormatInt(example.PageID, 10), Role: "example"}}),
		}})
	if err != nil {
		t.Fatal(err)
	}
	target.Revision = updated.Revision
	// The review must preserve even validation fields it does not interpret.
	if _, err = f.db.ExecContext(f.ctx, `UPDATE content_provenance SET validated_json = json_set(validated_json, '$.future_validation', json('{"count":9007199254740993}')) WHERE page_id = ? AND revision = ?`, target.ID, target.Revision); err != nil {
		t.Fatal(err)
	}
	loadEvidence := func(revision int64) (map[string]json.RawMessage, string) {
		t.Helper()
		fact, loadErr := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: target.ID, Revision: revision})
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		var fields map[string]json.RawMessage
		if decodeErr := json.Unmarshal([]byte(fact.ValidatedJson), &fields); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		delete(fields, "translation_basis")
		delete(fields, "translation_reviewed_revision")
		delete(fields, "translation_status")
		return fields, fact.ReportedJson
	}
	before, reported := loadEvidence(target.Revision)
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.ID})
	if err != nil || detail.TranslationStatus != "stale" || detail.Validated.TranslationStatus != "stale" {
		t.Fatalf("expected stale live and validated status before review, got %q and %q: %v", detail.TranslationStatus, detail.Validated.TranslationStatus, err)
	}
	reviewed, err := review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, target))
	if err != nil {
		t.Fatal(err)
	}
	detail, err = f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.ID})
	if err != nil || detail.TranslationStatus != "current" || detail.Validated.TranslationStatus != "current" {
		t.Errorf("expected current live and validated status after review, got %q and %q: %v", detail.TranslationStatus, detail.Validated.TranslationStatus, err)
	}
	after, afterReported := loadEvidence(reviewed.Revision)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("review changed unrelated validation fields: before=%v after=%v", before, after)
	}
	if afterReported != reported {
		t.Error("review changed the agent report")
	}
	result, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: target.ID, BaseRevision: reviewed.Revision,
		Changes: draft.Changes{BodyMarkdown: draft.Set(exampleInput.Draft.BodyMarkdown)}})
	if err != nil || !slices.ContainsFunc(result.Issues, func(issue draft.Issue) bool { return issue.Code == "overlap_with_example" }) {
		t.Fatalf("next agent edit lost the example overlap check: %+v, %v", result.Issues, err)
	}
}

func TestTranslationReviewRepairsLegacyStatusOnce(t *testing.T) {
	f, source, target := staleReviewFixture(t)
	reviewed, err := review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, target))
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the stale status previously saved alongside a current basis.
	if _, err = f.db.ExecContext(f.ctx, `UPDATE content_provenance SET validated_json=json_set(validated_json, '$.translation_status', 'stale') WHERE page_id=? AND revision=?`, reviewed.ID, reviewed.Revision); err != nil {
		t.Fatal(err)
	}
	repaired, err := review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, reviewed))
	if err != nil || repaired.Revision != reviewed.Revision+1 {
		t.Fatalf("legacy status repair did not add a revision: %+v %v", repaired, err)
	}
	detail, err := f.svc.Get(f.ctx, f.actor, authoring.PageInput{PageID: target.ID})
	if err != nil || detail.TranslationStatus != "current" || detail.Validated.TranslationStatus != "current" {
		t.Fatalf("legacy status repair yielded live and validated status %q and %q: %v", detail.TranslationStatus, detail.Validated.TranslationStatus, err)
	}
	prior, err := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: reviewed.ID, Revision: reviewed.Revision})
	if err != nil {
		t.Fatal(err)
	}
	var validated authoring.Validation
	if err = json.Unmarshal([]byte(prior.ValidatedJson), &validated); err != nil || validated.TranslationStatus != "stale" {
		t.Fatalf("legacy evidence was rewritten: %q %v", prior.ValidatedJson, err)
	}
	repeated, err := review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, repaired))
	if err != nil || !reflect.DeepEqual(repeated, repaired) {
		t.Fatalf("same editor repeat after repair returned revision %d, want %d: %v", repeated.Revision, repaired.Revision, err)
	}
	testutil.AssertPageRevision(t, f.q, target.ID, repaired.Revision)
}

func TestTranslationReviewPreservesSiblingRevisionsAndRecordsAnotherEditor(t *testing.T) {
	f, source, target := staleReviewFixture(t)
	now := time.Now()
	_, err := f.q.CreateLanguage(f.ctx, store.CreateLanguageParams{Code: "de", Name: "German", NativeName: "Deutsch", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := f.svc.Translate(f.ctx, f.actor, authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: source.ID, SourceRevision: source.Revision, LanguageCode: "de", Draft: input("German sibling").Draft})
	if err != nil {
		t.Fatal(err)
	}
	source, err = f.q.GetPageByID(f.ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	target, err = f.q.GetPageByID(f.ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.q.GetPageByID(f.ctx, sibling.PageID)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := f.q.CreateUser(f.ctx, store.CreateUserParams{Email: "reviewer@example.test", Name: "Editorial reviewer", Role: "editor", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	first, err := review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, target))
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := review.MarkTranslationReviewed(f.ctx, f.q, editor.ID, reviewInput(source, first))
	if err != nil || reviewed.Revision != first.Revision+1 || reviewed.AuthorID != target.AuthorID {
		t.Fatalf("second editor did not add a review revision with the same owner: %+v %v", reviewed, err)
	}
	panel, err := review.Load(f.ctx, f.q, reviewed, f.user.ID)
	if err != nil || panel.Fact.UserID.Int64 != editor.ID || panel.ActorName != editor.Name {
		t.Fatalf("wrong reviewer: %+v %v", panel, err)
	}
	for _, original := range []store.Page{source, other} {
		current, loadErr := f.q.GetPageByID(f.ctx, original.ID)
		if loadErr != nil || !reflect.DeepEqual(original, current) {
			t.Fatalf("review changed sibling/source %d", original.ID)
		}
	}
}

func TestTranslationReviewRejectsChangedPagesAndUnavailableSources(t *testing.T) {
	for _, scenario := range []string{"target changed", "source changed", "wrong source", "unlinked", "same language source", "inactive source", "inactive target", "deleted source", "self reference", "published", "scheduled", "demoted reviewer", "missing reviewer", "invalid revision"} {
		t.Run(scenario, func(t *testing.T) {
			f, source, target := staleReviewFixture(t)
			in := reviewInput(source, target)
			var err error
			switch scenario {
			case "target changed":
				_, err = f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: target.ID, BaseRevision: target.Revision, Changes: draft.Changes{Summary: draft.Set("Concurrent translation edit")}})
			case "source changed":
				_, err = f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: source.ID, BaseRevision: source.Revision, Changes: draft.Changes{Summary: draft.Set("Concurrent source edit")}})
			case "wrong source":
				in.SourcePageID = target.ID
			case "unlinked":
				_, err = f.db.Exec("DELETE FROM translations")
			case "same language source":
				_, err = f.db.Exec("UPDATE pages SET language_code=? WHERE id=?", target.LanguageCode, source.ID)
			case "inactive source":
				_, err = f.db.Exec("UPDATE languages SET is_active=0 WHERE code='en'")
			case "inactive target":
				_, err = f.db.Exec("UPDATE languages SET is_active=0 WHERE code=?", target.LanguageCode)
			case "deleted source":
				_, err = f.db.Exec("DELETE FROM pages WHERE id=?", source.ID)
			case "self reference":
				_, err = f.db.Exec(`UPDATE content_provenance SET validated_json=json_set(validated_json, '$.translation_basis.source_page_id', ?)
					WHERE page_id=? AND revision=?`, target.ID, target.ID, target.Revision)
				in.SourcePageID, in.SourceRevision = target.ID, target.Revision
			case "published":
				_, err = f.db.Exec("UPDATE pages SET status='published' WHERE id=?", target.ID)
			case "scheduled":
				_, err = f.db.Exec("UPDATE pages SET scheduled_at=? WHERE id=?", time.Now().Add(time.Hour), target.ID)
			case "demoted reviewer":
				_, err = f.db.Exec("UPDATE users SET role='public' WHERE id=?", f.user.ID)
			case "missing reviewer":
				f.user.ID = 999999
			case "invalid revision":
				in.SourceRevision = 0
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.q.GetPageByID(f.ctx, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "published" || scenario == "scheduled" {
				panel, loadErr := review.Load(f.ctx, f.q, before, f.user.ID)
				if loadErr != nil || panel.TranslationSource != nil {
					t.Fatalf("non-draft review panel offered a source: %v, %v", panel.TranslationSource, loadErr)
				}
			}
			fact, err := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: before.ID, Revision: before.Revision})
			if err != nil {
				t.Fatal(err)
			}
			_, err = review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, in)
			var conflict *pagewrite.Conflict
			switch scenario {
			case "target changed", "source changed":
				if !errors.As(err, &conflict) {
					t.Fatalf("want revision conflict, got %v", err)
				}
			case "demoted reviewer", "missing reviewer":
				if !errors.Is(err, pagewrite.ErrEditorRequired) {
					t.Fatalf("want editor refusal, got %v", err)
				}
			default:
				if !errors.Is(err, review.ErrTranslationReviewUnavailable) {
					t.Fatalf("want unavailable, got %v", err)
				}
			}
			after, loadErr := f.q.GetPageByID(f.ctx, target.ID)
			if loadErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("refused review changed target")
			}
			afterFact, loadErr := f.q.GetContentProvenance(f.ctx, store.GetContentProvenanceParams{PageID: before.ID, Revision: before.Revision})
			if loadErr != nil || !reflect.DeepEqual(fact, afterFact) {
				t.Fatal("refused review changed evidence")
			}
		})
	}
}

func TestTranslationReviewRollsBackFailedEvidenceAndWorksAfterHumanEdits(t *testing.T) {
	f, source, target := staleReviewFixture(t)
	_, err := f.db.Exec(`CREATE TRIGGER refuse_translation_review BEFORE INSERT ON content_provenance WHEN NEW.transport='translation_review' BEGIN SELECT RAISE(ABORT,'fixture rollback'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, target)); err == nil {
		t.Fatal("failed provenance insert accepted")
	}
	testutil.AssertPageRevision(t, f.q, target.ID, target.Revision)
	if _, err = f.db.Exec("DROP TRIGGER refuse_translation_review"); err != nil {
		t.Fatal(err)
	}
	// A normal human edit has no agent evidence of its own. The recorded
	// translation basis still resolves, including when AI access is disabled.
	work, err := pagewrite.InTx(f.ctx, f.db, provenance.Principal{UserID: f.user.ID, Source: "human", Transport: "admin"}, func(work *pagewrite.Tx) error {
		current, e := work.Track(f.ctx, target.ID, &target.Revision, true)
		if e != nil {
			return e
		}
		params := pagewrite.ParamsFor(current)
		params.Summary = "Human-reviewed translation"
		if _, e = work.Q.UpdatePage(f.ctx, params); e != nil {
			return e
		}
		return work.Q.SetPageAIAccess(f.ctx, store.SetPageAIAccessParams{PageID: target.ID, AiEditable: false, SetBy: f.user.ID, SetAt: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	target = work.Pages[target.ID]
	reviewed, err := review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, target))
	if err != nil || reviewed.Revision != target.Revision+1 {
		t.Fatalf("human-edited translation review: %+v %v", reviewed, err)
	}
	items, err := review.Translations(f.ctx, f.q, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.PageID == target.ID && item.Freshness != "current" {
			t.Fatal("editorial freshness did not refresh")
		}
	}
	access, err := f.q.GetPageAIAccess(f.ctx, target.ID)
	if err != nil || access.AiEditable {
		t.Fatal("review changed AI access")
	}
	// A successful review is not authorization to publish invalid content.
	_, err = f.db.Exec("UPDATE pages SET body='<script>bad()</script>' WHERE id=?", target.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.q.GetPageByID(f.ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.ValidatePublication(f.ctx, f.q, current); !errors.Is(err, review.ErrHardValidation) {
		t.Fatalf("review bypassed publication gate: %v", err)
	}
	if _, err = review.MarkTranslationReviewed(f.ctx, f.q, f.user.ID, reviewInput(source, target)); err == nil {
		t.Fatal("old form replay was accepted")
	}
}
