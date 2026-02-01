// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/model"
)

func setupEventTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create events table (matches schema in migrations)
	_, err = db.Exec(`
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL DEFAULT 'info',
			category TEXT NOT NULL DEFAULT 'system',
			message TEXT NOT NULL,
			user_id INTEGER,
			metadata TEXT NOT NULL DEFAULT '{}',
			ip_address TEXT NOT NULL DEFAULT '',
			request_url TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("failed to create events table: %v", err)
	}

	return db
}

func TestNewEventService(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	if svc == nil {
		t.Error("NewEventService returned nil")
	}
}

func TestLogEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	userID := int64(123)
	err := svc.LogEvent(ctx, model.EventLevelInfo, model.EventCategoryMigrator, "Test message", &userID, "192.168.1.100", "/admin/migrator", map[string]any{
		"key": "value",
	})
	if err != nil {
		t.Fatalf("LogEvent failed: %v", err)
	}

	// Verify event was created
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count events: %v", err)
	}
	if count != 1 {
		t.Errorf("event count = %d, want 1", count)
	}

	// Verify event details
	var level, category, message, metadata, requestURL string
	var savedUserID sql.NullInt64
	err = db.QueryRow("SELECT level, category, message, user_id, metadata, request_url FROM events").Scan(&level, &category, &message, &savedUserID, &metadata, &requestURL)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}

	if level != "info" {
		t.Errorf("level = %q, want %q", level, "info")
	}
	if category != "migrator" {
		t.Errorf("category = %q, want %q", category, "migrator")
	}
	if message != "Test message" {
		t.Errorf("message = %q, want %q", message, "Test message")
	}
	if !savedUserID.Valid || savedUserID.Int64 != 123 {
		t.Errorf("user_id = %v, want 123", savedUserID)
	}
	if metadata != `{"key":"value"}` {
		t.Errorf("metadata = %q, want %q", metadata, `{"key":"value"}`)
	}
	if requestURL != "/admin/migrator" {
		t.Errorf("request_url = %q, want %q", requestURL, "/admin/migrator")
	}
}

func TestLogEvent_NilUserID(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogEvent(ctx, model.EventLevelWarning, model.EventCategorySystem, "No user", nil, "", "", nil)
	if err != nil {
		t.Fatalf("LogEvent failed: %v", err)
	}

	// Verify user_id is NULL
	var savedUserID sql.NullInt64
	err = db.QueryRow("SELECT user_id FROM events").Scan(&savedUserID)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if savedUserID.Valid {
		t.Error("user_id should be NULL")
	}
}

func TestLogEvent_NilMetadata(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogEvent(ctx, model.EventLevelInfo, model.EventCategoryAuth, "Test", nil, "", "", nil)
	if err != nil {
		t.Fatalf("LogEvent failed: %v", err)
	}

	// Verify metadata is empty JSON object
	var metadata string
	err = db.QueryRow("SELECT metadata FROM events").Scan(&metadata)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if metadata != "{}" {
		t.Errorf("metadata = %q, want %q", metadata, "{}")
	}
}

// testEventField tests that a logging function produces the expected field value in the database.
func testEventField(t *testing.T, db *sql.DB, logFn func(*EventService, context.Context) error, fieldName, expected string) {
	t.Helper()
	svc := NewEventService(db)
	ctx := context.Background()

	err := logFn(svc, ctx)
	if err != nil {
		t.Fatalf("Log function failed: %v", err)
	}

	var got string
	err = db.QueryRow("SELECT " + fieldName + " FROM events").Scan(&got)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if got != expected {
		t.Errorf("%s = %q, want %q", fieldName, got, expected)
	}
}

func TestLogLevels(t *testing.T) {
	tests := []struct {
		name     string
		logFn    func(*EventService, context.Context) error
		expected string
	}{
		{"info", func(svc *EventService, ctx context.Context) error { return svc.LogInfo(ctx, model.EventCategoryPage, "Page created", nil, "", "", nil) }, "info"},
		{"warning", func(svc *EventService, ctx context.Context) error { return svc.LogWarning(ctx, model.EventCategorySystem, "Low disk space", nil, "", "", nil) }, "warning"},
		{"error", func(svc *EventService, ctx context.Context) error { return svc.LogError(ctx, model.EventCategoryAuth, "Login failed", nil, "", "", nil) }, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupEventTestDB(t)
			defer func() { _ = db.Close() }()
			testEventField(t, db, tt.logFn, "level", tt.expected)
		})
	}
}

func TestLogMigratorEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	userID := int64(1)
	err := svc.LogMigratorEvent(ctx, model.EventLevelInfo, "Content imported from elefant", &userID, "10.0.0.1", "/admin/migrator/elefant/import", map[string]any{
		"source":         "elefant",
		"posts_imported": 10,
		"tags_imported":  5,
		"media_imported": 3,
	})
	if err != nil {
		t.Fatalf("LogMigratorEvent failed: %v", err)
	}

	var level, category, message, requestURL string
	err = db.QueryRow("SELECT level, category, message, request_url FROM events").Scan(&level, &category, &message, &requestURL)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}

	if level != "info" {
		t.Errorf("level = %q, want %q", level, "info")
	}
	if category != "migrator" {
		t.Errorf("category = %q, want %q", category, "migrator")
	}
	if message != "Content imported from elefant" {
		t.Errorf("message = %q, want %q", message, "Content imported from elefant")
	}
	if requestURL != "/admin/migrator/elefant/import" {
		t.Errorf("request_url = %q, want %q", requestURL, "/admin/migrator/elefant/import")
	}
}

func TestLogCategoryEvents(t *testing.T) {
	tests := []struct {
		name     string
		logFn    func(*EventService, context.Context) error
		expected string
	}{
		{"auth", func(svc *EventService, ctx context.Context) error { return svc.LogAuthEvent(ctx, model.EventLevelInfo, "User logged in", nil, "", "", nil) }, "auth"},
		{"page", func(svc *EventService, ctx context.Context) error { return svc.LogPageEvent(ctx, model.EventLevelInfo, "Page published", nil, "", "", nil) }, "page"},
		{"user", func(svc *EventService, ctx context.Context) error { return svc.LogUserEvent(ctx, model.EventLevelInfo, "User created", nil, "", "", nil) }, "user"},
		{"config", func(svc *EventService, ctx context.Context) error { return svc.LogConfigEvent(ctx, model.EventLevelInfo, "Config updated", nil, "", "", nil) }, "config"},
		{"system", func(svc *EventService, ctx context.Context) error { return svc.LogSystemEvent(ctx, model.EventLevelInfo, "System started", nil, "", "", nil) }, "system"},
		{"cache", func(svc *EventService, ctx context.Context) error { return svc.LogCacheEvent(ctx, model.EventLevelInfo, "Cache cleared", nil, "", "", nil) }, "cache"},
		{"media", func(svc *EventService, ctx context.Context) error { return svc.LogMediaEvent(ctx, model.EventLevelInfo, "Media uploaded", nil, "", "", nil) }, "media"},
		{"tag", func(svc *EventService, ctx context.Context) error { return svc.LogTagEvent(ctx, model.EventLevelInfo, "Tag created", nil, "", "", nil) }, "tag"},
		{"category", func(svc *EventService, ctx context.Context) error { return svc.LogCategoryEvent(ctx, model.EventLevelInfo, "Category created", nil, "", "", nil) }, "category"},
		{"menu", func(svc *EventService, ctx context.Context) error { return svc.LogMenuEvent(ctx, model.EventLevelInfo, "Menu created", nil, "", "", nil) }, "menu"},
		{"api_key", func(svc *EventService, ctx context.Context) error { return svc.LogAPIKeyEvent(ctx, model.EventLevelInfo, "API key created", nil, "", "", nil) }, "api_key"},
		{"webhook", func(svc *EventService, ctx context.Context) error { return svc.LogWebhookEvent(ctx, model.EventLevelInfo, "Webhook created", nil, "", "", nil) }, "webhook"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupEventTestDB(t)
			defer func() { _ = db.Close() }()
			testEventField(t, db, tt.logFn, "category", tt.expected)
		})
	}
}

func TestDeleteOldEvents(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	// Insert an old event directly
	_, err := db.Exec(`
		INSERT INTO events (level, category, message, metadata, created_at)
		VALUES ('info', 'test', 'Old event', '{}', datetime('now', '-31 days'))
	`)
	if err != nil {
		t.Fatalf("failed to insert old event: %v", err)
	}

	// Insert a recent event
	err = svc.LogInfo(ctx, "test", "Recent event", nil, "", "", nil)
	if err != nil {
		t.Fatalf("LogInfo failed: %v", err)
	}

	// Verify we have 2 events
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count events: %v", err)
	}
	if count != 2 {
		t.Errorf("event count = %d, want 2", count)
	}

	// Delete events older than 30 days
	err = svc.DeleteOldEvents(ctx, 30*24*60*60*1e9) // 30 days in nanoseconds
	if err != nil {
		t.Fatalf("DeleteOldEvents failed: %v", err)
	}

	// Verify only 1 event remains
	err = db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count events: %v", err)
	}
	if count != 1 {
		t.Errorf("event count after delete = %d, want 1", count)
	}
}
