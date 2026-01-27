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
)

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
	PublishedAt     sql.NullTime
	CreatedAt       time.Time
	UpdatedAt       time.Time
	FeaturedImageID sql.NullInt64
	Rank            float64
}

// SearchParams holds search parameters.
type SearchParams struct {
	Query      string
	Limit      int
	Offset     int
	LanguageID int64 // Optional: filter by language ID (0 = all languages)
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
		if len(word) > 0 {
			// Use * for prefix matching to make search more forgiving
			terms = append(terms, `"`+word+`"*`)
		}
	}

	return strings.Join(terms, " OR ")
}

// SearchPublishedPages searches published pages using FTS5.
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

	if params.LanguageID > 0 {
		// Include pages with matching language_id OR NULL language_id (universal pages)
		languageFilter = " AND (p.language_id = ? OR p.language_id IS NULL)"
		countArgs = append(countArgs, params.LanguageID)
		searchArgs = append(searchArgs, params.LanguageID)
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

	if len(body) == 0 {
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
			excerpt = excerpt + "..."
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

// RebuildIndex rebuilds the FTS index from scratch.
// This is useful after bulk operations or to ensure consistency.
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

// SearchAllPages searches all pages (for admin search) using FTS5.
func (s *SearchService) SearchAllPages(ctx context.Context, params SearchParams) ([]SearchResult, int64, error) {
	if params.Query == "" {
		return []SearchResult{}, 0, nil
	}

	escapedQuery := s.escapeQuery(params.Query)
	if escapedQuery == "" {
		return []SearchResult{}, 0, nil
	}

	// For admin search, we search all pages regardless of status
	// Since FTS only indexes published pages, we need a different approach
	// Use LIKE for admin search to include all pages
	likePattern := "%" + params.Query + "%"

	// Count total results
	countQuery := `
		SELECT COUNT(*) FROM pages
		WHERE title LIKE ? OR body LIKE ?
	`

	var total int64
	err := s.db.QueryRowContext(ctx, countQuery, likePattern, likePattern).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	if total == 0 {
		return []SearchResult{}, 0, nil
	}

	// Search all pages
	searchQuery := `
		SELECT
			id, title, slug, body, status,
			published_at, created_at, updated_at, featured_image_id
		FROM pages
		WHERE title LIKE ? OR body LIKE ?
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.QueryContext(ctx, searchQuery, likePattern, likePattern, params.Limit, params.Offset)
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
		)
		if err != nil {
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
