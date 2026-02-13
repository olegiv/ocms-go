// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package service provides business logic services.
package service

import (
	"context"
	"database/sql"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// SearchService provides full-text search functionality using SQLite FTS5.
type SearchService struct {
	db      *sql.DB
	queries *store.Queries
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
	return &SearchService{db: db, queries: store.New(db)}
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

// SearchPublishedPages searches published pages using FTS5.
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

	// Count total results
	//goland:noinspection SqlResolve
	countQuery := `
		SELECT COUNT(*) FROM pages p
		INNER JOIN pages_fts ON pages_fts.rowid = p.id
		WHERE pages_fts MATCH ? AND p.status = 'published'` + languageFilter

	var total int64
	err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total)
	if err != nil {
		// If FTS table doesn't exist yet, fall back to 0 results
		if strings.Contains(err.Error(), "no such table") {
			return []SearchResult{}, 0, nil
		}
		return nil, 0, err
	}

	if total == 0 {
		return []SearchResult{}, 0, nil
	}

	// Search with ranking and highlights
	// bm25() provides relevance ranking (lower = more relevant)
	// snippet() provides highlighted excerpts
	//goland:noinspection SqlResolve,SqlSignature
	searchQuery := `
		SELECT
			p.id,
			p.title,
			p.slug,
			p.body,
			p.status,
			p.published_at,
			p.created_at,
			p.updated_at,
			p.featured_image_id,
			bm25(pages_fts) as rank,
			snippet(pages_fts, 1, '<mark>', '</mark>', '...', 30) as highlight
		FROM pages p
		INNER JOIN pages_fts ON pages_fts.rowid = p.id
		WHERE pages_fts MATCH ? AND p.status = 'published'` + languageFilter + `
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

		// Sanitize highlight to remove broken HTML from FTS snippet output
		r.Highlight = sanitizeHighlight(r.Highlight)

		// Generate excerpt from body if highlight is empty
		r.Excerpt = s.generateExcerpt(r.Body, params.Query, 200)
		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// generateExcerpt creates a text excerpt from the body, highlighting the search term.
func (s *SearchService) generateExcerpt(body, query string, maxLen int) string {
	// Strip HTML tags for plain text excerpt
	body = stripHTMLTags(body)

	if body == "" {
		return ""
	}

	// Find the first occurrence of any search term
	lowerBody := strings.ToLower(body)
	words := strings.Fields(strings.ToLower(query))

	var firstMatch = -1
	for _, word := range words {
		if idx := strings.Index(lowerBody, word); idx != -1 {
			if firstMatch == -1 || idx < firstMatch {
				firstMatch = idx
			}
		}
	}

	var excerpt string
	if firstMatch == -1 {
		// No match found, take from beginning
		if len(body) > maxLen {
			excerpt = body[:maxLen] + "..."
		} else {
			excerpt = body
		}
	} else {
		// Center the excerpt around the match
		start := firstMatch - maxLen/3
		if start < 0 {
			start = 0
		}
		end := start + maxLen
		if end > len(body) {
			end = len(body)
		}

		excerpt = body[start:end]
		if start > 0 {
			excerpt = "..." + excerpt
		}
		if end < len(body) {
			excerpt += "..."
		}
	}

	return excerpt
}

// stripHTMLTags removes HTML tags from a string.
func stripHTMLTags(s string) string {
	// Simple regex-based tag stripping
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, "")
	// Unescape HTML entities
	s = html.UnescapeString(s)
	// Normalize whitespace
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// sanitizeHighlight strips all HTML tags from FTS snippet output except <mark> tags.
func sanitizeHighlight(highlight string) string {
	if highlight == "" {
		return ""
	}

	// Protect <mark> tags
	highlight = strings.ReplaceAll(highlight, "<mark>", "\x00MARK_OPEN\x00")
	highlight = strings.ReplaceAll(highlight, "</mark>", "\x00MARK_CLOSE\x00")

	// Strip all HTML tags
	highlight = stripHTMLTags(highlight)

	// Restore <mark> tags
	highlight = strings.ReplaceAll(highlight, "\x00MARK_OPEN\x00", "<mark>")
	highlight = strings.ReplaceAll(highlight, "\x00MARK_CLOSE\x00", "</mark>")

	return strings.TrimSpace(highlight)
}

// RebuildIndex rebuilds the FTS index from scratch.
// This is useful after bulk operations or to ensure consistency.
// SEC-005: FTS5 virtual table operations must remain as direct SQL.
func (s *SearchService) RebuildIndex(ctx context.Context) error {
	// Delete all entries
	//goland:noinspection SqlResolve
	_, err := s.db.ExecContext(ctx, `DELETE FROM pages_fts`)
	if err != nil {
		return err
	}

	// Rebuild from pages table
	//goland:noinspection SqlResolve
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO pages_fts(rowid, title, body, meta_title, meta_description, meta_keywords)
		SELECT id, title, body, meta_title, meta_description, meta_keywords
		FROM pages
		WHERE status = 'published'
	`)
	return err
}

// SearchAllPages searches all pages (for admin search) using LIKE.
// FTS5 only indexes published pages, so admin search uses LIKE to include all statuses.
func (s *SearchService) SearchAllPages(ctx context.Context, params SearchParams) ([]SearchResult, int64, error) {
	if params.Query == "" {
		return []SearchResult{}, 0, nil
	}

	likePattern := "%" + params.Query + "%"

	// Count total results using SQLC
	total, err := s.queries.CountAdminSearchPages(ctx, store.CountAdminSearchPagesParams{
		Title: likePattern,
		Body:  likePattern,
	})
	if err != nil {
		return nil, 0, err
	}

	if total == 0 {
		return []SearchResult{}, 0, nil
	}

	// Search all pages using SQLC
	rows, err := s.queries.SearchAdminPages(ctx, store.SearchAdminPagesParams{
		Title:  likePattern,
		Body:   likePattern,
		Limit:  int64(params.Limit),
		Offset: int64(params.Offset),
	})
	if err != nil {
		return nil, 0, err
	}

	results := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		r := SearchResult{
			ID:              row.ID,
			Title:           row.Title,
			Slug:            row.Slug,
			Body:            row.Body,
			Status:          row.Status,
			PublishedAt:     row.PublishedAt,
			CreatedAt:       row.CreatedAt,
			UpdatedAt:       row.UpdatedAt,
			FeaturedImageID: row.FeaturedImageID,
		}
		r.Excerpt = s.generateExcerpt(r.Body, params.Query, 200)
		results = append(results, r)
	}

	return results, total, nil
}
