// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	_ "github.com/mattn/go-sqlite3"
)

func TestSearchServiceEscapeQuery(t *testing.T) {
	service := &SearchService{}

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "simple word",
			query: "hello",
			want:  `"hello"*`,
		},
		{
			name:  "multiple words",
			query: "hello world",
			want:  `"hello"* OR "world"*`,
		},
		{
			name:  "special characters removed",
			query: "hello:world*test",
			want:  `"hello"* OR "world"* OR "test"*`,
		},
		{
			name:  "quotes removed",
			query: `"quoted phrase"`,
			want:  `"quoted"* OR "phrase"*`,
		},
		{
			name:  "parentheses removed",
			query: "(hello OR world)",
			want:  `"hello"* OR "OR"* OR "world"*`,
		},
		{
			name:  "empty string",
			query: "",
			want:  "",
		},
		{
			name:  "only special characters",
			query: ":*^()",
			want:  "",
		},
		{
			name:  "unicode characters preserved (Cyrillic)",
			query: "привет мир",
			want:  `"привет"* OR "мир"*`,
		},
		{
			name:  "unicode characters preserved (Chinese)",
			query: "你好世界",
			want:  `"你好世界"*`,
		},
		{
			name:  "mixed ASCII and Unicode",
			query: "hello мир test",
			want:  `"hello"* OR "мир"* OR "test"*`,
		},
		{
			name:  "whitespace trimmed",
			query: "  hello  world  ",
			want:  `"hello"* OR "world"*`,
		},
		{
			name:  "underscore preserved",
			query: "hello_world",
			want:  `"hello_world"*`,
		},
		{
			name:  "hyphen preserved",
			query: "hello-world",
			want:  `"hello-world"*`,
		},
		{
			name:  "numbers preserved",
			query: "test123",
			want:  `"test123"*`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.escapeQuery(tt.query)
			if got != tt.want {
				t.Errorf("escapeQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestSearchServiceGenerateExcerpt(t *testing.T) {
	service := &SearchService{}

	tests := []struct {
		name   string
		body   string
		query  string
		maxLen int
		check  func(t *testing.T, got string)
	}{
		{
			name:   "short body",
			body:   "Hello world",
			query:  "hello",
			maxLen: 200,
			check: func(t *testing.T, got string) {
				if got != "Hello world" {
					t.Errorf("got %q, want %q", got, "Hello world")
				}
			},
		},
		{
			name:   "body longer than maxLen without match",
			body:   "Lorem ipsum dolor sit amet consectetur adipiscing elit. This is some very long text that exceeds the maximum length we want to show in the excerpt.",
			query:  "xyz",
			maxLen: 50,
			check: func(t *testing.T, got string) {
				if len(got) > 60 { // Allow some margin for ...
					t.Errorf("excerpt too long: %d chars", len(got))
				}
			},
		},
		{
			name:   "empty body",
			body:   "",
			query:  "test",
			maxLen: 200,
			check: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("got %q, want empty", got)
				}
			},
		},
		{
			name:   "multi-byte text is never split",
			body:   "<p>" + strings.Repeat("Привет мир ", 30) + "</p>",
			query:  "мир",
			maxLen: 25,
			check: func(t *testing.T, got string) {
				if !utf8.ValidString(got) {
					t.Errorf("excerpt is not valid UTF-8: %q", got)
				}
				if n := utf8.RuneCountInString(strings.Trim(got, ".")); n > 25 {
					t.Errorf("excerpt has %d characters, want at most 25", n)
				}
			},
		},
		{
			name:   "markup words are not excerpt matches",
			body:   `<p style="text-align: center">Intro text</p><p>` + strings.Repeat("filler ", 40) + `</p>`,
			query:  "center",
			maxLen: 20,
			check: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "Intro text") || strings.Contains(got, "center") {
					t.Errorf("got %q, want the start of the page text", got)
				}
			},
		},
		{
			name:   "html tags stripped",
			body:   "<p>Hello <strong>world</strong></p>",
			query:  "hello",
			maxLen: 200,
			check: func(t *testing.T, got string) {
				if got == "" {
					t.Error("excerpt should not be empty")
				}
				// Should not contain HTML tags
				if contains(got, "<") || contains(got, ">") {
					t.Errorf("excerpt should not contain HTML tags: %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.generateExcerpt(tt.body, tt.query, tt.maxLen)
			tt.check(t, got)
		})
	}
}

func TestSearchParams(t *testing.T) {
	params := SearchParams{
		Query:  "test query",
		Limit:  10,
		Offset: 20,
	}

	if params.Query != "test query" {
		t.Errorf("Query = %q, want %q", params.Query, "test query")
	}
	if params.Limit != 10 {
		t.Errorf("Limit = %d, want 10", params.Limit)
	}
	if params.Offset != 20 {
		t.Errorf("Offset = %d, want 20", params.Offset)
	}
}

