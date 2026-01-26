// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewEventsHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewEventsHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewEventsHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestEventCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	event, err := queries.CreateEvent(context.Background(), store.CreateEventParams{
		Level:    "info",
		Category: "system",
		Message:  "Test event message",
		UserID:   sql.NullInt64{Int64: user.ID, Valid: true},
		Metadata: `{"key": "value"}`,
	})
	if err != nil {
		t.Fatalf("CreateEvent failed: %v", err)
	}

	if event.Level != "info" {
		t.Errorf("Level = %q, want %q", event.Level, "info")
	}
	if event.Category != "system" {
		t.Errorf("Category = %q, want %q", event.Category, "system")
	}
	if event.Message != "Test event message" {
		t.Errorf("Message = %q, want %q", event.Message, "Test event message")
	}
}

func TestEventList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create test events
	levels := []string{"info", "warning", "error"}
	for _, level := range levels {
		_, err := queries.CreateEvent(context.Background(), store.CreateEventParams{
			Level:    level,
			Category: "system",
			Message:  "Event " + level,
			Metadata: "{}",
		})
		if err != nil {
			t.Fatalf("CreateEvent failed: %v", err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		events, err := queries.ListEvents(context.Background(), store.ListEventsParams{
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}
		if len(events) != 3 {
			t.Errorf("got %d events, want 3", len(events))
		}
	})

	t.Run("count", func(t *testing.T) {
		count, err := queries.CountEvents(context.Background())
		if err != nil {
			t.Fatalf("CountEvents failed: %v", err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
	})
}

func TestEventWithoutUser(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	event, err := queries.CreateEvent(context.Background(), store.CreateEventParams{
		Level:    "info",
		Category: "system",
		Message:  "System event without user",
		Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("CreateEvent failed: %v", err)
	}

	if event.UserID.Valid {
		t.Error("UserID should not be valid for system event")
	}
}

func TestEventGetByID(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	created, err := queries.CreateEvent(context.Background(), store.CreateEventParams{
		Level:    "warning",
		Category: "auth",
		Message:  "Login attempt",
		Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("CreateEvent failed: %v", err)
	}

	event, err := queries.GetEvent(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetEvent failed: %v", err)
	}

	if event.ID != created.ID {
		t.Errorf("ID = %d, want %d", event.ID, created.ID)
	}
	if event.Level != "warning" {
		t.Errorf("Level = %q, want %q", event.Level, "warning")
	}
}

func TestEventListByLevel(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create events with different levels
	for _, level := range []string{"info", "info", "warning", "error"} {
		_, err := queries.CreateEvent(context.Background(), store.CreateEventParams{
			Level:    level,
			Category: "system",
			Message:  "Event " + level,
			Metadata: "{}",
		})
		if err != nil {
			t.Fatalf("CreateEvent failed: %v", err)
		}
	}

	events, err := queries.ListEventsByLevel(context.Background(), store.ListEventsByLevelParams{
		Level:  "info",
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListEventsByLevel failed: %v", err)
	}

	if len(events) != 2 {
		t.Errorf("got %d info events, want 2", len(events))
	}
}

func TestEventListByCategory(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create events with different categories
	categories := []string{"auth", "auth", "system", "page"}
	for i, cat := range categories {
		_, err := queries.CreateEvent(context.Background(), store.CreateEventParams{
			Level:    "info",
			Category: cat,
			Message:  "Event " + string(rune('A'+i)),
			Metadata: "{}",
		})
		if err != nil {
			t.Fatalf("CreateEvent failed: %v", err)
		}
	}

	events, err := queries.ListEventsByCategory(context.Background(), store.ListEventsByCategoryParams{
		Category: "auth",
		Limit:    100,
		Offset:   0,
	})
	if err != nil {
		t.Fatalf("ListEventsByCategory failed: %v", err)
	}

	if len(events) != 2 {
		t.Errorf("got %d auth events, want 2", len(events))
	}
}

func TestEventCountByLevel(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create error events
	for i := 0; i < 3; i++ {
		_, err := queries.CreateEvent(context.Background(), store.CreateEventParams{
			Level:    "error",
			Category: "system",
			Message:  "Error event",
			Metadata: "{}",
		})
		if err != nil {
			t.Fatalf("CreateEvent failed: %v", err)
		}
	}

	count, err := queries.CountEventsByLevel(context.Background(), "error")
	if err != nil {
		t.Fatalf("CountEventsByLevel failed: %v", err)
	}

	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}
