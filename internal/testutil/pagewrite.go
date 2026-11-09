// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"
	"database/sql"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

// AssertPageRevision checks persisted content, complete history and provenance
// after a writer has returned, using the caller's non-transactional queries.
func AssertPageRevision(t *testing.T, q *store.Queries, id, want int64) {
	t.Helper()
	ctx := context.Background()
	page, err := q.GetPageByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if page.Revision != want {
		t.Fatalf("page %d revision=%d, want %d", id, page.Revision, want)
	}
	version, err := q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: id, Revision: sql.NullInt64{Int64: want, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := q.GetPageSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if version.Legacy || !version.SnapshotJson.Valid || version.SnapshotJson.String != current {
		t.Fatalf("page %d complete snapshot does not match persisted state", id)
	}
	if _, err = q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: id, Revision: want}); err != nil {
		t.Fatalf("page %d revision %d missing provenance: %v", id, want, err)
	}
	versions, err := q.CountPageVersions(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if versions != want {
		t.Fatalf("page %d has %d versions for revision %d", id, versions, want)
	}
}
