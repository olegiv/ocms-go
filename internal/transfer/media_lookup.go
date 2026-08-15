// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"context"
	"fmt"
	"strings"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/store"
)

const mediaLookupBatchSize int64 = 1000

// listAllMediaForLookup returns every media row in a stable order. The ID
// tie-breaker in ListMediaSorted keeps offset pagination deterministic when
// many uploads share the same creation timestamp.
func listAllMediaForLookup(ctx context.Context, queries *store.Queries) ([]store.Medium, error) {
	media := make([]store.Medium, 0, mediaLookupBatchSize)
	for offset := int64(0); ; {
		batch, err := queries.ListMediaSorted(ctx, store.ListMediaSortedParams{
			Limit:     mediaLookupBatchSize,
			Offset:    offset,
			SortField: "created_at",
			SortDir:   "desc",
		})
		if err != nil {
			return nil, err
		}
		media = append(media, batch...)
		if int64(len(batch)) < mediaLookupBatchSize {
			return media, nil
		}
		offset += int64(len(batch))
	}
}

type destinationMediaIdentityIndex struct {
	byLogicalUUID map[string][]store.Medium
}

func loadDestinationMediaIdentityIndex(
	ctx context.Context,
	queries *store.Queries,
) (destinationMediaIdentityIndex, error) {
	media, err := listAllMediaForLookup(ctx, queries)
	if err != nil {
		return destinationMediaIdentityIndex{}, err
	}
	index := destinationMediaIdentityIndex{byLogicalUUID: make(map[string][]store.Medium, len(media))}
	for _, medium := range media {
		if !imaging.IsCanonicalMediaUUID(medium.Uuid) {
			// Invalid legacy rows remain administratively removable. They cannot
			// own a canonical archive identity or filesystem target.
			continue
		}
		logicalUUID := strings.ToLower(medium.Uuid)
		index.byLogicalUUID[logicalUUID] = append(index.byLogicalUUID[logicalUUID], medium)
	}
	return index, nil
}

// exact resolves one archive/reference spelling. A unique destination row with
// another case is still a collision: silently remapping it would make file-only
// restores and page URLs depend on a legacy platform-specific spelling.
func (index destinationMediaIdentityIndex) exact(mediaUUID string) (store.Medium, bool, error) {
	logicalUUID := strings.ToLower(mediaUUID)
	matches := index.byLogicalUUID[logicalUUID]
	if len(matches) > 1 {
		spellings := make([]string, 0, len(matches))
		for _, medium := range matches {
			spellings = append(spellings, medium.Uuid)
		}
		return store.Medium{}, false, fmt.Errorf(
			"destination contains duplicate logical media UUID %q as %v", logicalUUID, spellings,
		)
	}
	if len(matches) == 0 {
		return store.Medium{}, false, nil
	}
	if matches[0].Uuid != mediaUUID {
		return store.Medium{}, false, fmt.Errorf(
			"media UUID %q conflicts by case with destination UUID %q", mediaUUID, matches[0].Uuid,
		)
	}
	return matches[0], true, nil
}

func (index destinationMediaIdentityIndex) exactLookupMap() map[string]int64 {
	lookup := make(map[string]int64, len(index.byLogicalUUID))
	for _, matches := range index.byLogicalUUID {
		if len(matches) == 1 {
			lookup[matches[0].Uuid] = matches[0].ID
		}
	}
	return lookup
}

// validateExportMediaIdentities rejects archives that could represent one
// logical UUID with more than one database/filesystem spelling. Canonical UUID
// syntax is case-insensitive, while SQLite uniqueness and Linux paths are not.
func validateExportMediaIdentities(media []store.Medium) error {
	seen := make(map[string]store.Medium, len(media))
	for _, medium := range media {
		if !imaging.IsCanonicalMediaUUID(medium.Uuid) {
			return fmt.Errorf("media %d has invalid canonical UUID %q", medium.ID, medium.Uuid)
		}
		logicalUUID := strings.ToLower(medium.Uuid)
		if existing, ok := seen[logicalUUID]; ok {
			return fmt.Errorf(
				"media %d UUID %q duplicates logical UUID owned by media %d as %q",
				medium.ID, medium.Uuid, existing.ID, existing.Uuid,
			)
		}
		seen[logicalUUID] = medium
	}
	return nil
}
