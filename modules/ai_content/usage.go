// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package ai_content

import (
	"database/sql"
	"fmt"
	"time"
)

// UsageRecord represents a single AI usage log entry.
type UsageRecord struct {
	ID               int64
	PageID           sql.NullInt64
	Provider         string
	Model            string
	Operation        string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CostUSD          float64
	LanguageCode     string
	PageTitle        string
	CreatedBy        int64
	CreatedAt        time.Time
}

// UsageStats contains aggregated usage statistics.
type UsageStats struct {
	TotalRequests      int64
	TotalTokens        int64
	TotalCostUSD       float64
	ByProvider         map[string]*ProviderUsageStats
	RecentRecords      []*UsageRecord
	TotalPages         int64
	TotalImageRequests int64
	TotalTextRequests  int64
}

// ProviderUsageStats contains per-provider aggregate stats.
type ProviderUsageStats struct {
	Provider     string
	Requests     int64
	TotalTokens  int64
	TotalCostUSD float64
}

// logUsage records an AI usage event.
func logUsage(db *sql.DB, record *UsageRecord) error {
	_, err := db.Exec(`
		INSERT INTO ai_content_usage (page_id, provider, model, operation, prompt_tokens, completion_tokens, total_tokens, cost_usd, language_code, page_title, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.PageID, record.Provider, record.Model, record.Operation,
		record.PromptTokens, record.CompletionTokens, record.TotalTokens,
		record.CostUSD, record.LanguageCode, record.PageTitle,
		record.CreatedBy, record.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("logging usage: %w", err)
	}
	return nil
}

// loadUsageStats loads aggregated usage statistics.
func loadUsageStats(db *sql.DB) (*UsageStats, error) {
	stats := &UsageStats{
		ByProvider: make(map[string]*ProviderUsageStats),
	}

	// Total aggregates
	err := db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(cost_usd), 0)
		FROM ai_content_usage
	`).Scan(&stats.TotalRequests, &stats.TotalTokens, &stats.TotalCostUSD)
	if err != nil {
		return nil, fmt.Errorf("loading total stats: %w", err)
	}

	// Count unique pages
	err = db.QueryRow(`SELECT COUNT(DISTINCT page_id) FROM ai_content_usage WHERE page_id IS NOT NULL`).Scan(&stats.TotalPages)
	if err != nil {
		return nil, fmt.Errorf("loading page count: %w", err)
	}

	// Count by operation type
	err = db.QueryRow(`SELECT COALESCE(COUNT(*), 0) FROM ai_content_usage WHERE operation = 'image'`).Scan(&stats.TotalImageRequests)
	if err != nil {
		return nil, fmt.Errorf("loading image count: %w", err)
	}
	err = db.QueryRow(`SELECT COALESCE(COUNT(*), 0) FROM ai_content_usage WHERE operation = 'text'`).Scan(&stats.TotalTextRequests)
	if err != nil {
		return nil, fmt.Errorf("loading text count: %w", err)
	}

	// Per-provider aggregates
	rows, err := db.Query(`
		SELECT provider, COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(cost_usd), 0)
		FROM ai_content_usage
		GROUP BY provider
		ORDER BY provider
	`)
	if err != nil {
		return nil, fmt.Errorf("loading provider stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		ps := &ProviderUsageStats{}
		if err := rows.Scan(&ps.Provider, &ps.Requests, &ps.TotalTokens, &ps.TotalCostUSD); err != nil {
			return nil, fmt.Errorf("scanning provider stats: %w", err)
		}
		stats.ByProvider[ps.Provider] = ps
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating provider stats: %w", err)
	}

	return stats, nil
}

// loadUsageRecords loads recent usage records with pagination.
func loadUsageRecords(db *sql.DB, limit, offset int) ([]*UsageRecord, int, error) {
	// Count total records
	var total int
	err := db.QueryRow(`SELECT COUNT(*) FROM ai_content_usage`).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("counting usage records: %w", err)
	}

	rows, err := db.Query(`
		SELECT id, page_id, provider, model, operation, prompt_tokens, completion_tokens, total_tokens, cost_usd, language_code, page_title, created_by, created_at
		FROM ai_content_usage
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("loading usage records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []*UsageRecord
	for rows.Next() {
		r := &UsageRecord{}
		if err := rows.Scan(&r.ID, &r.PageID, &r.Provider, &r.Model, &r.Operation,
			&r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&r.CostUSD, &r.LanguageCode, &r.PageTitle,
			&r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scanning usage record: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating usage records: %w", err)
	}

	return records, total, nil
}
