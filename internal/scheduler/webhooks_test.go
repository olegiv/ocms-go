// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"database/sql"
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

	if len(recorder.Events()) != 1 {
		t.Fatalf("sent %d events, want page.published: %+v", len(recorder.Events()), recorder.Events())
	}
	got := recorder.Events()[0]
	data, ok := got.Data.(webhook.PageEventData)
	if got.Type != model.EventPagePublished || !ok || data.ID != page.ID || data.Status != model.PageStatusPublished || data.PublishedAt == nil {
		t.Errorf("sent %s %+v, want page.published for page %d, published", got.Type, got.Data, page.ID)
	}
}
