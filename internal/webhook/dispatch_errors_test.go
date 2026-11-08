// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

type dispatchFunc func(context.Context, string, any) error

func (f dispatchFunc) DispatchEvent(ctx context.Context, eventType string, data any) error {
	return f(ctx, eventType, data)
}

func TestDispatchCallersLogFailureOnce(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	cfg := DefaultConfig()
	cfg.EnableDebounce = false
	d := NewDispatcher(db, logger, cfg)
	ctx := t.Context()
	DispatchPageEvent(ctx, d, model.EventPageUpdated, store.Page{ID: 31}, "")
	DispatchMediaEvent(ctx, d, model.EventMediaDeleted, store.Medium{ID: 42})
	wrapped := dispatchFunc(func(ctx context.Context, eventType string, data any) error {
		return fmt.Errorf("wrapped: %w", d.DispatchEvent(ctx, eventType, data))
	})
	DispatchPageEvent(ctx, wrapped, model.EventPageDeleted, store.Page{ID: 53}, "")
	failed := dispatchFunc(func(context.Context, string, any) error { return errors.New("unreported failure") })
	DispatchMediaEvent(ctx, failed, model.EventMediaUploaded, store.Medium{ID: 64})

	d.Start(ctx)
	defer d.Stop()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	DispatchPageEvent(ctx, d, model.EventPageCreated, store.Page{ID: 75}, "")
	d.Stop()
	for _, tc := range []struct {
		message, event string
		id             int64
	}{
		{"webhook event dropped", model.EventPageUpdated, 31},
		{"webhook event dropped", model.EventMediaDeleted, 42},
		{"webhook event dropped", model.EventPageDeleted, 53},
		{"failed to dispatch webhook event", model.EventMediaUploaded, 64},
		{"failed to list webhooks for event", model.EventPageCreated, 75},
	} {
		requireOnlyDispatchError(t, logs.String(), tc.message, tc.event, tc.id)
	}
}

func TestStoppedDispatchReportsEntity(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	var logs bytes.Buffer
	cfg := DefaultConfig()
	cfg.Debounce.Interval = time.Hour
	d := NewDispatcher(db, slog.New(slog.NewJSONHandler(&logs, nil)), cfg)
	defer d.debouncer.Stop()
	ctx := t.Context()
	check := func(t *testing.T) {
		t.Helper()
		for _, call := range []struct {
			event string
			id    int64
			err   error
		}{
			{model.EventPageUpdated, 31, d.DispatchEvent(ctx, model.EventPageUpdated, PageEventData{ID: 31})},
			{model.EventMediaDeleted, 42, d.DispatchImmediate(ctx, model.EventMediaDeleted, &MediaEventData{ID: 42})},
			{model.EventUserCreated, 53, d.DispatchEvent(ctx, model.EventUserCreated, UserEventData{ID: 53})},
			{model.EventFormSubmitted, 64, d.Dispatch(ctx, NewEvent(model.EventFormSubmitted, FormEventData{FormID: 2, SubmissionID: 64}))},
			{"custom.nil", 0, d.Dispatch(ctx, NewEvent("custom.nil", (*PageEventData)(nil)))},
		} {
			if call.err == nil {
				t.Errorf("stopped dispatcher reported success for %s", call.event)
			}
			requireDispatchError(t, logs.String(), "webhook event dropped", call.event, call.id, 0)
		}
		if pending, _ := d.DebounceStats(); pending != 0 {
			t.Errorf("stopped dispatcher accepted %d events into the debounce queue", pending)
		}
	}
	t.Run("before start", check)
	d.Start(ctx)
	d.Stop()
	logs.Reset()
	t.Run("after stop", check)
}

func TestDispatchFailuresLogEntity(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	ctx := t.Context()
	hook := createDispatchTestWebhook(t, store.New(db))
	var logs bytes.Buffer
	cfg := DefaultConfig()
	cfg.Debounce.Interval = time.Hour
	d := NewDispatcher(db, slog.New(slog.NewJSONHandler(&logs, nil)), cfg)
	d.Start(ctx)
	defer d.Stop()

	var marshalErr *json.UnsupportedTypeError
	if err := d.DispatchImmediate(ctx, model.EventPageUpdated, map[string]any{"id": int64(71), "unsupported": make(chan int)}); !errors.As(err, &marshalErr) {
		t.Errorf("dispatch returned %v, want the marshal error", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_delivery BEFORE INSERT ON webhook_deliveries
		BEGIN SELECT RAISE(ABORT, 'injected delivery insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.DispatchImmediate(ctx, model.EventPageUpdated, PageEventData{ID: 82}); err != nil {
		t.Fatalf("dispatch should continue after a failed per-hook insert: %v", err)
	}
	if err := d.DispatchEvent(ctx, model.EventMediaUploaded, &MediaEventData{ID: 93}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	d.Stop() // Flush the real debounce queue and wait for its error log.
	requireDispatchError(t, logs.String(), "failed to marshal event payload", model.EventPageUpdated, 71, 0)
	requireDispatchError(t, logs.String(), "failed to create delivery record", model.EventPageUpdated, 82, hook.ID)
	requireOnlyDispatchError(t, logs.String(), "failed to list webhooks for event", model.EventMediaUploaded, 93)
}

func requireOnlyDispatchError(t *testing.T, logs, message, event string, entityID int64) {
	t.Helper()
	var matches []string
	for line := range strings.Lines(logs) {
		if strings.Contains(line, `"level":"ERROR"`) && strings.Contains(line, `"event_type":"`+event+`"`) {
			matches = append(matches, strings.TrimSpace(line))
		}
	}
	if len(matches) != 1 {
		t.Errorf("got %d error records for %s, want 1:\n%s", len(matches), event, strings.Join(matches, "\n"))
	}
	requireDispatchError(t, logs, message, event, entityID, 0)
}

func requireDispatchError(t *testing.T, logs, message, event string, entityID, webhookID int64) {
	t.Helper()
	for line := range strings.Lines(logs) {
		var rec struct {
			Level    string `json:"level"`
			Message  string `json:"msg"`
			Event    string `json:"event_type"`
			EntityID *int64 `json:"entity_id"`
			HookID   int64  `json:"webhook_id"`
			Error    string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Message != message || rec.Event != event {
			continue
		}
		if rec.Level != "ERROR" || rec.Error == "" || rec.HookID != webhookID ||
			(entityID == 0 && rec.EntityID != nil) || (entityID != 0 && (rec.EntityID == nil || *rec.EntityID != entityID)) {
			t.Errorf("incomplete dispatch error for %s, want entity %d and webhook %d:\n%s", event, entityID, webhookID, line)
		}
		return
	}
	t.Errorf("missing dispatch error %q for %s:\n%s", message, event, logs)
}

func createDispatchTestWebhook(t *testing.T, queries *store.Queries) store.Webhook {
	t.Helper()
	ctx := t.Context()
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
	return hook
}
