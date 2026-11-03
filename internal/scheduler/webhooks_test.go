// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// TestScheduledPublishingSendsWebhookEvent checks that publishing a page at
// its scheduled time sends the page.published webhook event the admin UI's
// publish toggle sends, with the published page's data. Scheduled
// publishing sent none (review of the 2026-10-17 audit fixes).
//
// Bug state: replace the DispatchPageEvent call in publishPage with
// `_ = published` and this fails.
func TestScheduledPublishingSendsWebhookEvent(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "author@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Author", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Later", Slug: "later", Body: "b", Status: model.PageStatusDraft, AuthorID: author.ID,
		ScheduledAt: sql.NullTime{Time: now.Add(-time.Minute), Valid: true}, LanguageCode: "en",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	s := New(db, slog.Default(), nil)
	recorder := &testutil.WebhookRecorder{}
	s.SetWebhookDispatcher(recorder)
	if err := s.processScheduledPages(); err != nil {
		t.Fatal(err)
	}

	current, err := queries.GetPageByID(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != page.Revision+1 {
		t.Fatalf("scheduled publish revision=%d", current.Revision)
	}
	version, err := queries.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: page.ID, Revision: sql.NullInt64{Int64: current.Revision, Valid: true}})
	if err != nil || version.Legacy || !version.SnapshotJson.Valid {
		t.Fatalf("scheduled publish snapshot: %+v %v", version, err)
	}
	prov, err := queries.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: current.Revision})
	if err != nil || prov.Source != "system" || prov.UserID.Valid {
		t.Fatalf("scheduler provenance: %+v %v", prov, err)
	}

	if len(recorder.Events()) != 1 {
		t.Fatalf("sent %d events, want page.published: %+v", len(recorder.Events()), recorder.Events())
	}
	got := recorder.Events()[0]
	data, ok := got.Data.(webhook.PageEventData)
	if got.Type != model.EventPagePublished || !ok || data.ID != page.ID || data.Status != model.PageStatusPublished || data.PublishedAt == nil {
		t.Errorf("sent %s %+v, want page.published for page %d, published", got.Type, got.Data, page.ID)
	}
}

// TestScheduledPublishingSkipsPagesNoLongerDue checks that the scheduler
// leaves alone a page that was published, unscheduled or rescheduled to a
// later time after it listed the page: no second publish, no event log entry and no second
// page.published event. A manual publish racing the scheduler sent the event
// twice.
//
// Bug state: drop the status and scheduled_at conditions from the
// PublishScheduledPage query, or return its sql.ErrNoRows as an error, and
// this fails.
func TestScheduledPublishingSkipsPagesNoLongerDue(t *testing.T) {
	cases := []struct {
		name   string
		change string
	}{
		{"published by hand", `UPDATE pages SET status = 'published', published_at = ?1, scheduled_at = NULL WHERE id = ?2`},
		// A REST status change publishes the page and keeps scheduled_at.
		{"published, still scheduled", `UPDATE pages SET status = 'published', published_at = ?1 WHERE id = ?2`},
		{"unscheduled", `UPDATE pages SET scheduled_at = NULL, updated_at = ?1 WHERE id = ?2`},
		{"rescheduled later", `UPDATE pages SET scheduled_at = ?1 WHERE id = ?2`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			defer cleanup()
			queries := store.New(db)
			ctx := context.Background()
			now := time.Now()
			author, err := queries.CreateUser(ctx, store.CreateUserParams{
				Email: "author@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Author", CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := queries.CreatePage(ctx, store.CreatePageParams{
				Title: "Later", Slug: "later", Body: "b", Status: model.PageStatusDraft, AuthorID: author.ID,
				ScheduledAt: sql.NullTime{Time: now.Add(-time.Minute), Valid: true}, LanguageCode: "en",
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			listed, err := queries.GetScheduledPagesForPublishing(ctx, sql.NullTime{Time: now, Valid: true})
			if err != nil || len(listed) != 1 {
				t.Fatalf("listed %d pages (%v), want the due page", len(listed), err)
			}

			changedAt := now.Add(time.Hour)
			if _, err := db.ExecContext(ctx, tc.change, changedAt, listed[0].ID); err != nil {
				t.Fatal(err)
			}
			before, err := queries.GetPageByID(ctx, listed[0].ID)
			if err != nil {
				t.Fatal(err)
			}

			s := New(db, slog.Default(), nil)
			recorder := &testutil.WebhookRecorder{}
			s.SetWebhookDispatcher(recorder)
			if err := s.publishPage(ctx, queries, listed[0], now); !errors.Is(err, errNoLongerDue) {
				t.Fatalf("publishPage() = %v, want errNoLongerDue", err)
			}

			after, err := queries.GetPageByID(ctx, listed[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Status != before.Status || after.PublishedAt != before.PublishedAt || after.ScheduledAt != before.ScheduledAt {
				t.Errorf("page changed from %s %v %v to %s %v %v; want it left alone",
					before.Status, before.PublishedAt, before.ScheduledAt, after.Status, after.PublishedAt, after.ScheduledAt)
			}
			if events := recorder.Events(); len(events) != 0 {
				t.Errorf("sent %+v, want no event", events)
			}
			var logged int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE category = 'page'`).Scan(&logged); err != nil {
				t.Fatal(err)
			}
			if logged != 0 {
				t.Errorf("logged %d page events, want none", logged)
			}
		})
	}
}

// TestScheduledPublishingSurvivesCompactionFailure checks that a failed
// idempotency-replay compaction, which is housekeeping, does not stop due
// pages from being published in the same run.
//
// Bug state: return the CompactIdempotencyKeys error from
// processScheduledPages and this fails.
func TestScheduledPublishingSurvivesCompactionFailure(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "author@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Author", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Later", Slug: "later", Body: "b", Status: model.PageStatusDraft, AuthorID: author.ID,
		ScheduledAt: sql.NullTime{Time: now.Add(-time.Minute), Valid: true}, LanguageCode: "en",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ALTER TABLE idempotency_keys RENAME TO idempotency_keys_unavailable`); err != nil {
		t.Fatal(err)
	}

	if err = New(db, slog.Default(), nil).processScheduledPages(); err != nil {
		t.Fatalf("compaction failure stopped the run: %v", err)
	}
	current, err := queries.GetPageByID(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != model.PageStatusPublished {
		t.Fatalf("due page not published after compaction failure: status=%s", current.Status)
	}
}
