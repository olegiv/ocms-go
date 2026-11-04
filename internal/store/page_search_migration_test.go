// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// queuedPageIDs returns the page IDs waiting in page_search_queue.
func queuedPageIDs(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT page_id FROM page_search_queue ORDER BY page_id`)
	if err != nil {
		t.Fatalf("reading page_search_queue: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func createSearchTestPage(t *testing.T, q *Queries, ctx context.Context, slug, status string) Page {
	t.Helper()
	now := time.Now()
	user := createTestUser(t, q, ctx, slug+"@example.com")
	page, err := q.CreatePage(ctx, CreatePageParams{
		Title: "Greeting", Slug: slug, Status: status, AuthorID: user.ID,
		Body:         `<p style="text-align: center">Hello <b>world</b></p>`,
		LanguageCode: getDefaultLangCode(t, q, ctx), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	return page
}

// TestPageSearchMigrationIsPlainSQL covers the page_search migration (issue
// #203). Its triggers only queue page IDs, so any program can write pages:
// nothing in the schema calls oCMS code. Existing pages are queued for
// indexing on upgrade, pages_fts keeps working for older oCMS versions, and
// rolling back removes everything the migration added.
func TestPageSearchMigrationIsPlainSQL(t *testing.T) {
	ctx := context.Background()
	db, err := NewDB(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	q := New(db)

	const before = "20261022100000"
	if err := RunMigrationCommand(ctx, db, "up-to", before); err != nil {
		t.Fatalf("migrate up to %s: %v", before, err)
	}
	existing := createSearchTestPage(t, q, ctx, "existing", "published")

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := queuedPageIDs(t, db); !slices.Equal(got, []int64{existing.ID}) {
		t.Fatalf("after upgrade the queue holds %v, want the existing page %d", got, existing.ID)
	}

	// The triggers queue every insert, update and delete, drafts included.
	if _, err := db.Exec(`DELETE FROM page_search_queue`); err != nil {
		t.Fatal(err)
	}
	draft := createSearchTestPage(t, q, ctx, "draft", "draft")
	// A plain SQL write, as the sqlite3 tool or an older oCMS would make it.
	if _, err := db.Exec(`UPDATE pages SET body = '<p>edited</p>' WHERE id = ?`, existing.ID); err != nil {
		t.Fatalf("plain SQL update of a page: %v", err)
	}
	if got := queuedPageIDs(t, db); !slices.Equal(got, []int64{existing.ID, draft.ID}) {
		t.Errorf("after insert and update the queue holds %v, want %v", got, []int64{existing.ID, draft.ID})
	}
	// A change that leaves the searchable text alone queues nothing.
	if _, err := db.Exec(`DELETE FROM page_search_queue`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE pages SET status = 'published', scheduled_at = NULL WHERE id = ?`, draft.ID); err != nil {
		t.Fatalf("status update: %v", err)
	}
	if got := queuedPageIDs(t, db); len(got) != 0 {
		t.Errorf("a status change queued %v, want nothing", got)
	}
	if _, err := db.Exec(`DELETE FROM page_search_queue`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM pages WHERE id = ?`, draft.ID); err != nil {
		t.Fatalf("plain SQL delete of a page: %v", err)
	}
	if got := queuedPageIDs(t, db); !slices.Equal(got, []int64{draft.ID}) {
		t.Errorf("after delete the queue holds %v, want the deleted page %d", got, draft.ID)
	}

	// pages_fts, which older oCMS versions search, is still maintained.
	var raw string
	if err := db.QueryRow(`SELECT body FROM pages_fts WHERE rowid = ?`, existing.ID).Scan(&raw); err != nil || raw != "<p>edited</p>" {
		t.Errorf("pages_fts body = %q (err %v), want the stored body", raw, err)
	}

	if err := RunMigrationCommand(ctx, db, "down-to", before); err != nil {
		t.Fatalf("migrate down to %s: %v", before, err)
	}
	var leftovers int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'page_search%'`).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Errorf("rolling back left %d page_search objects", leftovers)
	}
	if _, err := db.Exec(`UPDATE pages SET body = '<p>after rollback</p>' WHERE id = ?`, existing.ID); err != nil {
		t.Errorf("page update after rolling back: %v", err)
	}
}
