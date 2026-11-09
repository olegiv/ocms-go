// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// seedAIDraftRevisions inserts an unpublished AI draft whose revisions 1..n are
// recorded, directly: the job reads rows, whatever wrote them.
func seedAIDraftRevisions(t *testing.T, db *sql.DB, n int64) int64 {
	t.Helper()
	q := store.New(db)
	now := time.Now().UTC()
	user, err := q.CreateUser(t.Context(), store.CreateUserParams{Email: "scheduler-retention@example.test", PasswordHash: "unused", Role: "editor", Name: "Editor", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	page, err := q.CreatePage(t.Context(), store.CreatePageParams{Title: "Draft", Slug: "scheduler-retention", Body: "<p>x</p>", Status: "draft", AuthorID: user.ID, LanguageCode: "en", PageType: "page", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SetPageAIAccess(t.Context(), store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: user.ID, SetAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE pages SET revision=? WHERE id=?", n, page.ID); err != nil {
		t.Fatal(err)
	}
	for revision := int64(1); revision <= n; revision++ {
		if _, err = db.Exec(`INSERT INTO page_versions(page_id,title,body,changed_by,created_at,revision,snapshot_json,legacy) VALUES (?,?,?,?,?,?,?,0)`, page.ID, "Draft", "<p>x</p>", user.ID, now, revision, `{"title":"Draft"}`); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO content_provenance(page_id,revision,user_id,source,api_key_id,transport,validated_json,reported_json,created_at) VALUES (?,?,?,'api',1,'mcp','{}','{}',?)`, page.ID, revision, user.ID, now); err != nil {
			t.Fatal(err)
		}
	}
	return page.ID
}

func versions(t *testing.T, db *sql.DB, pageID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM page_versions WHERE page_id=?", pageID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRevisionRetentionUsesRegistryOverridePrunesAndReportsFailure(t *testing.T) {
	db, closeDB := testutil.TestDB(t)
	defer closeDB()
	logger := testutil.TestLoggerSilent()
	r := NewRegistry(db, logger)
	if _, err := db.Exec(`INSERT INTO scheduler_overrides(source,name,override_schedule) VALUES ('core','ai_revision_cleanup','*/30 * * * *')`); err != nil {
		t.Fatal(err)
	}
	s := New(db, logger, r)
	if err := s.AddRevisionRetention(); err != nil {
		t.Fatal(err)
	}
	jobs := r.List()
	if len(jobs) != 1 || jobs[0].Source != "core" || jobs[0].Name != "ai_revision_cleanup" || jobs[0].Schedule != "*/30 * * * *" || !jobs[0].CanTrigger || jobs[0].Description == "" {
		t.Fatalf("job not registered with override: %+v", jobs)
	}

	draft := seedAIDraftRevisions(t, db, 25)
	if err := r.TriggerNow("core", "ai_revision_cleanup"); err != nil {
		t.Fatal("manual retention failed", err)
	}
	if got := versions(t, db, draft); got != 20 {
		t.Fatalf("job left %d revisions, want the newest 20", got)
	}

	if _, err := db.Exec(`CREATE TRIGGER retention_failure BEFORE DELETE ON page_versions BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE pages SET revision=30 WHERE id=?", draft); err != nil {
		t.Fatal(err)
	}
	if err := r.TriggerNow("core", "ai_revision_cleanup"); err == nil {
		t.Fatal("retention failure was swallowed")
	}
}

func TestRevisionRetentionRegistersWithoutRegistry(t *testing.T) {
	db, closeDB := testutil.TestDB(t)
	defer closeDB()
	if err := New(db, testutil.TestLoggerSilent(), nil).AddRevisionRetention(); err != nil {
		t.Fatal(err)
	}
}
