// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

const (
	// EvidenceRetention starts at absolute family expiry or first revocation.
	// It never applies to individual tokens in a still-active family.
	EvidenceRetention = 30 * 24 * time.Hour
	// CodeRetention retains expired code and unused client metadata for a day.
	CodeRetention = 24 * time.Hour
	// CleanupBatchSize bounds each of the five deletion classes per run.
	CleanupBatchSize = 250
)

// CleanupResult contains counts only, never credential or account data.
type CleanupResult struct {
	Codes, Tokens, Previews, Grants, Clients int64
}

// Cleanup performs one repeatable, atomic, time-bounded maintenance batch.
// It works with OAuth disabled and never deletes tokens of an active family.
func Cleanup(ctx context.Context, db *sql.DB) (CleanupResult, error) {
	return cleanupAt(ctx, db, time.Now())
}

func cleanupAt(ctx context.Context, db *sql.DB, now time.Time) (CleanupResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("begin OAuth retention: %w", err)
	}
	defer rollback(tx)
	q := store.New(tx)
	shortCutoff := now.Add(-CodeRetention).Unix()
	familyCutoff := now.Add(-EvidenceRetention).Unix()
	var result CleanupResult
	if result.Codes, err = q.DeleteRetainedOAuthCodes(ctx, store.DeleteRetainedOAuthCodesParams{Cutoff: shortCutoff, BatchLimit: CleanupBatchSize}); err != nil {
		return CleanupResult{}, fmt.Errorf("clean OAuth codes: %w", err)
	}
	if result.Tokens, err = q.DeleteRetiredOAuthTokens(ctx, store.DeleteRetiredOAuthTokensParams{Cutoff: familyCutoff, BatchLimit: CleanupBatchSize}); err != nil {
		return CleanupResult{}, fmt.Errorf("clean OAuth tokens: %w", err)
	}
	if result.Previews, err = q.DeleteRetiredOAuthPreviews(ctx, store.DeleteRetiredOAuthPreviewsParams{Cutoff: familyCutoff, BatchLimit: CleanupBatchSize}); err != nil {
		return CleanupResult{}, fmt.Errorf("clean OAuth previews: %w", err)
	}
	if result.Grants, err = q.DeleteRetiredOAuthGrants(ctx, store.DeleteRetiredOAuthGrantsParams{Cutoff: familyCutoff, BatchLimit: CleanupBatchSize}); err != nil {
		return CleanupResult{}, fmt.Errorf("clean OAuth grants: %w", err)
	}
	if result.Clients, err = q.DeleteRetainedOAuthClients(ctx, store.DeleteRetainedOAuthClientsParams{Cutoff: shortCutoff, BatchLimit: CleanupBatchSize}); err != nil {
		return CleanupResult{}, fmt.Errorf("clean OAuth clients: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CleanupResult{}, fmt.Errorf("commit OAuth retention: %w", err)
	}
	return result, nil
}
