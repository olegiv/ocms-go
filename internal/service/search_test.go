// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"database/sql"
	"testing"

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

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple paragraph",
			input: "<p>Hello World</p>",
			want:  "Hello World",
		},
		{
			name:  "nested tags",
			input: "<div><p>Hello <strong>World</strong></p></div>",
			want:  "Hello World",
		},
		{
			name:  "multiple spaces normalized",
			input: "<p>Hello</p>   <p>World</p>",
			want:  "Hello World",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "no tags",
			input: "Hello World",
			want:  "Hello World",
		},
		{
			name:  "html entities decoded",
			input: "Hello &amp; World",
			want:  "Hello & World",
		},
		{
			name:  "script tags removed",
			input: "<script>alert('xss')</script>Safe content",
			want:  "alert('xss')Safe content",
		},
		{
			name:  "self-closing tags",
			input: "Line 1<br/>Line 2<hr>Line 3",
			want:  "Line 1Line 2Line 3",
		},
		{
			name:  "attributes removed with tags",
			input: `<a href="http://example.com" class="link">Click me</a>`,
			want:  "Click me",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTMLTags(tt.input)
			if got != tt.want {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
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
	if service.queries == nil {
		t.Error("queries should be initialized even with nil db")
	}
}

func TestSanitizeHighlight(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "clean highlight preserved",
			input: "This is a <mark>test</mark> highlight",
			want:  "This is a <mark>test</mark> highlight",
		},
		{
			name:  "multiple marks preserved",
			input: "<mark>mysql</mark> server with <mark>mysql</mark> client",
			want:  "<mark>mysql</mark> server with <mark>mysql</mark> client",
		},
		{
			name:  "html tags stripped but mark preserved",
			input: `View the <mark>mysql</mark> guide <a href="test">here</a>`,
			want:  "View the <mark>mysql</mark> guide here",
		},
		{
			name:  "br tags removed",
			input: `Line 1<br>Line 2 <mark>test</mark>`,
			want:  "Line 1Line 2 <mark>test</mark>",
		},
		{
			name:  "div and p tags removed",
			input: `<div><p><mark>test</mark> content</p></div>`,
			want:  "<mark>test</mark> content",
		},
		{
			name:  "nested html with mark",
			input: `<strong>Bold</strong> and <mark>highlighted</mark> text`,
			want:  "Bold and <mark>highlighted</mark> text",
		},
		{
			name:  "whitespace normalized",
			input: "  <mark>test</mark>   result  ",
			want:  "<mark>test</mark> result",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeHighlight(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeHighlight(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// searchTestDB creates an in-memory SQLite database with the pages table for search tests.
func searchTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

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

// Helper function
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
