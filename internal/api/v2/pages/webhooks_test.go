// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"context"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// TestPageWritesSendWebhookEvents checks that creating, updating and
// deleting a page through the REST API sends the page.created, page.updated
// and page.deleted webhook events the admin UI sends, with the page's data.
// Only the admin UI sent them, so integrations missed every API write
// (review of the 2026-10-17 audit fixes).
//
// Bug state: drop a dispatchPageEvent call in the pages service and this
// fails.
func TestPageWritesSendWebhookEvents(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	recorder := &testutil.WebhookRecorder{}
	svc.SetWebhookDispatcher(recorder)
	ctx := context.Background()
	now := time.Now()
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "api@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "API", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := v2.Actor{APIKey: &store.ApiKey{ID: 1, CreatedBy: author.ID}, Permissions: []string{model.PermissionPagesWrite}}

	created, err := svc.Create(ctx, writer, pages.CreatePageBody{Title: "Hooks", Slug: "hooks", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	title := "Hooks, renamed"
	if _, err := svc.Update(ctx, writer, created.ID, pages.UpdatePageBody{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, writer, created.ID); err != nil {
		t.Fatal(err)
	}

	want := []struct{ eventType, title string }{
		{model.EventPageCreated, "Hooks"},
		{model.EventPageUpdated, "Hooks, renamed"},
		{model.EventPageDeleted, "Hooks, renamed"},
	}
	if len(recorder.Events()) != len(want) {
		t.Fatalf("sent %d events, want %d: %+v", len(recorder.Events()), len(want), recorder.Events())
	}
	for i, w := range want {
		got := recorder.Events()[i]
		data, ok := got.Data.(webhook.PageEventData)
		if got.Type != w.eventType || !ok || data.ID != created.ID || data.Title != w.title || data.Slug != "hooks" || data.AuthorID != author.ID {
			t.Errorf("event %d = %s %+v, want %s for page %d titled %q", i, got.Type, got.Data, w.eventType, created.ID, w.title)
		}
	}
}

// TestFailedPageWritesSendNoWebhookEvents checks that a REST page write that
// fails sends no webhook event, here an update rolled back after the page row
// changed and a delete rolled back by a trigger: integrations must not hear
// of changes that never happened.
//
// Bug state: dispatch page.updated right after UpdatePage, before the
// commit, or page.deleted before the delete, and this fails.
func TestFailedPageWritesSendNoWebhookEvents(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	recorder := &testutil.WebhookRecorder{}
	svc.SetWebhookDispatcher(recorder)
	ctx := context.Background()
	now := time.Now()
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "api@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "API", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := v2.Actor{APIKey: &store.ApiKey{ID: 1, CreatedBy: author.ID}, Permissions: []string{model.PermissionPagesWrite}}
	created, err := svc.Create(ctx, writer, pages.CreatePageBody{Title: "Hooks", Slug: "hooks", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	category, err := queries.CreateCategory(ctx, store.CreateCategoryParams{Name: "News", Slug: "news", LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{
		`CREATE TRIGGER fail_page_categories BEFORE INSERT ON page_categories BEGIN SELECT RAISE(ABORT, 'probe'); END`,
		`CREATE TRIGGER fail_page_delete BEFORE DELETE ON pages BEGIN SELECT RAISE(ABORT, 'probe'); END`,
	} {
		if _, err := db.ExecContext(ctx, trigger); err != nil {
			t.Fatal(err)
		}
	}

	title := "Renamed"
	if _, err := svc.Update(ctx, writer, created.ID, pages.UpdatePageBody{Title: &title, CategoryIDs: &[]int64{category.ID}}); err == nil {
		t.Fatal("the update succeeded; the trigger did not roll it back and this test is vacuous")
	}
	if err := svc.Delete(ctx, writer, created.ID); err == nil {
		t.Fatal("the delete succeeded; the trigger did not roll it back and this test is vacuous")
	}
	if events := recorder.Events(); len(events) != 1 || events[0].Type != model.EventPageCreated {
		t.Errorf("events = %+v, want only the page.created of the successful create", events)
	}
}
