// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package service provides business logic services.
package service

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/olegiv/ocms-go/internal/security"
)

// Markers snippet() puts around each match in a search highlight. snippet()
// reads only the body column, which holds security.PageHTMLText output with
// no control characters, so the markers cannot collide with page text.
const (
	highlightOpen  = "\x01"
	highlightClose = "\x02"
)

const (
	// indexBatchSize is how many queued pages one write transaction indexes:
	// enough to keep up with editing, small enough that the write lock and
	// the page bodies in memory stay small on any site.
	indexBatchSize = 100
	// searchSyncBatches caps the queue work a search does itself. A larger
	// backlog (after an upgrade or a bulk import) is indexed by a background
	// drain, and searches meanwhile answer from the index as it stands.
	searchSyncBatches = 2
	// indexDrainTimeout bounds one background drain.
	indexDrainTimeout = 30 * time.Minute
)

// drainingIndexes holds the databases whose page_search queue a background
// drain is indexing, so each database has at most one.
var drainingIndexes sync.Map // *sql.DB -> struct{}

// SearchService provides full-text search functionality using SQLite FTS5.
type SearchService struct {
	db *sql.DB
}

// SearchResult represents a single search result with match highlight.
type SearchResult struct {
	ID              int64
	Title           string
	Slug            string
	Body            string
	Excerpt         string
	Highlight       string
	Status          string
	PageType        string
	PublishedAt     sql.NullTime
	CreatedAt       time.Time
	UpdatedAt       time.Time
	FeaturedImageID sql.NullInt64
	Rank            float64
}

// SearchParams holds search parameters.
type SearchParams struct {
	Query        string
	Limit        int
	Offset       int
	LanguageCode string // Optional: filter by language code ("" = all languages)
}

// NewSearchService creates a new search service.
func NewSearchService(db *sql.DB) *SearchService {
	return &SearchService{db: db}
}

// escapeQuery escapes special FTS5 characters in the query.
func (s *SearchService) escapeQuery(query string) string {
	// FTS5 special characters that need escaping: " ^ * : OR AND NOT ( ) NEAR
	// For simplicity, we'll remove most special chars and use simple term matching
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	// Remove potentially problematic characters
	// Use Unicode-aware character classes: \p{L} for letters, \p{N} for numbers
	// This preserves non-ASCII characters (Cyrillic, Chinese, Arabic, etc.)
	re := regexp.MustCompile(`[^\p{L}\p{N}\s_-]`)
	query = re.ReplaceAllString(query, " ")

	// Split into words and join with OR for broader matching
	words := strings.Fields(query)
	if len(words) == 0 {
		return ""
	}

	// Wrap each word in quotes for phrase matching and add wildcard for prefix matching
	var terms []string
	for _, word := range words {
		if word != "" {
			// Use * for prefix matching to make search more forgiving
			terms = append(terms, `"`+word+`"*`)
		}
	}

	return strings.Join(terms, " OR ")
}

