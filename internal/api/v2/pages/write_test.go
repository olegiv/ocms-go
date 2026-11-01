// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

type writeFixture struct {
	db    *sql.DB
	q     *store.Queries
	svc   *pages.Service
	actor v2.Actor
	human v2.Actor
}

func setupWrites(t *testing.T) writeFixture {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q := store.New(db)
	now := time.Now()
	ctx := context.Background()
	user, err := q.CreateUser(ctx, store.CreateUserParams{Email: "agent@example.test", PasswordHash: "unused", Role: "editor", Name: "Agent", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	permissions := []string{model.PermissionMCPAccess, model.PermissionMCPDrafts}
	encoded, err := json.Marshal(permissions)
	if err != nil {
		t.Fatal(err)
	}
	key, err := q.CreateAPIKey(ctx, store.CreateAPIKeyParams{Name: "Fixture", KeyHash: "unused", KeyPrefix: "test", Permissions: string(encoded), IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now, ExpiresAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	return writeFixture{db, q, pages.NewService(db, q, nil, nil, pages.Policy{}), v2.Actor{APIKey: &key, Permissions: permissions}, v2.ActorForUser(user, []string{model.PermissionPagesWrite, model.PermissionPagesRead})}
}
func draftOptions() pages.WriteOptions {
	return pages.WriteOptions{ForceDraft: true, RequestID: uuid.NewString(), Authorize: func(context.Context, *store.Queries) error { return nil }}
}
func requireCode(t *testing.T, err error, want string) {
	t.Helper()
	var domain *v2.Error
	if !errors.As(err, &domain) || domain.Code() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func TestDraftCreateAndLostUpdateResponsesReplayBeforeCAS(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	options := draftOptions()
	input := pages.CreatePageBody{Title: "Agent draft", Slug: "agent-draft", Body: "<p>Draft</p>"}
	page, err := f.svc.CreateWith(ctx, f.actor, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if page.Status != "draft" || page.PublishedAt != nil || page.ScheduledAt != nil || page.AuthorID != f.human.UserID || page.Revision != 1 {
		t.Fatalf("unsafe draft: %+v", page)
	}
	again, err := f.svc.CreateWith(ctx, f.actor, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != page.ID || again.Revision != 1 {
		t.Fatal("create retry did not replay")
	}
	input.Title = "Different request under same ID"
	_, err = f.svc.CreateWith(ctx, f.actor, input, options)
	requireCode(t, err, "idempotency_key_reused")
	title := "Updated draft"
	patch := pages.UpdatePageBody{Title: &title, BaseRevision: &page.Revision}
	options = draftOptions()
	updated, err := f.svc.UpdateWith(ctx, f.actor, page.ID, patch, options)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("revision=%d", updated.Revision)
	}
	replayed, err := f.svc.UpdateWith(ctx, f.actor, page.ID, patch, options)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Revision != 2 {
		t.Fatal("lost update response did not replay original base revision")
	}
	count, err := f.q.CountPageVersions(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("replay created a version: %d", count)
	}
	options.RequestID = uuid.NewString()
	_, err = f.svc.UpdateWith(ctx, f.actor, page.ID, patch, options)
	requireCode(t, err, "revision_conflict")
}

func TestOrdinaryRESTCompatibilityAndAIRevisionRequirement(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	page, err := f.svc.Create(ctx, f.human, pages.CreatePageBody{Title: "Human draft", Slug: "human-draft"})
	if err != nil {
		t.Fatal(err)
	}
	title := "Legacy REST update"
	updated, err := f.svc.Update(ctx, f.human, page.ID, pages.UpdatePageBody{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("legacy update revision=%d", updated.Revision)
	}
	if err = f.q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: f.human.UserID, SetAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Update(ctx, f.human, page.ID, pages.UpdatePageBody{Title: &title})
	requireCode(t, err, "revision_required")
	_, err = f.svc.Update(ctx, f.human, page.ID, pages.UpdatePageBody{Title: &title, BaseRevision: &updated.Revision})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDraftPermissionsOwnershipAndLifecycle(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	options := draftOptions()
	page, err := f.svc.CreateWith(ctx, f.actor, pages.CreatePageBody{Title: "Draft", Slug: "draft"}, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"draft", "published"} {
		_, err = f.svc.CreateWith(ctx, f.actor, pages.CreatePageBody{Title: "No", Slug: "no", Status: status}, draftOptions())
		requireCode(t, err, "forbidden_field")
	}
	scheduled := time.Now().Add(time.Hour).Format(time.RFC3339)
	_, err = f.svc.CreateWith(ctx, f.actor, pages.CreatePageBody{Title: "No", Slug: "no", ScheduledAt: &scheduled}, draftOptions())
	requireCode(t, err, "forbidden_field")
	status := "draft"
	_, err = f.svc.UpdateWith(ctx, f.actor, page.ID, pages.UpdatePageBody{Status: &status, BaseRevision: &page.Revision}, draftOptions())
	requireCode(t, err, "forbidden_field")
	if _, err = f.db.Exec(`UPDATE pages SET scheduled_at = CURRENT_TIMESTAMP WHERE id = ?`, page.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateWith(ctx, f.actor, page.ID, pages.UpdatePageBody{BaseRevision: &page.Revision}, draftOptions())
	requireCode(t, err, "not_found")
	if _, err = f.db.Exec(`UPDATE pages SET scheduled_at = NULL, status = 'published' WHERE id = ?`, page.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateWith(ctx, f.actor, page.ID, pages.UpdatePageBody{BaseRevision: &page.Revision}, draftOptions())
	requireCode(t, err, "not_found")
	if _, err = f.db.Exec(`UPDATE pages SET status = 'draft' WHERE id = ?`, page.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: false, SetBy: f.human.UserID, SetAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateWith(ctx, f.actor, page.ID, pages.UpdatePageBody{BaseRevision: &page.Revision}, draftOptions())
	requireCode(t, err, "not_found")
	// Removing opt-in also denies a replay; it cannot return the old content.
	_, err = f.svc.CreateWith(ctx, f.actor, pages.CreatePageBody{Title: "Draft", Slug: "draft"}, options)
	requireCode(t, err, "not_found")
	if err = f.q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: f.human.UserID, SetAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	other, err := f.q.CreateUser(ctx, store.CreateUserParams{Email: "admin@example.test", Role: "admin", PasswordHash: "unused", Name: "Admin", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := f.q.CreateAPIKey(ctx, store.CreateAPIKeyParams{Name: "Other admin", KeyHash: "unused", KeyPrefix: "othr", Permissions: f.actor.APIKey.Permissions, IsActive: true, CreatedBy: other.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	otherActor := v2.Actor{APIKey: &otherKey, Permissions: f.actor.Permissions}
	_, err = f.svc.UpdateWith(ctx, otherActor, page.ID, pages.UpdatePageBody{BaseRevision: &page.Revision}, draftOptions())
	requireCode(t, err, "not_found")

	if _, err = f.db.Exec(`UPDATE users SET role = 'public' WHERE id = ?`, f.human.UserID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateWith(ctx, f.actor, pages.CreatePageBody{Title: "Draft", Slug: "draft"}, options)
	requireCode(t, err, "forbidden")
}

func TestParallelDuplicateCreateProducesOnePage(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	options := draftOptions()
	input := pages.CreatePageBody{Title: "Parallel", Slug: "parallel"}
	var wg sync.WaitGroup
	results := make(chan *pages.Page, 2)
	failures := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			<-start
			p, err := f.svc.CreateWith(ctx, f.actor, input, options)
			results <- p
			failures <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id int64
	for p := range results {
		if id != 0 && p.ID != id {
			t.Fatal("duplicate write")
		}
		id = p.ID
	}
	count, err := f.q.CountPages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("parallel duplicate created %d pages", count)
	}
}

func TestDifferentAppRequestsRaceOnOneUserDraft(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	page, err := f.svc.CreateWith(ctx, f.actor, pages.CreatePageBody{Title: "Shared user draft", Slug: "shared-user-draft"}, draftOptions())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	key, err := f.q.CreateAPIKey(ctx, store.CreateAPIKeyParams{Name: "Second app", KeyHash: "second-unused", KeyPrefix: "sec", Permissions: f.actor.APIKey.Permissions, IsActive: true, CreatedBy: f.human.UserID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	second := v2.Actor{APIKey: &key, Permissions: f.actor.Permissions}
	start := make(chan struct{})
	results := make(chan error, 2)
	var writers sync.WaitGroup
	for index, actor := range []v2.Actor{f.actor, second} {
		writers.Go(func() {
			<-start
			title := []string{"First app edit", "Second app edit"}[index]
			_, err := f.svc.UpdateWith(ctx, actor, page.ID, pages.UpdatePageBody{Title: &title, BaseRevision: &page.Revision}, draftOptions())
			results <- err
		})
	}
	close(start)
	writers.Wait()
	close(results)
	won, conflicted := 0, 0
	for err := range results {
		if err == nil {
			won++
		} else {
			requireCode(t, err, "revision_conflict")
			conflicted++
		}
	}
	if won != 1 || conflicted != 1 {
		t.Fatalf("different app requests: wins=%d conflicts=%d", won, conflicted)
	}
	testutil.AssertPageRevision(t, f.q, page.ID, 2)
	var requests int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM idempotency_keys WHERE tool = 'update_page'`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("failed CAS left an idempotency record: %d", requests)
	}
}

func TestFailedAtomicWriteHasNoIdempotencyRecord(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	options := draftOptions()
	input := pages.CreatePageBody{Title: "Atomic", Slug: "atomic"}
	if _, err := f.db.Exec(`CREATE TRIGGER fail_snapshot BEFORE INSERT ON page_versions BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateWith(ctx, f.actor, input, options); err == nil {
		t.Fatal("injected snapshot error succeeded")
	}
	count, err := f.q.CountPages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("partial page committed")
	}
	_, err = f.q.GetIdempotencyKey(ctx, store.GetIdempotencyKeyParams{UserID: f.human.UserID, Tool: "create_page", RequestID: options.RequestID})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed write consumed request ID: %v", err)
	}
	if _, err = f.db.Exec(`DROP TRIGGER fail_snapshot`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CreateWith(ctx, f.actor, input, options); err != nil {
		t.Fatalf("same ID cannot retry corrected transient failure: %v", err)
	}
}

func TestExpiredAndDeletedRequestsNeverWriteAgain(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	options := draftOptions()
	input := pages.CreatePageBody{Title: "Expiring", Slug: "expiring"}
	page, err := f.svc.CreateWith(ctx, f.actor, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE idempotency_keys SET created_at = ?`, time.Now().Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateWith(ctx, f.actor, input, options)
	requireCode(t, err, "request_expired")
	if err = f.q.CompactIdempotencyKeys(ctx, time.Now().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	record, err := f.q.GetIdempotencyKey(ctx, store.GetIdempotencyKeyParams{UserID: f.human.UserID, Tool: "create_page", RequestID: options.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	if record.ResultJson.Valid || record.EntityID != page.ID || record.EntityRevision != 1 {
		t.Fatal("compaction removed tombstone identity")
	}
	if err = f.svc.Delete(ctx, f.human, page.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateWith(ctx, f.actor, input, options)
	requireCode(t, err, "request_expired")
	count, err := f.q.CountPages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("expired/deleted request created a duplicate")
	}
}

func TestReplaysRecheckRevocationAndTransportPolicy(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	options := draftOptions()
	input := pages.CreatePageBody{Title: "Authorized", Slug: "authorized"}
	if _, err := f.svc.CreateWith(ctx, f.actor, input, options); err != nil {
		t.Fatal(err)
	}
	denied := options
	denied.Authorize = func(context.Context, *store.Queries) error {
		return v2.NewError(v2.ErrForbidden, "Module disabled or source no longer allowed")
	}
	_, err := f.svc.CreateWith(ctx, f.actor, input, denied)
	requireCode(t, err, "forbidden")
	if err = f.q.DeactivateAPIKey(ctx, store.DeactivateAPIKeyParams{ID: f.actor.APIKey.ID, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateWith(ctx, f.actor, input, options)
	requireCode(t, err, "unauthorized")
}

func TestDirectJSONCannotHideForbiddenLifecycleFields(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	for _, field := range []string{"status", "scheduled_at", "published_at"} {
		var input pages.CreatePageBody
		if err := json.Unmarshal([]byte(`{"title":"No","slug":"no","`+field+`":null}`), &input); err != nil {
			t.Fatal(err)
		}
		_, err := f.svc.CreateWith(ctx, f.actor, input, draftOptions())
		requireCode(t, err, "forbidden_field")
	}
	var first, second pages.UpdatePageBody
	if err := json.Unmarshal([]byte(`{"summary":null}`), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{}`), &second); err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.CreateWith(ctx, f.actor, pages.CreatePageBody{Title: "Null comparison", Slug: "null-comparison"}, draftOptions())
	if err != nil {
		t.Fatal(err)
	}
	first.BaseRevision = &page.Revision
	second.BaseRevision = &page.Revision
	options := draftOptions()
	if _, err = f.svc.UpdateWith(ctx, f.actor, page.ID, first, options); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateWith(ctx, f.actor, page.ID, second, options)
	requireCode(t, err, "idempotency_key_reused")

}

func TestReplayAndNoOpDoNotRepeatWebhooks(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	recorder := &testutil.WebhookRecorder{}
	f.svc.SetWebhookDispatcher(recorder)
	options := draftOptions()
	input := pages.CreatePageBody{Title: "One event", Slug: "one-event"}
	page, err := f.svc.CreateWith(ctx, f.actor, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CreateWith(ctx, f.actor, input, options); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.UpdateWith(ctx, f.actor, page.ID, pages.UpdatePageBody{Title: &input.Title, BaseRevision: &page.Revision}, draftOptions()); err != nil {
		t.Fatal(err)
	}
	if events := recorder.Events(); len(events) != 1 || events[0].Type != model.EventPageCreated {
		t.Fatalf("replay/no-op emitted events: %+v", events)
	}
}

func TestAgentUpdateClearsLegacyPublicationTimestamp(t *testing.T) {
	f := setupWrites(t)
	ctx := context.Background()
	page, err := f.svc.CreateWith(ctx, f.actor, pages.CreatePageBody{Title: "Legacy draft", Slug: "legacy-draft"}, draftOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE pages SET published_at = CURRENT_TIMESTAMP WHERE id = ?`, page.ID); err != nil {
		t.Fatal(err)
	}
	title := "Updated legacy draft"
	updated, err := f.svc.UpdateWith(ctx, f.actor, page.ID, pages.UpdatePageBody{Title: &title, BaseRevision: &page.Revision}, draftOptions())
	if err != nil {
		t.Fatal(err)
	}
	if updated.PublishedAt != nil || updated.ScheduledAt != nil || updated.Status != "draft" {
		t.Fatalf("agent write retained lifecycle state: %+v", updated)
	}
}
