// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/olegiv/ocms-go/internal/pagewrite"
)

// trackImportedPages captures every existing page the import may change before
// its first mutation. An overwrite that names a destination revision in
// opts.BaseRevisions is checked against it and refused with a
// *pagewrite.Conflict when the page moved on. Without one the import replaces
// whatever the page holds inside this transaction, AI-editable pages included,
// and is recorded as the import's own revision: the import has no editor form
// that displayed a revision, which is why nothing could fill it in (2026-11-03
// review R2 P3-6).
func trackImportedPages(ctx context.Context, work *pagewrite.Tx, data *ExportData, opts ImportOptions) error {
	if !opts.ImportPages {
		return nil
	}
	for _, item := range data.Pages {
		page, err := work.Q.GetPageBySlug(ctx, item.Slug)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if opts.ConflictStrategy == ConflictOverwrite {
			if err = trackOverwrittenPage(ctx, work, page.ID, opts.BaseRevisions, item.Slug); err != nil {
				return fmt.Errorf("import page %q: %w", item.Slug, err)
			}
		}
		if err = work.TrackComponent(ctx, page.ID); err != nil {
			return err
		}
	}
	return nil
}

func trackOverwrittenPage(ctx context.Context, work *pagewrite.Tx, id int64, bases map[string]int64, slug string) error {
	if base, ok := bases[slug]; ok {
		_, err := work.Track(ctx, id, &base, false)
		return err
	}
	_, err := work.TrackSystem(ctx, id)
	return err
}
