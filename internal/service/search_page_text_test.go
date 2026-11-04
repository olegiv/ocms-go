// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// TestSearchPublishedPagesMatchesPageTextOnly is the regression test for
// issue #203: a word that occurs only in a page's markup must not match, and
// the highlight must show page text, never HTML. It runs against the real
// schema, so the page_search queue triggers, the sync before each search and
// RebuildIndex are all covered.
func TestSearchPublishedPagesMatchesPageTextOnly(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewDB(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q := store.New(db)
	now := time.Now()
	user, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "search@example.com", PasswordHash: "hash", Role: "editor", Name: "Search", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	lang, err := q.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("GetDefaultLanguage: %v", err)
	}
	page, err := q.CreatePage(ctx, store.CreatePageParams{
		Title: "Formatting", Slug: "formatting", Status: "published", AuthorID: user.ID,
		Body:         `<p style="text-align: center"><span style="color: #e03e2d">Highlighted &lt;b&gt; words</span></p><p>more text</p>`,
		LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	svc := NewSearchService(db)
	check := func(stage string) {
		t.Helper()
		for _, markupOnly := range []string{"center", "e03e2d", "style", "span", "text-align"} {
			results, total, err := svc.SearchPublishedPages(ctx, SearchParams{Query: markupOnly, Limit: 10})
			if err != nil {
				t.Fatalf("%s: SearchPublishedPages(%q): %v", stage, markupOnly, err)
			}
			if total != 0 || len(results) != 0 {
				t.Errorf("%s: %q occurs only in markup but matched %d pages", stage, markupOnly, total)
			}
		}
		results, total, err := svc.SearchPublishedPages(ctx, SearchParams{Query: "Highlighted", Limit: 10})
		if err != nil {
			t.Fatalf("%s: SearchPublishedPages(Highlighted): %v", stage, err)
		}
		if total != 1 || len(results) != 1 {
			t.Fatalf("%s: Highlighted matched %d pages, want 1", stage, total)
		}
		const want = "<mark>Highlighted</mark> &lt;b&gt; words more text"
		if results[0].Highlight != want {
			t.Errorf("%s: highlight = %q, want %q", stage, results[0].Highlight, want)
		}
	}

	check("indexed from the queue")
	if err := svc.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	check("after RebuildIndex")

	// Draft (all-status) search matches the readable text too, including
	// entity-encoded words and words split by inline tags.
	for query, want := range map[string]int64{"e03e2d": 0, "text-align": 0, "Highlighted": 1, "<b>": 1, "more text": 1} {
		_, total, err := svc.SearchAllPages(ctx, SearchParams{Query: query, Limit: 10})
		if err != nil {
			t.Fatalf("SearchAllPages(%q): %v", query, err)
		}
		if total != want {
			t.Errorf("SearchAllPages(%q) total = %d, want %d", query, total, want)
		}
	}

	// A plain SQL write, as the sqlite3 tool or an older oCMS makes it, is
	// indexed by the next search.
	if _, err := db.Exec(`UPDATE pages SET body = '<p>Rewritten <i>caf&eacute;</i></p>' WHERE id = ?`, page.ID); err != nil {
		t.Fatalf("plain SQL update: %v", err)
	}
	for query, want := range map[string]int64{"Highlighted": 0, "café": 1} {
		if _, total, err := svc.SearchPublishedPages(ctx, SearchParams{Query: query, Limit: 10}); err != nil || total != want {
			t.Errorf("after a plain SQL update, SearchPublishedPages(%q) = %d (err %v), want %d", query, total, err, want)
		}
	}
	if _, err := db.Exec(`DELETE FROM pages WHERE id = ?`, page.ID); err != nil {
		t.Fatalf("plain SQL delete: %v", err)
	}
	if _, total, err := svc.SearchAllPages(ctx, SearchParams{Query: "café", Limit: 10}); err != nil || total != 0 {
		t.Errorf("after deleting the page, SearchAllPages(café) = %d (err %v), want 0", total, err)
	}
	var indexed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM page_search`).Scan(&indexed); err != nil || indexed != 0 {
		t.Errorf("page_search holds %d entries after the delete (err %v), want 0", indexed, err)
	}
}