func TestNewSearchService(t *testing.T) {
	// NewSearchService with nil db (just testing it doesn't panic)
	service := NewSearchService(nil)
	if service == nil {
		t.Fatal("NewSearchService(nil) returned nil")
	}
	if service.db != nil {
		t.Error("db should be nil when passed nil")
	}
}

// highlightTag matches every tag in a highlight.
var highlightTag = regexp.MustCompile(`<[^>]*>`)

// TestHighlightHTML pins the Highlight contract themes rely on when they
// render it unescaped: escaped page text with <mark> as the only markup.
func TestHighlightHTML(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"marks a match", "a \x01b\x02 c", "a <mark>b</mark> c"},
		{"several matches", "\x01go\x02 and \x01go\x02", "<mark>go</mark> and <mark>go</mark>"},
		{"escapes text", "x < y & \x01z\x02", "x &lt; y &amp; <mark>z</mark>"},
		{"escapes decoded markup", "<script>alert(1)</script> \x01t\x02", "&lt;script&gt;alert(1)&lt;/script&gt; <mark>t</mark>"},
		{"unbalanced markers dropped", "\x01a b", "a b"},
		{"trims", "  \x01t\x02 ...  ", "<mark>t</mark> ..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := highlightHTML(tt.in)
			if got != tt.want {
				t.Errorf("highlightHTML(%q) = %q, want %q", tt.in, got, tt.want)
			}
			for _, tag := range highlightTag.FindAllString(got, -1) {
				if tag != "<mark>" && tag != "</mark>" {
					t.Errorf("highlightHTML(%q) = %q carries tag %q", tt.in, got, tag)
				}
			}
		})
	}
}

// pageSearchMigration is the migration that creates page_search, its queue
// and the queue triggers.
const pageSearchMigration = "20261023120000_create_page_search.sql"

// applyPageSearchMigration runs the Up section of the real page_search
// migration, so hand-made test schemas use the production triggers.
func applyPageSearchMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	migration, err := os.ReadFile(filepath.Join("..", "store", "migrations", pageSearchMigration)) // #nosec G304 -- fixed repo file
	if err != nil {
		t.Fatalf("reading %s: %v", pageSearchMigration, err)
	}
	up, _, found := strings.Cut(string(migration), "-- +goose Down")
	if !found {
		t.Fatalf("%s has no Down section", pageSearchMigration)
	}
	if _, err := db.Exec(up); err != nil {
		t.Fatalf("applying %s: %v", pageSearchMigration, err)
	}
}

// searchTestDB creates an in-memory SQLite database with the pages table and
// both search indexes for search tests.
func searchTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	db.SetMaxOpenConns(1)

	// Create minimal pages table matching the columns SearchAdminPages returns
	_, err = db.Exec(`
		CREATE TABLE pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			body TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			author_id INTEGER NOT NULL DEFAULT 1,
			published_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			featured_image_id INTEGER,
			language_code TEXT NOT NULL DEFAULT 'en',
			meta_title TEXT NOT NULL DEFAULT '',
			meta_description TEXT NOT NULL DEFAULT '',
			meta_keywords TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("failed to create pages table: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE VIRTUAL TABLE pages_fts USING fts5(title, body, meta_title, meta_description, meta_keywords)`); err != nil {
		t.Fatalf("failed to create pages_fts: %v", err)
	}
	applyPageSearchMigration(t, db)
	return db
}

