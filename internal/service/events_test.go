// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/model"
)

func TestLogEventCapsMetadataAndPreservesStructuredFields(t *testing.T) {
	db := setupEventTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	metadata := map[string]any{
		"count": 3, "allowed": true, "nested": []any{"read", 7},
		"oversized_array": []string{strings.Repeat("x", 5000)},
		"multibyte":       strings.Repeat("界", 5000),
	}
	for _, name := range []string{"x", "y", "z"} {
		metadata[name] = strings.Repeat("<\x00", 5000)
	}
	if err := NewEventService(db).LogSecurityEvent(t.Context(), model.EventLevelWarning,
		"request rejected", nil, "192.0.2.1", "/"+strings.Repeat("x", 10000), metadata); err != nil {
		t.Fatal(err)
	}
	var raw, path string
	if err := db.QueryRow("SELECT metadata, request_url FROM events").Scan(&raw, &path); err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &got); err != nil || len(raw) > 4096 || len(path) != 256 {
		t.Fatalf("invalid or oversized audit row: metadata=%d path=%d err=%v", len(raw), len(path), err)
	}
	if string(got["count"]) != "3" || string(got["allowed"]) != "true" ||
		string(got["nested"]) != `["read",7]` || got["oversized_array"] != nil {
		t.Fatalf("structured fields changed")
	}
	var value string
	if err := json.Unmarshal(got["multibyte"], &value); err != nil || value != strings.Repeat("界", 341) {
		t.Fatal("multibyte field was lost or corrupted")
	}
}

func TestAnonymousEventBudgetIsScopedAndBounded(t *testing.T) {
	var limiter anonymousEventLimiter
	now := time.Now()
	if !limiter.allow("rejected", "::ffff:192.0.2.1", now) ||
		limiter.allow("rejected", "192.0.2.1", now) ||
		!limiter.allow("different", "192.0.2.1", now) ||
		!limiter.allow("rejected", "2001:db8::1", now) ||
		limiter.allow("rejected", "2001:db8::2", now) ||
		!limiter.allow("rejected", "2001:db8:0:1::1", now) {
		t.Fatal("sampling did not distinguish the message and canonical address")
	}
	ip := netip.MustParseAddr("198.18.0.0")
	for len(limiter.seen) < maxAnonymousEventSources {
		if !limiter.allow("rejected", ip.String(), now) {
			t.Fatal("source suppressed before capacity")
		}
		ip = ip.Next()
	}
	if limiter.allow("rejected", ip.String(), now) ||
		!limiter.allow("rejected", ip.String(), now.Add(anonymousEventWindow)) {
		t.Fatal("saturated limiter did not recover after expiry")
	}
}

func setupEventTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	db.SetMaxOpenConns(1)

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

	err := svc.LogEvent(ctx, model.EventLevelInfo, model.EventCategoryMigrator, "Test message", new(int64(123)), "192.168.1.100", "/admin/migrator", map[string]any{
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
		{"info", func(svc *EventService, ctx context.Context) error {
			return svc.LogInfo(ctx, model.EventCategoryPage, "Page created", nil, "", "", nil)
		}, "info"},
		{"warning", func(svc *EventService, ctx context.Context) error {
			return svc.LogWarning(ctx, model.EventCategorySystem, "Low disk space", nil, "", "", nil)
		}, "warning"},
		{"error", func(svc *EventService, ctx context.Context) error {
			return svc.LogError(ctx, model.EventCategoryAuth, "Login failed", nil, "", "", nil)
		}, "error"},
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

	err := svc.LogMigratorEvent(ctx, model.EventLevelInfo, "Content imported from elefant", new(int64(1)), "10.0.0.1", "/admin/migrator/elefant/import", map[string]any{
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
		{"auth", func(svc *EventService, ctx context.Context) error {
			return svc.LogAuthEvent(ctx, model.EventLevelInfo, "User logged in", nil, "", "", nil)
		}, "auth"},
		{"page", func(svc *EventService, ctx context.Context) error {
			return svc.LogPageEvent(ctx, model.EventLevelInfo, "Page published", nil, "", "", nil)
		}, "page"},
		{"user", func(svc *EventService, ctx context.Context) error {
			return svc.LogUserEvent(ctx, model.EventLevelInfo, "User created", nil, "", "", nil)
		}, "user"},
		{"config", func(svc *EventService, ctx context.Context) error {
			return svc.LogConfigEvent(ctx, model.EventLevelInfo, "Config updated", nil, "", "", nil)
		}, "config"},
		{"system", func(svc *EventService, ctx context.Context) error {
			return svc.LogSystemEvent(ctx, model.EventLevelInfo, "System started", nil, "", "", nil)
		}, "system"},
		{"cache", func(svc *EventService, ctx context.Context) error {
			return svc.LogCacheEvent(ctx, model.EventLevelInfo, "Cache cleared", nil, "", "", nil)
		}, "cache"},
		{"media", func(svc *EventService, ctx context.Context) error {
			return svc.LogMediaEvent(ctx, model.EventLevelInfo, "Media uploaded", nil, "", "", nil)
		}, "media"},
		{"tag", func(svc *EventService, ctx context.Context) error {
			return svc.LogTagEvent(ctx, model.EventLevelInfo, "Tag created", nil, "", "", nil)
		}, "tag"},
		{"category", func(svc *EventService, ctx context.Context) error {
			return svc.LogCategoryEvent(ctx, model.EventLevelInfo, "Category created", nil, "", "", nil)
		}, "category"},
		{"menu", func(svc *EventService, ctx context.Context) error {
			return svc.LogMenuEvent(ctx, model.EventLevelInfo, "Menu created", nil, "", "", nil)
		}, "menu"},
		{"api_key", func(svc *EventService, ctx context.Context) error {
			return svc.LogAPIKeyEvent(ctx, model.EventLevelInfo, "API key created", nil, "", "", nil)
		}, "api_key"},
		{"webhook", func(svc *EventService, ctx context.Context) error {
			return svc.LogWebhookEvent(ctx, model.EventLevelInfo, "Webhook created", nil, "", "", nil)
		}, "webhook"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupEventTestDB(t)
			defer func() { _ = db.Close() }()
			testEventField(t, db, tt.logFn, "category", tt.expected)
		})
	}
}