// SearchPublishedPages searches published pages using FTS5 over their
// readable text (page_search), indexing queued page changes first.
// SEC-005: FTS5 queries must remain as direct SQL because bm25(), snippet(),
// and MATCH are SQLite FTS5-specific functions that SQLC cannot generate
// type-safe code for. The dynamic language filter clause is also built conditionally.
func (s *SearchService) SearchPublishedPages(ctx context.Context, params SearchParams) ([]SearchResult, int64, error) {
	if params.Query == "" {
		return []SearchResult{}, 0, nil
	}

	escapedQuery := s.escapeQuery(params.Query)
	if escapedQuery == "" {
		return []SearchResult{}, 0, nil
	}

	// Build language filter clause
	var languageFilter string
	var countArgs []interface{}
	var searchArgs []interface{}

	countArgs = append(countArgs, escapedQuery)
	searchArgs = append(searchArgs, escapedQuery)

	if params.LanguageCode != "" {
		languageFilter = " AND p.language_code = ?"
		countArgs = append(countArgs, params.LanguageCode)
		searchArgs = append(searchArgs, params.LanguageCode)
	}

	s.syncIndex(ctx)

	// Count total results
	//goland:noinspection SqlResolve
	countQuery := `
		SELECT COUNT(*) FROM pages p
		INNER JOIN page_search ON page_search.rowid = p.id
		WHERE page_search MATCH ? AND p.status = 'published' AND p.exclude_from_lists = 0` + languageFilter

	var total int64
	err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total)
	if err != nil {
		// If the search tables don't exist yet, fall back to 0 results
		if strings.Contains(err.Error(), "no such table") {
			slog.Warn("site search unavailable: search tables missing", "error", err)
			return []SearchResult{}, 0, nil
		}
		return nil, 0, err
	}

	if total == 0 {
		return []SearchResult{}, 0, nil
	}

	// Search with ranking and highlights
	// bm25() provides relevance ranking (lower = more relevant)
	// snippet() provides highlighted excerpts of the indexed page text,
	// marking matches with control characters that highlightHTML turns into
	// <mark> after escaping the text
	//goland:noinspection SqlResolve,SqlSignature
	searchQuery := `
		SELECT
			p.id,
			p.title,
			p.slug,
			p.body,
			p.status,
			p.page_type,
			p.published_at,
			p.created_at,
			p.updated_at,
			p.featured_image_id,
			bm25(page_search) as rank,
			snippet(page_search, 1, char(1), char(2), '...', 30) as highlight
		FROM pages p
		INNER JOIN page_search ON page_search.rowid = p.id
		WHERE page_search MATCH ? AND p.status = 'published' AND p.exclude_from_lists = 0` + languageFilter + `
		ORDER BY rank
		LIMIT ? OFFSET ?
	`

	searchArgs = append(searchArgs, params.Limit, params.Offset)
	rows, err := s.db.QueryContext(ctx, searchQuery, searchArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func(rows *sql.Rows) {
		_ = rows.Close()
	}(rows)

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		err := rows.Scan(
			&r.ID,
			&r.Title,
			&r.Slug,
			&r.Body,
			&r.Status,
			&r.PageType,
			&r.PublishedAt,
			&r.CreatedAt,
			&r.UpdatedAt,
			&r.FeaturedImageID,
			&r.Rank,
			&r.Highlight,
		)
		if err != nil {
			return nil, 0, err
		}

		r.Highlight = highlightHTML(r.Highlight)

		// Generate excerpt from body if highlight is empty
		r.Excerpt = s.generateExcerpt(r.Body, params.Query, 200)
		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// generateExcerpt returns up to maxLen characters of the page's readable text,
// centred on the first occurrence of a query word. It cuts by runes, so a
// multi-byte character is never split.
func (s *SearchService) generateExcerpt(body, query string, maxLen int) string {
	text := []rune(security.PageHTMLText(body))
	if len(text) == 0 {
		return ""
	}

	lower := lowerRunes(string(text))
	firstMatch := -1
	for _, word := range strings.Fields(query) {
		if idx := indexRunes(lower, lowerRunes(word)); idx != -1 && (firstMatch == -1 || idx < firstMatch) {
			firstMatch = idx
		}
	}

	start := 0
	if firstMatch != -1 {
		// Centre the excerpt around the match.
		start = max(firstMatch-maxLen/3, 0)
	}
	end := min(start+maxLen, len(text))

	excerpt := string(text[start:end])
	if start > 0 {
		excerpt = "..." + excerpt
	}
	if end < len(text) {
		excerpt += "..."
	}
	return excerpt
}

// lowerRunes lowercases s rune by rune, so indexes line up with []rune(s).
func lowerRunes(s string) []rune {
	runes := []rune(s)
	for i, r := range runes {
		runes[i] = unicode.ToLower(r)
	}
	return runes
}

// indexRunes returns the index of the first occurrence of sub in s, or -1.
func indexRunes(s, sub []rune) int {
	if len(sub) == 0 {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// highlightHTML turns a snippet() excerpt of indexed page text into HTML: the
// text is escaped and only the match markers become <mark> elements. Themes
// render Highlight unescaped, so it must never carry any other markup.
func highlightHTML(snippet string) string {
	if snippet == "" {
		return ""
	}
	escaped := html.EscapeString(strings.TrimSpace(snippet))
	if strings.Count(escaped, highlightOpen) != strings.Count(escaped, highlightClose) {
		return strings.NewReplacer(highlightOpen, "", highlightClose, "").Replace(escaped)
	}
	return strings.NewReplacer(highlightOpen, "<mark>", highlightClose, "</mark>").Replace(escaped)
}

// indexedPage is a page's searchable fields as stored.
type indexedPage struct {
	id                                       int64
	title, body                              string
	metaTitle, metaDescription, metaKeywords sql.NullString
}

// indexPage replaces a page's page_search entry with its readable text.
func indexPage(ctx context.Context, tx *sql.Tx, p indexedPage) error {
	//goland:noinspection SqlResolve
	if _, err := tx.ExecContext(ctx, `DELETE FROM page_search WHERE rowid = ?`, p.id); err != nil {
		return fmt.Errorf("unindexing page %d: %w", p.id, err)
	}
	//goland:noinspection SqlResolve
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO page_search(rowid, title, body, meta_title, meta_description, meta_keywords)
		VALUES (?, ?, ?, ?, ?, ?)
	`, p.id, p.title, security.PageHTMLText(p.body), p.metaTitle, p.metaDescription, p.metaKeywords); err != nil {
		return fmt.Errorf("indexing page %d: %w", p.id, err)
	}
	return nil
}

// indexQueued indexes up to indexBatchSize pages from page_search_queue in
// one short write transaction: a changed page gets its readable text indexed
// again, a deleted one loses its entry, and both leave the queue. It reports
// how many pages it took off the queue; with an empty queue it costs one
// indexed lookup and takes no write lock.
// SEC-005: FTS5 virtual table operations must remain as direct SQL.
func (s *SearchService) indexQueued(ctx context.Context) (int, error) {
	var queued bool
	//goland:noinspection SqlResolve
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM page_search_queue)`).Scan(&queued); err != nil {
		return 0, err
	}
	if !queued {
		return 0, nil
	}

	// The store opens transactions immediate (_txlock=immediate): a page
	// saved meanwhile waits, so its queue entry cannot be dropped unindexed.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	type queuedPage struct {
		exists bool
		page   indexedPage
	}
	//goland:noinspection SqlResolve
	rows, err := tx.QueryContext(ctx, `
		SELECT q.page_id, p.id IS NOT NULL, COALESCE(p.title, ''), COALESCE(p.body, ''),
		       p.meta_title, p.meta_description, p.meta_keywords
		FROM page_search_queue q
		LEFT JOIN pages p ON p.id = q.page_id
		ORDER BY q.page_id
		LIMIT ?
	`, indexBatchSize)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var batch []queuedPage
	for rows.Next() {
		var q queuedPage
		if err := rows.Scan(&q.page.id, &q.exists, &q.page.title, &q.page.body,
			&q.page.metaTitle, &q.page.metaDescription, &q.page.metaKeywords); err != nil {
			return 0, err
		}
		batch = append(batch, q)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	_ = rows.Close()

	for _, q := range batch {
		if q.exists {
			if err := indexPage(ctx, tx, q.page); err != nil {
				return 0, err
			}
		} else if _, err := tx.ExecContext(ctx, `DELETE FROM page_search WHERE rowid = ?`, q.page.id); err != nil {
			return 0, fmt.Errorf("unindexing deleted page %d: %w", q.page.id, err)
		}
		//goland:noinspection SqlResolve
		if _, err := tx.ExecContext(ctx, `DELETE FROM page_search_queue WHERE page_id = ?`, q.page.id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(batch), nil
}

// drainIndex indexes the whole page_search queue, batch by batch.
func (s *SearchService) drainIndex(ctx context.Context) error {
	for {
		n, err := s.indexQueued(ctx)
		if err != nil {
			return err
		}
		if n < indexBatchSize {
			return nil
		}
	}
}

// syncIndex brings page_search up to date before a search. It indexes at most
// searchSyncBatches batches itself and hands a larger backlog to a background
// drain, so a search never holds the write lock for long; until the drain
// finishes, results can miss the pages still queued. A failure is logged and
// the search answers from the index as it stands.
func (s *SearchService) syncIndex(ctx context.Context) {
	for range searchSyncBatches {
		n, err := s.indexQueued(ctx)
		if err != nil {
			slog.Warn("search index update failed; searching the index as it stands", "error", err)
			return
		}
		if n < indexBatchSize {
			return
		}
	}
	s.drainIndexInBackground()
}

// drainIndexInBackground starts indexing the rest of the queue on its own
// goroutine, unless a drain of this database is already running.
func (s *SearchService) drainIndexInBackground() {
	if _, running := drainingIndexes.LoadOrStore(s.db, struct{}{}); running {
		return
	}
	go func() {
		defer drainingIndexes.Delete(s.db)
		ctx, cancel := context.WithTimeout(context.Background(), indexDrainTimeout)
		defer cancel()
		if err := s.drainIndex(ctx); err != nil {
			slog.Warn("background search index update stopped; the next search resumes it", "error", err)
		}
	}()
}

// RebuildIndex rebuilds both search indexes: pages_fts, the raw-HTML index
// older oCMS versions search (its own triggers keep it current), so a
// rollback finds it complete; and page_search, by queueing every page and
// indexing the queue in short batches. page_search keeps answering searches
// meanwhile, and entries of pages that no longer exist are removed. Useful
// after bulk operations or to ensure consistency.
// SEC-005: FTS5 virtual table operations must remain as direct SQL.
func (s *SearchService) RebuildIndex(ctx context.Context) error {
	//goland:noinspection SqlResolve
	if _, err := s.db.ExecContext(ctx, `DELETE FROM pages_fts`); err != nil {
		return err
	}
	//goland:noinspection SqlResolve
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
		SELECT id, title, body, meta_title, meta_description, meta_keywords
		FROM pages
		WHERE status = 'published'
	`); err != nil {
		return err
	}

	//goland:noinspection SqlResolve
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO page_search_queue(page_id)
		SELECT id FROM pages
		UNION
		SELECT rowid FROM page_search WHERE rowid NOT IN (SELECT id FROM pages)
	`); err != nil {
		return err
	}
	return s.drainIndex(ctx)
}

// SearchAllPages searches pages of every status (drafts-visible MCP search)
// with substring matching: the title, or the page's readable text in
// page_search, never its HTML. FTS ranking is not used, so results come
// newest first. Queued page changes are indexed first.
// SEC-005: page_search is an FTS5 table, which SQLC cannot query.
func (s *SearchService) SearchAllPages(ctx context.Context, params SearchParams) ([]SearchResult, int64, error) {
	if params.Query == "" {
		return []SearchResult{}, 0, nil
	}
	s.syncIndex(ctx)

	likePattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(params.Query) + "%"
	const match = `
		FROM pages p
		LEFT JOIN page_search ON page_search.rowid = p.id
		WHERE p.title LIKE ? ESCAPE '!' OR page_search.body LIKE ? ESCAPE '!'`

	var total int64
	//goland:noinspection SqlResolve
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+match, likePattern, likePattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []SearchResult{}, 0, nil
	}

	//goland:noinspection SqlResolve
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.title, p.slug, p.body, p.status, p.published_at, p.created_at, p.updated_at, p.featured_image_id`+match+`
		ORDER BY p.updated_at DESC
		LIMIT ? OFFSET ?`, likePattern, likePattern, params.Limit, params.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Title, &r.Slug, &r.Body, &r.Status,
			&r.PublishedAt, &r.CreatedAt, &r.UpdatedAt, &r.FeaturedImageID); err != nil {
			return nil, 0, err
		}
		r.Excerpt = s.generateExcerpt(r.Body, params.Query, 200)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return results, total, nil
}
