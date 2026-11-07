// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"log/slog"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestStopDeliversDebouncedEvents checks that an event still in the debounce
// window when shutdown starts is persisted. Pause dispatch during the drain
// and verify that an event arriving after the flush begins is refused.
func TestStopDeliversDebouncedEvents(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	ctx := t.Context()
	hook := createDispatchTestWebhook(t, queries)
	cfg := DefaultConfig()
	if !cfg.EnableDebounce {
		t.Fatal("the default configuration no longer debounces; this test is vacuous")
	}
	cfg.Debounce.Interval = time.Hour
	db.SetMaxOpenConns(1)
	// Keep the flush from reaching SQLite until the late call has run.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(db, slog.New(slog.DiscardHandler), cfg)
	d.Start(ctx)
	var stopped chan struct{}
	defer func() {
		_ = conn.Close()
		if stopped != nil {
			<-stopped
		} else {
			d.Stop()
		}
		d.debouncer.Stop()
	}()
	if err := d.DispatchEvent(ctx, model.EventPageUpdated, PageEventData{ID: 1, Title: "Probe"}); err != nil {
		t.Fatal(err)
	}
	stopped = make(chan struct{})
	go func() {
		d.Stop()
		close(stopped)
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(5 * time.Second)
	for d.debouncer.PendingCount() != 0 {
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("shutdown did not start draining the debounce queue")
		}
	}
	if err := d.DispatchEvent(ctx, model.EventPageUpdated, PageEventData{ID: 2, Title: "Late"}); err == nil {
		t.Error("late event was accepted while the debounce queue was draining")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	<-stopped

	count, err := queries.CountWebhookDeliveries(ctx, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("deliveries after Stop = %d, want the debounced event's 1", count)
	}
	if pending := d.debouncer.PendingCount(); pending != 0 {
		t.Errorf("debounce queue still contains %d events after shutdown", pending)
	}
}
