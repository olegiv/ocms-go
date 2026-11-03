// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring_test

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/ai/authoring"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// seedPage inserts a page directly, which is far cheaper than a real write and
// stays outside the hourly mutation quota. Only the columns the limit reads
// matter: author, status, schedule and AI access.
func (f *fixture) seedPage(t testing.TB, author int64, status string, scheduled, ai bool) int64 {
	t.Helper()
	now := time.Now().UTC()
	params := store.CreatePageParams{Title: "Seed", Slug: "seed-" + uuid.NewString(), Body: "<p>Seed</p>", Status: status, AuthorID: author, LanguageCode: "en", PageType: "page", CreatedAt: now, UpdatedAt: now}
	if scheduled {
		params.ScheduledAt = sql.NullTime{Time: now.Add(24 * time.Hour), Valid: true}
	}
	page, err := f.q.CreatePage(f.ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if ai {
		if err = f.q.SetPageAIAccess(f.ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: author, SetAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	return page.ID
}

func (f *fixture) seedDrafts(t testing.TB, n int) []int64 {
	t.Helper()
	ids := make([]int64, 0, n)
	for range n {
		ids = append(ids, f.seedPage(t, f.user.ID, "draft", false, true))
	}
	return ids
}

func (f *fixture) ownDrafts(t testing.TB) int64 {
	t.Helper()
	n, err := f.q.CountOwnAIDrafts(f.ctx, store.CountOwnAIDraftsParams{UserID: f.user.ID, LanguageCode: ""})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) tableCount(t testing.TB, table string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Fix invariant: a user holds at most MaxOwnAIDrafts unpublished AI drafts.
// Bug state: no count in the create transaction, so the 51st create succeeds.
func TestCreateDraftRefusesTheDraftAfterTheLimit(t *testing.T) {
	f := setup(t)
	f.seedDrafts(t, authoring.MaxOwnAIDrafts-2)

	// 49 existing: the 50th draft is allowed.
	first, err := f.svc.Create(f.ctx, f.actor, input("The forty-ninth draft"))
	if err != nil {
		t.Fatalf("draft 49: %v", err)
	}
	// 50 would exist after this one: still allowed.
	lastInput := input("The fiftieth draft")
	last, err := f.svc.Create(f.ctx, f.actor, lastInput)
	if err != nil {
		t.Fatalf("draft 50: %v", err)
	}
	if got := f.ownDrafts(t); got != authoring.MaxOwnAIDrafts {
		t.Fatalf("own drafts = %d", got)
	}

	// The 51st is refused with a machine code that waiting cannot clear.
	pagesBefore, keysBefore, versionsBefore := f.tableCount(t, "pages"), f.tableCount(t, "idempotency_keys"), f.tableCount(t, "page_versions")
	_, err = f.svc.Create(f.ctx, f.actor, input("The fifty-first draft"))
	var refused *authoring.Error
	if !errors.As(err, &refused) || refused.Code != "draft_limit_reached" || refused.Retryable || refused.RetryAfterSeconds != 0 {
		t.Fatalf("draft 51: %#v", err)
	}
	if f.tableCount(t, "pages") != pagesBefore || f.tableCount(t, "idempotency_keys") != keysBefore || f.tableCount(t, "page_versions") != versionsBefore {
		t.Fatal("a refused create left rows behind or spent quota")
	}

	// Updates to existing drafts still work at the limit.
	updated, err := f.svc.Update(f.ctx, f.actor, authoring.UpdateInput{RequestID: uuid.NewString(), PageID: first.PageID, BaseRevision: first.Revision, Changes: draft.Changes{Summary: draft.Set("Edited at the limit")}})
	if err != nil || updated.Revision != first.Revision+1 {
		t.Fatalf("update at the limit: %+v %v", updated, err)
	}
	// A lost response is replayed from its receipt, not refused by the count.
	replayed, err := f.svc.Create(f.ctx, f.key(t), lastInput)
	if err != nil || replayed.PageID != last.PageID {
		t.Fatalf("replay at the limit: %+v %v", replayed, err)
	}
}

func TestCreateTranslationDraftIsRefusedAtTheLimit(t *testing.T) {
	f := setup(t)
	source, err := f.svc.Create(f.ctx, f.actor, input("Source before the limit"))
	if err != nil {
		t.Fatal(err)
	}
	published := "published"
	human := v2.ActorForUser(f.user, []string{model.PermissionPagesWrite})
	if _, err = f.pages.Update(f.ctx, human, source.PageID, pages.UpdatePageBody{BaseRevision: &source.Revision, Status: &published}); err != nil {
		t.Fatal(err)
	}
	// Publishing frees the source's slot: it is no longer an unpublished draft.
	f.seedDrafts(t, authoring.MaxOwnAIDrafts)
	latest, err := f.q.GetPageByID(f.ctx, source.PageID)
	if err != nil {
		t.Fatal(err)
	}
	in := authoring.TranslateInput{RequestID: uuid.NewString(), SourcePageID: source.PageID, SourceRevision: latest.Revision, LanguageCode: "ru", Draft: input("Перевод").Draft}
	pagesBefore := f.tableCount(t, "pages")
	if _, err = f.svc.Translate(f.ctx, f.actor, in); code(err) != "draft_limit_reached" {
		t.Fatalf("translation at the limit: %v", err)
	}
	if f.tableCount(t, "pages") != pagesBefore {
		t.Fatal("refused translation created a page")
	}
	// Freeing one slot lets the same request through.
	if _, err = f.db.Exec("UPDATE page_ai_access SET ai_editable=0 WHERE page_id=(SELECT MIN(page_id) FROM page_ai_access WHERE page_id!=?)", source.PageID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Translate(f.ctx, f.actor, in); err != nil {
		t.Fatalf("translation below the limit: %v", err)
	}
}

// The limit uses the definition behind list_my_drafts: owned, AI access on,
// status draft, unscheduled. Anything else does not occupy a slot.
func TestDraftLimitCountsOnlyOwnUnpublishedAIDrafts(t *testing.T) {
	f := setup(t)
	now := time.Now()
	other, err := f.q.CreateUser(f.ctx, store.CreateUserParams{Email: "other-" + uuid.NewString() + "@example.test", Name: "Other", Role: "editor", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	f.seedDrafts(t, authoring.MaxOwnAIDrafts-1)
	for range 3 {
		f.seedPage(t, f.user.ID, "published", false, true)  // published
		f.seedPage(t, f.user.ID, "draft", false, false)     // human draft, no AI access
		f.seedPage(t, f.user.ID, "draft", true, true)       // scheduled
		f.seedPage(t, other.ID, "draft", false, true)       // another user's AI draft
		f.seedPage(t, other.ID, "published", false, true)   // another user's published page
		f.seedPage(t, other.ID, "draft", true, true)        // another user's scheduled page
		f.seedPage(t, other.ID, "draft", false, false)      // another user's human draft
		f.seedPage(t, f.user.ID, "archived", false, true)   // not a draft
		f.seedPage(t, other.ID, "archived", false, true)    // not a draft
		f.seedPage(t, f.user.ID, "published", false, false) // plain published page
	}
	if got := f.ownDrafts(t); got != authoring.MaxOwnAIDrafts-1 {
		t.Fatalf("fixture counts %d own AI drafts", got)
	}
	if _, err = f.svc.Create(f.ctx, f.actor, input("Fills the last slot")); err != nil {
		t.Fatalf("non-draft pages occupied slots: %v", err)
	}
	if _, err = f.svc.Create(f.ctx, f.actor, input("One too many")); code(err) != "draft_limit_reached" {
		t.Fatalf("limit: %v", err)
	}
	// Publishing one frees a slot; so does ending its AI access.
	for _, statement := range []string{
		"UPDATE pages SET status='published' WHERE id=(SELECT MIN(p.id) FROM pages p JOIN page_ai_access a ON a.page_id=p.id AND a.ai_editable=1 WHERE p.author_id=? AND p.status='draft' AND p.scheduled_at IS NULL)",
		"UPDATE page_ai_access SET ai_editable=0 WHERE page_id=(SELECT MIN(p.id) FROM pages p JOIN page_ai_access a ON a.page_id=p.id AND a.ai_editable=1 WHERE p.author_id=? AND p.status='draft' AND p.scheduled_at IS NULL)",
	} {
		if _, err = f.db.Exec(statement, f.user.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = f.svc.Create(f.ctx, f.actor, input("Uses the freed slot")); err != nil {
			t.Fatalf("freed slot unusable: %v", err)
		}
		if _, err = f.svc.Create(f.ctx, f.actor, input("Over again")); code(err) != "draft_limit_reached" {
			t.Fatalf("limit after reuse: %v", err)
		}
	}
}

// Concurrent creates serialize in the write transaction, so racing past the
// count is impossible: the limit holds exactly.
func TestConcurrentCreatesCannotExceedTheDraftLimit(t *testing.T) {
	f := setup(t)
	f.seedDrafts(t, authoring.MaxOwnAIDrafts-2)
	const racers = 6
	results := make(chan error, racers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range racers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := f.svc.Create(f.ctx, f.key(t), input(fmt.Sprintf("Racing draft %d", i)))
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	created := 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case code(err) == "draft_limit_reached" || code(err) == "write_retryable":
		default:
			t.Fatal(err)
		}
	}
	if created == 0 || created > 2 || f.ownDrafts(t) != int64(authoring.MaxOwnAIDrafts-2+created) {
		t.Fatalf("created %d, own drafts %d", created, f.ownDrafts(t))
	}
	if f.ownDrafts(t) > authoring.MaxOwnAIDrafts {
		t.Fatalf("limit exceeded: %d", f.ownDrafts(t))
	}
}
