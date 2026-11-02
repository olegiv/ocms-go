// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSearchPublishedPagesPreservesTypesAndVisibility(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE pages (
			id INTEGER PRIMARY KEY, title TEXT, slug TEXT, body TEXT,
			status TEXT, page_type TEXT, exclude_from_lists INTEGER,
			language_code TEXT, published_at DATETIME, created_at DATETIME,
			updated_at DATETIME, featured_image_id INTEGER
		);
		CREATE VIRTUAL TABLE pages_fts USING fts5(title, body);
		INSERT INTO pages VALUES
			(1,'needle article','article','needle body','published','post',0,'en',NULL,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,NULL),
			(2,'needle guide','guide','needle body','published','page',0,'en',NULL,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,NULL),
			(3,'needle draft','draft','needle body','draft','post',0,'en',NULL,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,NULL),
			(4,'needle excluded','excluded','needle body','published','post',1,'en',NULL,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,NULL);
		INSERT INTO pages_fts(rowid,title,body) SELECT id,title,body FROM pages;
	`)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSearchService(db)
	results, total, err := svc.SearchPublishedPages(context.Background(), SearchParams{Query: "needle", Limit: 10, LanguageCode: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(results) != 2 {
		t.Fatalf("results=%v total=%d; expected only the two published/listed records", results, total)
	}
	for _, result := range results {
		want, exists := map[int64]string{1: "post", 2: "page"}[result.ID]
		if !exists || result.PageType != want {
			t.Errorf("result %d has type %q, want %q", result.ID, result.PageType, want)
		}
	}
	results, total, err = svc.SearchPublishedPages(context.Background(), SearchParams{Query: "absentword", Limit: 10})
	if err != nil || total != 0 || len(results) != 0 {
		t.Fatalf("no-match search: results=%v total=%d err=%v", results, total, err)
	}
}
