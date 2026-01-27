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

	// Create events table (matches schema in migrations/00006_create_events.sql)
	_, err = db.Exec(`
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL DEFAULT 'info',
			category TEXT NOT NULL DEFAULT 'system',
			message TEXT NOT NULL,
			user_id INTEGER,
			metadata TEXT NOT NULL DEFAULT '{}',
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
	err := svc.LogEvent(ctx, model.EventLevelInfo, model.EventCategoryMigrator, "Test message", &userID, map[string]any{
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
	var level, category, message, metadata string
	var savedUserID sql.NullInt64
	err = db.QueryRow("SELECT level, category, message, user_id, metadata FROM events").Scan(&level, &category, &message, &savedUserID, &metadata)
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
}

func TestLogEvent_NilUserID(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogEvent(ctx, model.EventLevelWarning, model.EventCategorySystem, "No user", nil, nil)
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

	err := svc.LogEvent(ctx, model.EventLevelInfo, model.EventCategoryAuth, "Test", nil, nil)
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

func TestLogInfo(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogInfo(ctx, model.EventCategoryPage, "Page created", nil, nil)
	if err != nil {
		t.Fatalf("LogInfo failed: %v", err)
	}

	var level string
	err = db.QueryRow("SELECT level FROM events").Scan(&level)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if level != "info" {
		t.Errorf("level = %q, want %q", level, "info")
	}
}

func TestLogWarning(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogWarning(ctx, model.EventCategorySystem, "Low disk space", nil, nil)
	if err != nil {
		t.Fatalf("LogWarning failed: %v", err)
	}

	var level string
	err = db.QueryRow("SELECT level FROM events").Scan(&level)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if level != "warning" {
		t.Errorf("level = %q, want %q", level, "warning")
	}
}

func TestLogError(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogError(ctx, model.EventCategoryAuth, "Login failed", nil, nil)
	if err != nil {
		t.Fatalf("LogError failed: %v", err)
	}

	var level string
	err = db.QueryRow("SELECT level FROM events").Scan(&level)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if level != "error" {
		t.Errorf("level = %q, want %q", level, "error")
	}
}

func TestLogMigratorEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	userID := int64(1)
	err := svc.LogMigratorEvent(ctx, model.EventLevelInfo, "Content imported from elefant", &userID, map[string]any{
		"source":         "elefant",
		"posts_imported": 10,
		"tags_imported":  5,
		"media_imported": 3,
	})
	if err != nil {
		t.Fatalf("LogMigratorEvent failed: %v", err)
	}

	var level, category, message string
	err = db.QueryRow("SELECT level, category, message FROM events").Scan(&level, &category, &message)
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
}

func TestLogAuthEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogAuthEvent(ctx, model.EventLevelInfo, "User logged in", nil, nil)
	if err != nil {
		t.Fatalf("LogAuthEvent failed: %v", err)
	}

	var category string
	err = db.QueryRow("SELECT category FROM events").Scan(&category)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if category != "auth" {
		t.Errorf("category = %q, want %q", category, "auth")
	}
}

func TestLogPageEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogPageEvent(ctx, model.EventLevelInfo, "Page published", nil, nil)
	if err != nil {
		t.Fatalf("LogPageEvent failed: %v", err)
	}

	var category string
	err = db.QueryRow("SELECT category FROM events").Scan(&category)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if category != "page" {
		t.Errorf("category = %q, want %q", category, "page")
	}
}

func TestLogUserEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogUserEvent(ctx, model.EventLevelInfo, "User created", nil, nil)
	if err != nil {
		t.Fatalf("LogUserEvent failed: %v", err)
	}

	var category string
	err = db.QueryRow("SELECT category FROM events").Scan(&category)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if category != "user" {
		t.Errorf("category = %q, want %q", category, "user")
	}
}

func TestLogConfigEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogConfigEvent(ctx, model.EventLevelInfo, "Config updated", nil, nil)
	if err != nil {
		t.Fatalf("LogConfigEvent failed: %v", err)
	}

	var category string
	err = db.QueryRow("SELECT category FROM events").Scan(&category)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if category != "config" {
		t.Errorf("category = %q, want %q", category, "config")
	}
}

func TestLogSystemEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogSystemEvent(ctx, model.EventLevelInfo, "System started", nil, nil)
	if err != nil {
		t.Fatalf("LogSystemEvent failed: %v", err)
	}

	var category string
	err = db.QueryRow("SELECT category FROM events").Scan(&category)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if category != "system" {
		t.Errorf("category = %q, want %q", category, "system")
	}
}

func TestLogCacheEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogCacheEvent(ctx, model.EventLevelInfo, "Cache cleared", nil, nil)
	if err != nil {
		t.Fatalf("LogCacheEvent failed: %v", err)
	}

	var category string
	err = db.QueryRow("SELECT category FROM events").Scan(&category)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if category != "cache" {
		t.Errorf("category = %q, want %q", category, "cache")
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
	err = svc.LogInfo(ctx, "test", "Recent event", nil, nil)
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
