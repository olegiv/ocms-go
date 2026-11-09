// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite

import (
	"context"
	"errors"

	"github.com/olegiv/ocms-go/internal/store"
)

// DeleteTerm snapshots every page whose assignments change when a term is
// removed. The explicit unlink also works in embedders without FK cascades.
func (t *Tx) DeleteTerm(ctx context.Context, kind string, id int64) error {
	var ids []int64
	var err error
	switch kind {
	case "tag":
		ids, err = t.Q.ListPageIDsForTag(ctx, id)
	case "category":
		ids, err = t.Q.ListPageIDsForCategory(ctx, id)
	default:
		return errors.New("unknown taxonomy kind")
	}
	if err != nil {
		return err
	}
	for _, pageID := range ids {
		if _, err = t.TrackSystem(ctx, pageID); err != nil {
			return err
		}
		if kind == "tag" {
			err = t.Q.RemoveTagFromPage(ctx, store.RemoveTagFromPageParams{PageID: pageID, TagID: id})
		} else {
			err = t.Q.RemoveCategoryFromPage(ctx, store.RemoveCategoryFromPageParams{PageID: pageID, CategoryID: id})
		}
		if err != nil {
			return err
		}
	}
	if kind == "tag" {
		return t.Q.DeleteTag(ctx, id)
	}
	return t.Q.DeleteCategory(ctx, id)
}
