// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestGetPageStatsExcludesTaxonomyArchives pins the fix for a real over-count.
//
// getPageStats matches language-prefixed paths with LIKE "%/slug", which is
// anchored only at the end — so it also matched /category/slug and /tag/slug. A
// post whose slug doubles as a category or tag slug therefore absorbed its own
// archive's views. That is not hypothetical: on a live site four of twenty-two
// published posts collided, and one article reported 22 views instead of 16.
//
// page_id is never populated by the tracker, so path matching is the only
// mechanism available and the filter has to be right.
func TestGetPageStatsExcludesTaxonomyArchives(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	now := time.Now()
	record := func(path string) {
		t.Helper()
		if err := m.insertPageView(&PageView{
			VisitorHash: "visitor-hash-x",
			SessionHash: "session-hash-x",
			Path:        path,
			DeviceType:  "desktop",
			CreatedAt:   now,
		}); err != nil {
			t.Fatalf("insertPageView(%q): %v", path, err)
		}
	}

	// "karfagen" is simultaneously an article slug, a category slug and a tag
	// slug — the exact collision shape seen in production.
	counted := []string{
		"/karfagen",    // the article itself
		"/ru/karfagen", // the same article under a language prefix
	}
	excluded := []string{
		"/category/karfagen",    // taxonomy archive
		"/ru/category/karfagen", // ...and its language-prefixed form
		"/tag/karfagen",
		"/ru/tag/karfagen",
	}
	for _, p := range counted {
		record(p)
	}
	for _, p := range excluded {
		record(p)
	}

	stats := m.getPageStats(context.Background(), "/karfagen")

	if got, want := stats.Views, int64(len(counted)); got != want {
		t.Errorf("Views = %d, want %d — the archive paths %v must not be counted "+
			"toward the article", got, want, excluded)
	}
}

// TestGetPageStatsCountsPostSlugMatchingTaxonomyPrefix guards the other side of
// the filter: the exclusion must not swallow a legitimate article.
//
// The patterns require a slash AFTER the segment ("%/category/%"), and slugs
// never contain slashes, so a post whose slug is literally "category" lives at
// /category and has to keep its views.
func TestGetPageStatsCountsPostSlugMatchingTaxonomyPrefix(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	defer func() { _ = m.Shutdown() }()

	now := time.Now()
	for _, path := range []string{"/category", "/ru/category", "/tag"} {
		if err := m.insertPageView(&PageView{
			VisitorHash: "visitor-hash-y",
			SessionHash: "session-hash-y",
			Path:        path,
			DeviceType:  "desktop",
			CreatedAt:   now,
		}); err != nil {
			t.Fatalf("insertPageView(%q): %v", path, err)
		}
	}

	if got := m.getPageStats(context.Background(), "/category").Views; got != 2 {
		t.Errorf("Views for a post slugged \"category\" = %d, want 2 (/category and "+
			"/ru/category); the taxonomy filter must not swallow it", got)
	}
	if got := m.getPageStats(context.Background(), "/tag").Views; got != 1 {
		t.Errorf("Views for a post slugged \"tag\" = %d, want 1", got)
	}
}
