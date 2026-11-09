// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestEventRetentionClassifiesEveryCoreCategory(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "../model/event.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	categories := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "EventCategory") {
				continue
			}
			if i >= len(spec.Values) {
				t.Fatalf("category %s must have an explicit value", name.Name)
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Fatalf("category %s no longer has a string literal value", name.Name)
			}
			category, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			categories[category] = false
		}
		return true
	})
	if len(categories) == 0 {
		t.Fatal("no core event categories found")
	}
	rules := make(map[string][]string)
	for _, group := range slices.Concat(diagnosticEventProducers, auditEventProducers) {
		if _, known := categories[group.category]; !known {
			t.Errorf("retention rule for undeclared category %q", group.category)
		}
		categories[group.category] = true
		previous, exists := rules[group.category]
		if exists && (previous == nil || group.messages == nil) {
			t.Errorf("whole-category rule for %q overlaps another rule", group.category)
		}
		for _, message := range group.messages {
			for _, old := range previous {
				if old == message {
					t.Errorf("overlapping rules for %q / %q", group.category, message)
				}
			}
			previous = append(previous, message)
		}
		rules[group.category] = previous
	}
	for category, covered := range categories {
		if !covered {
			t.Errorf("no explicit retention policy for core category %q", category)
		}
	}
}

