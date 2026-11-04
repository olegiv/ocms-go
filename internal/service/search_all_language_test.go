// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"testing"
)

// TestSearchAllPagesFiltersByLanguage checks the language filter of the
// drafts-visible search (MCP search_pages with the drafts policy): it narrows
// the same page-text search to one language across statuses, keeps LIKE
// wildcards literal, pages consistently and returns nothing for a language
// without pages. The merge of master's page-text search (#205) moved this
// filter into raw SQL.
//
// Bug state: drop the language clause or its argument from SearchAllPages
// and this fails.
func TestSearchAllPagesFiltersByLanguage(t *testing.T) {
	db := searchTestDB(t)
	service := NewSearchService(db)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO pages (title, slug, body, status, language_code, updated_at) VALUES
		('Alpha', 'alpha', '<p>shared word, 50% off</p>', 'draft', 'en', '2026-11-01 10:00:00'),
		('Beta', 'beta', '<p>shared word, 50% off</p>', 'draft', 'ru', '2026-11-01 11:00:00'),
		('Gamma', 'gamma', '<p>shared word, 500 off</p>', 'published', 'ru', '2026-11-01 12:00:00'),
		('Delta', 'delta', '<p>shared word, 50% off</p>', 'published', 'ru', '2026-11-01 13:00:00')`); err != nil {
		t.Fatal(err)
	}
	titles := func(query, lang string, limit, offset int) ([]string, int64) {
		t.Helper()
		results, total, err := service.SearchAllPages(ctx, SearchParams{Query: query, LanguageCode: lang, Limit: limit, Offset: offset})
		if err != nil {
			t.Fatalf("SearchAllPages(%q, %q): %v", query, lang, err)
		}
		var got []string
		for _, r := range results {
			got = append(got, r.Title)
		}
		return got, total
	}
	if got, total := titles("shared", "ru", 10, 0); total != 3 || len(got) != 3 {
		t.Errorf("ru shared: %v (total %d), want the three ru pages of every status", got, total)
	}
	if got, total := titles("50%", "ru", 10, 0); total != 2 || len(got) != 2 || got[0] != "Delta" || got[1] != "Beta" {
		t.Errorf("ru 50%%: %v (total %d), want Delta then Beta (literal %%, newest first)", got, total)
	}
	first, total := titles("50%", "ru", 1, 0)
	second, _ := titles("50%", "ru", 1, 1)
	if total != 2 || len(first) != 1 || len(second) != 1 || first[0] == second[0] {
		t.Errorf("ru paging: %v then %v (total %d), want two different pages", first, second, total)
	}
	if got, total := titles("shared", "de", 10, 0); total != 0 || len(got) != 0 {
		t.Errorf("unknown language: %v (total %d), want nothing", got, total)
	}
	if got, total := titles("shared", "", 10, 0); total != 4 || len(got) != 4 {
		t.Errorf("no language filter: %v (total %d), want all four pages", got, total)
	}
}
