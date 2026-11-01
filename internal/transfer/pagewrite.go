// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/store"
)

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
		var base *int64
		if revision, ok := opts.BaseRevisions[item.Slug]; ok {
			base = &revision
		}
		if opts.ConflictStrategy == ConflictOverwrite {
			if _, err = work.Track(ctx, page.ID, base, false); err != nil {
				return fmt.Errorf("import page %q: %w", item.Slug, err)
			}
		}
		if err = work.TrackComponent(ctx, page.ID); err != nil {
			return err
		}
		if opts.ConflictStrategy == ConflictOverwrite {
			members, err := work.Q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: page.ID})
			if err != nil {
				return err
			}
			for _, member := range members {
				access, err := work.Q.GetPageAIAccess(ctx, member.EntityID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if !access.AiEditable {
					continue
				}
				sibling, err := work.Q.GetPageByID(ctx, member.EntityID)
				if err != nil {
					return err
				}
				value, ok := opts.BaseRevisions[sibling.Slug]
				if !ok {
					return fmt.Errorf("import translation %q: %w", sibling.Slug, pagewrite.ErrRevisionRequired)
				}
				if _, err = work.Track(ctx, sibling.ID, &value, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