func TestSearchAllPages(t *testing.T) {
	db := searchTestDB(t)
	service := NewSearchService(db)

	ctx := context.Background()

	// Insert test pages with different statuses
	_, err := db.Exec(`INSERT INTO pages (title, slug, body, status) VALUES
		('Hello World', 'hello-world', 'This is the first post about Go programming', 'published'),
		('Draft Post', 'draft-post', 'This draft discusses Go testing patterns', 'draft'),
		('Archived Article', 'archived', 'Archived content about Go modules', 'archived'),
		('Unrelated Page', 'unrelated', 'Nothing to see here about Python', 'published')
	`)
	if err != nil {
		t.Fatalf("failed to insert test pages: %v", err)
	}

	t.Run("matching query returns results", func(t *testing.T) {
		results, total, err := service.SearchAllPages(ctx, SearchParams{
			Query: "Go", Limit: 10, Offset: 0,
		})
		if err != nil {
			t.Fatalf("SearchAllPages failed: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3 (all Go pages regardless of status)", total)
		}
		if len(results) != 3 {
			t.Errorf("len(results) = %d, want 3", len(results))
		}
	})

	t.Run("includes all statuses", func(t *testing.T) {
		results, _, err := service.SearchAllPages(ctx, SearchParams{
			Query: "Go", Limit: 10, Offset: 0,
		})
		if err != nil {
			t.Fatalf("SearchAllPages failed: %v", err)
		}

		statuses := make(map[string]bool)
		for _, r := range results {
			statuses[r.Status] = true
		}
		for _, expected := range []string{"published", "draft", "archived"} {
			if !statuses[expected] {
				t.Errorf("expected status %q in results", expected)
			}
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		results, total, err := service.SearchAllPages(ctx, SearchParams{
			Query: "nonexistent_xyz", Limit: 10, Offset: 0,
		})
		if err != nil {
			t.Fatalf("SearchAllPages failed: %v", err)
		}
		if total != 0 {
			t.Errorf("total = %d, want 0", total)
		}
		if len(results) != 0 {
			t.Errorf("len(results) = %d, want 0", len(results))
		}
	})

	t.Run("empty query returns early", func(t *testing.T) {
		results, total, err := service.SearchAllPages(ctx, SearchParams{
			Query: "", Limit: 10, Offset: 0,
		})
		if err != nil {
			t.Fatalf("SearchAllPages failed: %v", err)
		}
		if total != 0 {
			t.Errorf("total = %d, want 0", total)
		}
		if len(results) != 0 {
			t.Errorf("len(results) = %d, want 0", len(results))
		}
	})

	t.Run("pagination works", func(t *testing.T) {
		// Get first page
		results1, total, err := service.SearchAllPages(ctx, SearchParams{
			Query: "Go", Limit: 2, Offset: 0,
		})
		if err != nil {
			t.Fatalf("SearchAllPages failed: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if len(results1) != 2 {
			t.Errorf("len(results1) = %d, want 2", len(results1))
		}

		// Get second page
		results2, _, err := service.SearchAllPages(ctx, SearchParams{
			Query: "Go", Limit: 2, Offset: 2,
		})
		if err != nil {
			t.Fatalf("SearchAllPages failed: %v", err)
		}
		if len(results2) != 1 {
			t.Errorf("len(results2) = %d, want 1", len(results2))
		}
	})

	t.Run("markup does not match", func(t *testing.T) {
		if _, err := db.Exec(`INSERT INTO pages (title, slug, body, status) VALUES
			('Styled', 'styled', '<p style="text-align: center"><span style="color: #e03e2d">Centred caf&eacute; <b>B</b>old</span></p>', 'draft')`); err != nil {
			t.Fatalf("insert styled page: %v", err)
		}
		for query, want := range map[string]int64{"text-align": 0, "e03e2d": 0, "span": 0, "Centred": 1, "café": 1, "Bold": 1} {
			_, total, err := service.SearchAllPages(ctx, SearchParams{Query: query, Limit: 10})
			if err != nil {
				t.Fatalf("SearchAllPages(%q): %v", query, err)
			}
			if total != want {
				t.Errorf("SearchAllPages(%q) total = %d, want %d", query, total, want)
			}
		}
	})

	t.Run("excerpt is generated", func(t *testing.T) {
		results, _, err := service.SearchAllPages(ctx, SearchParams{
			Query: "Go", Limit: 1, Offset: 0,
		})
		if err != nil {
			t.Fatalf("SearchAllPages failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least 1 result")
		}
		if results[0].Excerpt == "" {
			t.Error("expected non-empty excerpt")
		}
	})
}

func TestSearchAllPagesLiteralWildcards(t *testing.T) {
	db := searchTestDB(t)
	service := NewSearchService(db)
	seeds := []struct{ title, slug, body, status string }{
		{"Rate 50% alpha", "percent", "ordinary", "draft"},
		{"Rate 50x alpha", "percent-decoy", "ordinary", "published"},
		{"file_name alpha", "underscore", "plain", "draft"},
		{"filename alpha", "underscore-decoy", "plain", "draft"},
		{"Wow! alpha", "escape", `C:\tmp`, "draft"},
		{"Mixed alpha", "mixed", "literal !_!% and 50% file_name", "draft"},
		{"Body match alpha", "body", "50%", "draft"},
		{"plain alpha", "plain", "untouched", "published"},
	}
	for i, seed := range seeds {
		_, err := db.Exec(`INSERT INTO pages (id, title, slug, body, status, updated_at)
			VALUES (?, ?, ?, ?, ?, datetime('2026-01-01', '+' || ? || ' seconds'))`,
			i+1, seed.title, seed.slug, seed.body, seed.status, i+1)
		if err != nil {
			t.Fatalf("insert %s: %v", seed.slug, err)
		}
	}
	tests := []struct {
		name, query   string
		limit, offset int
		ids           []int64
		total         int64
	}{
		{"percent", "%", 10, 0, []int64{7, 6, 1}, 3},
		{"underscore", "_", 10, 0, []int64{6, 3}, 2},
		{"escape character", "!", 10, 0, []int64{6, 5}, 2},
		{"backslash", `\`, 10, 0, []int64{5}, 1},
		{"mixed literals", "!_!%", 10, 0, []int64{6}, 1},
		{"title and body", "50%", 10, 0, []int64{7, 6, 1}, 3},
		{"underscore substring", "_name", 10, 0, []int64{6, 3}, 2},
		{"ordinary word", "alpha", 10, 0, []int64{8, 7, 6, 5, 4, 3, 2, 1}, 8},
		{"no match", "absent%", 10, 0, nil, 0},
		{"first page", "50%", 2, 0, []int64{7, 6}, 3},
		{"second page", "50%", 2, 2, []int64{1}, 3},
		{"beyond last page", "50%", 2, 4, nil, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, total, err := service.SearchAllPages(context.Background(), SearchParams{
				Query: tt.query, Limit: tt.limit, Offset: tt.offset,
			})
			if err != nil {
				t.Fatalf("SearchAllPages: %v", err)
			}
			var ids []int64
			for _, result := range results {
				ids = append(ids, result.ID)
			}
			if total != tt.total || !slices.Equal(ids, tt.ids) {
				t.Errorf("query %q: ids=%v total=%d, want ids=%v total=%d", tt.query, ids, total, tt.ids, tt.total)
			}
		})
	}
}

// Helper function
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// countRows returns COUNT(*) of a table.
func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// TestSearchIndexingIsBatched guards against a search indexing a large
// backlog in one write transaction: after an upgrade every page is queued,
// and indexing them all at once held the write lock long enough for other
// searches and logins to fail. One batch takes at most indexBatchSize pages,
// a search at most searchSyncBatches batches, and a background drain the rest.
func TestSearchIndexingIsBatched(t *testing.T) {
	db := searchTestDB(t)
	svc := NewSearchService(db)
	ctx := context.Background()
	const pages = indexBatchSize*searchSyncBatches + 50
	for i := range pages {
		if _, err := db.Exec(`INSERT INTO pages (title, slug, body, status) VALUES (?, ?, '<p>batched opossum</p>', 'published')`,
			"Page", "page-"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}

	if n, err := svc.indexQueued(ctx); err != nil || n != indexBatchSize {
		t.Fatalf("indexQueued() = %d, %v; want one batch of %d", n, err, indexBatchSize)
	}
	if left := countRows(t, db, "page_search_queue"); left != pages-indexBatchSize {
		t.Fatalf("queue holds %d after one batch, want %d", left, pages-indexBatchSize)
	}

	// Both searches run syncIndex first; this schema carries the columns of
	// the all-status search.
	if _, _, err := svc.SearchAllPages(ctx, SearchParams{Query: "opossum", Limit: 10}); err != nil {
		t.Fatalf("SearchAllPages: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for countRows(t, db, "page_search_queue") > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the background drain did not empty the queue")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if indexed := countRows(t, db, "page_search"); indexed != pages {
		t.Errorf("page_search holds %d pages, want %d", indexed, pages)
	}
}

// TestRebuildIndexKeepsAnsweringAndDropsDeletedPages checks RebuildIndex goes
// through the queue: the index is never emptied, and an entry whose page no
// longer exists is removed.
func TestRebuildIndexKeepsAnsweringAndDropsDeletedPages(t *testing.T) {
	db := searchTestDB(t)
	svc := NewSearchService(db)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO pages (title, slug, body, status) VALUES ('Kept', 'kept', '<p>rebuild <b>word</b></p>', 'published')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO page_search(rowid, title, body) VALUES (999, 'Ghost', 'ghost text')`); err != nil {
		t.Fatal(err)
	}
	if err := svc.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	var body string
	if err := db.QueryRow(`SELECT body FROM page_search WHERE rowid = 1`).Scan(&body); err != nil || body != "rebuild word" {
		t.Errorf("indexed body = %q (err %v), want %q", body, err, "rebuild word")
	}
	if ghosts := countRows(t, db, "page_search WHERE rowid = 999"); ghosts != 0 {
		t.Error("the entry of a page that no longer exists survived RebuildIndex")
	}
	if left := countRows(t, db, "page_search_queue"); left != 0 {
		t.Errorf("RebuildIndex left %d pages queued", left)
	}
}
