// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite

import (
	"context"

	"github.com/olegiv/ocms-go/internal/store"
)

// Delete removes a page, severs translation edges, retains replay tombstones,
// and tracks the changed membership of surviving siblings in the same save.
func (t *Tx) Delete(ctx context.Context, id int64) error {
	if err := t.TrackComponent(ctx, id); err != nil {
		return err
	}
	if err := t.Q.CompactPageIdempotencyKeys(ctx, id); err != nil {
		return err
	}
	if err := t.Q.DeleteTranslationsRelatedToEntity(ctx, store.DeleteTranslationsRelatedToEntityParams{EntityType: "page", EntityID: id, TranslationID: id}); err != nil {
		return err
	}
	if err := t.Q.ClearPageCategories(ctx, id); err != nil {
		return err
	}
	if err := t.Q.ClearPageTags(ctx, id); err != nil {
		return err
	}
	if err := t.Q.ClearPageAliases(ctx, id); err != nil {
		return err
	}
	if err := t.Q.DeletePageVersions(ctx, id); err != nil {
		return err
	}
	return t.Q.DeletePage(ctx, id)
}
