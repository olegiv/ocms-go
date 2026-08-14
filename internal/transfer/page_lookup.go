// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"context"

	"github.com/olegiv/ocms-go/internal/store"
)

const pageLookupBatchSize int64 = 1000

// listAllPagesForLookup returns every page in a stable order. The ID
// tie-breaker in ListPagesSorted makes offset pagination deterministic when
// pages share the same creation timestamp.
func listAllPagesForLookup(ctx context.Context, queries *store.Queries) ([]store.Page, error) {
	pages := make([]store.Page, 0, pageLookupBatchSize)
	for offset := int64(0); ; {
		batch, err := queries.ListPagesSorted(ctx, store.ListPagesSortedParams{
			Limit:     pageLookupBatchSize,
			Offset:    offset,
			SortField: "created_at",
			SortDir:   "desc",
		})
		if err != nil {
			return nil, err
		}
		pages = append(pages, batch...)
		if int64(len(batch)) < pageLookupBatchSize {
			return pages, nil
		}
		offset += int64(len(batch))
	}
}