func TestEventRetentionKeepsRowsAtCutoff(t *testing.T) {
	db, closeDB := testutil.TestDB(t)
	t.Cleanup(closeDB)
	now := time.Date(2026, time.November, 9, 12, 30, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -90)
	if _, err := db.Exec(`INSERT INTO events(category, message, created_at)
		VALUES ('system', 'Page not found', ?), ('system', 'Page not found', ?),
		       ('system', 'Page not found', ?), ('system', 'Page not found', ?)`,
		cutoff.Format(time.DateTime), cutoff,
		cutoff.Add(-time.Second).Format(time.DateTime), cutoff.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	deleted, remaining, err := cleanupEvents(context.Background(), store.New(db), []eventRetentionRule{{
		category: model.EventCategorySystem, messages: `["Page not found"]`, days: 90,
	}}, now)
	if err != nil || deleted != 2 || remaining != 0 {
		t.Fatalf("retention deleted=%d, remaining=%d, err=%v", deleted, remaining, err)
	}
}

func TestEventRetentionReportsPartialFailureAndCanRetry(t *testing.T) {
	db, closeDB := testutil.TestDB(t)
	t.Cleanup(closeDB)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	registry := NewRegistry(db, logger)
	s := New(db, logger, registry)
	if _, err := db.Exec(`
		INSERT INTO events(category, message, created_at) VALUES
		  ('system', 'Page not found', '2020-01-01 00:00:00'),
		  ('cache', 'failed to preload caches', '2020-01-01 00:00:00');
		CREATE TRIGGER reject_cache_delete BEFORE DELETE ON events
		  WHEN OLD.category = 'cache' BEGIN SELECT RAISE(ABORT, 'test failure'); END;
	`); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEventCleanup(true, 90, 0); err != nil {
		t.Fatal(err)
	}
	if err := registry.TriggerNow("core", "event_cleanup"); err == nil || !strings.Contains(err.Error(), "test failure") {
		t.Fatalf("trigger result = %v, want the database failure", err)
	}
	if !strings.Contains(logs.String(), `"deleted":1,"remaining_known":false`) {
		t.Errorf("partial run did not report its progress: %s", logs.String())
	}
	var category string
	if err := db.QueryRow("SELECT category FROM events").Scan(&category); err != nil || category != "cache" {
		t.Fatalf("after partial run, remaining category = %q, err=%v", category, err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_cache_delete"); err != nil {
		t.Fatal(err)
	}
	if err := registry.TriggerNow("core", "event_cleanup"); err != nil {
		t.Fatal(err)
	}
	if count, err := store.New(db).CountEvents(context.Background()); err != nil || count != 0 {
		t.Fatalf("after retry, remaining count = %d, err=%v", count, err)
	}
}

func TestEventRetentionProtectsAuditAndUnknownEvents(t *testing.T) {
	for _, tc := range []struct {
		name       string
		diagnostic int
		audit      int
		removed    map[string]bool
	}{
		{"default", 90, 0, map[string]bool{"Page not found": true, "failed to preload caches": true}},
		{"finite audit and shorter diagnostic window", 30, 60, map[string]bool{
			"Page not found": true, "failed to preload caches": true, "Server started": true,
			"Login failed": true, "MFA reset": true, "API key revoked": true, "MCP settings updated": true,
			"Preview share link revoked": true, "CSRF validation failed": true, "All caches cleared": true,
		}},
		{"diagnostics kept", 0, 60, map[string]bool{
			"Login failed": true, "MFA reset": true, "API key revoked": true, "MCP settings updated": true,
			"Preview share link revoked": true, "CSRF validation failed": true, "All caches cleared": true,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, closeDB := testutil.TestDB(t)
			t.Cleanup(closeDB)
			logger := testutil.TestLoggerSilent()
			registry := NewRegistry(db, logger)
			s := New(db, logger, registry)
			now := time.Now().UTC()
			events := []struct {
				category string
				message  string
				age      int
			}{
				{model.EventCategorySystem, "Page not found", 91},
				{model.EventCategorySystem, "Server started", 45},
				{model.EventCategoryCache, "failed to preload caches", 95},
				{model.EventCategoryAuth, "Login failed", 120},
				{model.EventCategoryUser, "MFA reset", 120},
				{model.EventCategoryAPIKey, "API key revoked", 120},
				{model.EventCategoryConfig, "MCP settings updated", 120},
				{model.EventCategoryPage, "Preview share link revoked", 120},
				{model.EventCategorySystem, "CSRF validation failed", 120},
				{model.EventCategoryCache, "All caches cleared", 120},
				{model.EventCategorySecurity, "Recent security event", 50},
				{model.EventCategorySystem, "Unrecognized system event", 120},
				{"custom_module", "Page not found", 120},
			}
			for _, event := range events {
				// SQLite's legacy timestamp text and current driver timestamps
				// both occur in existing installations.
				when := now.AddDate(0, 0, -event.age)
				if _, err := db.Exec("INSERT INTO events(category, message, created_at) VALUES (?, ?, ?)",
					event.category, event.message, when.Format(time.DateTime)); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.AddEventCleanup(true, tc.diagnostic, tc.audit); err != nil {
				t.Fatal(err)
			}
			if err := registry.TriggerNow("core", "event_cleanup"); err != nil {
				t.Fatal(err)
			}
			// This is the data source behind the admin Event Log.
			rows, err := store.New(db).ListEventsWithUser(context.Background(), store.ListEventsWithUserParams{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			kept := make(map[[2]string]bool)
			for _, row := range rows {
				kept[[2]string{row.Category, row.Message}] = true
			}
			for _, event := range events {
				want := event.category == "custom_module" || !tc.removed[event.message]
				if got := kept[[2]string{event.category, event.message}]; got != want {
					t.Errorf("kept %s/%q = %v, want %v", event.category, event.message, got, want)
				}
			}
		})
	}
}

func TestEventRetentionDrainsBacklogInBoundedRuns(t *testing.T) {
	db, closeDB := testutil.TestDB(t)
	t.Cleanup(closeDB)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	registry := NewRegistry(db, logger)
	s := New(db, logger, registry)
	now := time.Now().UTC()
	if _, err := db.Exec(`WITH RECURSIVE batch(n) AS (
		SELECT 1 UNION ALL SELECT n+1 FROM batch WHERE n < 23000
	) INSERT INTO events(category, message, created_at) SELECT 'system', 'Page not found', ? FROM batch`,
		now.AddDate(0, 0, -120)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events(category, message, created_at)
		VALUES ('system', 'Page not found', ?), ('cache', 'failed to preload caches', ?), ('auth', 'Login failed', ?)`,
		now.AddDate(0, 0, -91), now.AddDate(0, 0, -91), now.AddDate(0, 0, -91)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEventCleanup(true, 90, 0); err != nil {
		t.Fatal(err)
	}
	for i, check := range []struct {
		total int
		log   string
	}{
		{13003, `"deleted":10000,"remaining":10001,"remaining_capped":true`},
		{3003, `"deleted":10000,"remaining":3002,"remaining_capped":false`},
		{1, `"deleted":3002,"remaining":0,"remaining_capped":false`},
	} {
		logs.Reset()
		if err := registry.TriggerNow("core", "event_cleanup"); err != nil {
			t.Fatal(err)
		}
		var total, auth, cache, newest int
		if err := db.QueryRow(`SELECT COUNT(*), COUNT(CASE WHEN category = 'auth' THEN 1 END),
			COUNT(CASE WHEN category = 'cache' THEN 1 END),
			COUNT(CASE WHEN category = 'system' AND created_at >= ? THEN 1 END) FROM events`,
			now.AddDate(0, 0, -92)).Scan(&total, &auth, &cache, &newest); err != nil {
			t.Fatal(err)
		}
		if total != check.total || auth != 1 || cache != 0 || (newest == 1) != (i < 2) {
			t.Errorf("run %d left %d total, %d auth, %d cache, %d newest", i+1, total, auth, cache, newest)
		}
		if !strings.Contains(logs.String(), check.log) {
			t.Errorf("run %d log = %s, want %s", i+1, logs.String(), check.log)
		}
	}
}
