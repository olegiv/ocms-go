// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestStopDeliversDebouncedEvents checks that an event still in the debounce
// window when the dispatcher stops is turned into a delivery. Stop marked
// the dispatcher stopped before flushing the debouncer, and Dispatch drops
// events once stopped, so whatever was sent in the second before a shutdown,
// a scheduled publish among them, was lost (review of the 2026-10-17 audit
// fixes).
//
// Bug state: set running to false before d.debouncer.Stop() in Stop, or
// cancel the debouncer's context before waiting for its dispatches, and this
// fails.
func TestStopDeliversDebouncedEvents(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	admin, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "hooks@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Hooks", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := queries.CreateWebhook(ctx, store.CreateWebhookParams{
		Name: "Probe", Url: "https://127.0.0.1:1/hook", Secret: "secret-secret-secret",
		Events: `["` + model.EventPageUpdated + `"]`, IsActive: true, Headers: "{}",
		CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	if !cfg.EnableDebounce {
		t.Fatal("the default configuration no longer debounces; this test is vacuous")
	}
	d := NewDispatcher(db, slog.New(slog.DiscardHandler), cfg)
	d.Start(ctx)
	if err := d.DispatchEvent(ctx, model.EventPageUpdated, PageEventData{ID: 1, Title: "Probe"}); err != nil {
		t.Fatal(err)
	}
	d.Stop()

	count, err := queries.CountWebhookDeliveries(ctx, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("deliveries after Stop = %d, want the debounced event's 1", count)
	}
}
