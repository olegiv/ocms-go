// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

const (
	// AIDraftRevisionsKept is how many of an AI draft's newest revisions are
	// always kept. A revision that something still reads is kept as well.
	AIDraftRevisionsKept = 20
	// RevisionPruneBatchSize bounds the revisions one run removes, so a large
	// backlog drains over several runs and no run holds the write lock long.
	RevisionPruneBatchSize = 100

	revisionPruneTimeout = 5 * time.Second
)

// RevisionPruneResult counts what one run removed.
type RevisionPruneResult struct {
	// Versions is the number of page_versions rows deleted, with the same number
	// of content_provenance rows. Replays counts retained idempotency receipts
	// that became tombstones because their revision is gone.
	Versions, Provenance, Replays int64
}

// PruneAIDraftRevisions removes old revisions of unpublished AI drafts in one
// repeatable, atomic, bounded batch. It keeps the newest AIDraftRevisionsKept
// revisions of each AI draft and, beyond them, every revision still pinned by an
// active share link, still the latest authoring or translation evidence, or
// still referenced by an AI draft's current evidence (see
// ListPrunableAIDraftRevisions). Published, scheduled and non-AI pages, and
// legacy title/body history, are never touched.
//
// A revision's version row, provenance row and claim acknowledgments (cascade)
// go together. Retained replay receipts of a removed revision are compacted to
// tombstones, the same state they reach after their seven days: the replay is
// answered request_expired, never re-executed, and its quota charge stays.
func PruneAIDraftRevisions(ctx context.Context, db *sql.DB) (RevisionPruneResult, error) {
	return pruneAIDraftRevisionsAt(ctx, db, time.Now())
}

func pruneAIDraftRevisionsAt(ctx context.Context, db *sql.DB, now time.Time) (RevisionPruneResult, error) {
	ctx, cancel := context.WithTimeout(ctx, revisionPruneTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return RevisionPruneResult{}, fmt.Errorf("begin revision retention: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)
	candidates, err := q.ListPrunableAIDraftRevisions(ctx, store.ListPrunableAIDraftRevisionsParams{Keep: AIDraftRevisionsKept, BatchLimit: RevisionPruneBatchSize, UnixNow: now.Unix()})
	if err != nil {
		return RevisionPruneResult{}, fmt.Errorf("select prunable revisions: %w", err)
	}
	var result RevisionPruneResult
	var versions, evidence, replays int64
	for _, candidate := range candidates {
		if versions, err = q.DeletePrunedPageVersion(ctx, candidate.VersionID); err != nil {
			return RevisionPruneResult{}, fmt.Errorf("delete page version: %w", err)
		}
		if evidence, err = q.DeletePrunedContentProvenance(ctx, store.DeletePrunedContentProvenanceParams{PageID: candidate.PageID, Revision: candidate.Revision.Int64}); err != nil {
			return RevisionPruneResult{}, fmt.Errorf("delete content provenance: %w", err)
		}
		if replays, err = q.CompactPrunedRevisionReplays(ctx, store.CompactPrunedRevisionReplaysParams{EntityID: candidate.PageID, EntityRevision: candidate.Revision.Int64}); err != nil {
			return RevisionPruneResult{}, fmt.Errorf("compact replay receipts: %w", err)
		}
		result.Versions += versions
		result.Provenance += evidence
		result.Replays += replays
	}
	if err = tx.Commit(); err != nil {
		return RevisionPruneResult{}, fmt.Errorf("commit revision retention: %w", err)
	}
	return result, nil
}
