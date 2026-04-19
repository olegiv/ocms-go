// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

const (
	maxTagsPerRequest = 50
	maxTagNameLength  = 100
)

// resolveTagIDs combines explicit tag IDs with name-based resolution, returning
// a de-duplicated list. New tags are created when the actor has canCreateTags.
func resolveTagIDs(ctx context.Context, q *store.Queries, ids []int64, names []string, langCode string, canCreateTags bool) ([]int64, error) {
	combined := make([]int64, 0, len(ids)+len(names))
	combined = append(combined, ids...)

	if len(names) > 0 {
		if len(names) > maxTagsPerRequest {
			return nil, v2.NewValidationError(
				map[string]string{"tags": fmt.Sprintf("Too many tags: %d exceeds maximum of %d", len(names), maxTagsPerRequest)},
				"Validation failed",
			)
		}
		now := time.Now()
		for _, raw := range names {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			if len(name) > maxTagNameLength {
				return nil, v2.NewValidationError(
					map[string]string{"tags": fmt.Sprintf("Tag name too long: %d chars exceeds maximum of %d", len(name), maxTagNameLength)},
					"Validation failed",
				)
			}
			slug := util.Slugify(name)
			if slug == "" {
				continue
			}
			tag, err := q.GetTagBySlug(ctx, slug)
			if err == nil {
				combined = append(combined, tag.ID)
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, v2.NewError(v2.ErrInternal, "Failed to look up tag")
			}
			if !canCreateTags {
				return nil, v2.NewError(v2.ErrForbidden, "taxonomy:write permission required to create new tags")
			}
			created, err := q.CreateTag(ctx, store.CreateTagParams{
				Name:         name,
				Slug:         slug,
				LanguageCode: langCode,
				CreatedAt:    now,
				UpdatedAt:    now,
			})
			if err != nil {
				return nil, v2.NewError(v2.ErrInternal, "Failed to create tag")
			}
			combined = append(combined, created.ID)
		}
	}

	// Validate all IDs exist AND dedupe.
	seen := make(map[int64]struct{}, len(combined))
	unique := make([]int64, 0, len(combined))
	for _, id := range combined {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	for _, id := range unique {
		if _, err := q.GetTagByID(ctx, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, v2.NewValidationError(
					map[string]string{"tag_ids": fmt.Sprintf("Tag %d not found", id)},
					"Validation failed",
				)
			}
			return nil, v2.NewError(v2.ErrInternal, "Failed to validate tag")
		}
	}
	return unique, nil
}

// linkCategories attaches categories to a page inside an existing transaction.
func linkCategories(ctx context.Context, q *store.Queries, pageID int64, categoryIDs []int64) error {
	for _, catID := range categoryIDs {
		if err := q.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: pageID, CategoryID: catID}); err != nil {
			return v2.NewError(v2.ErrInternal, "Failed to attach category")
		}
	}
	return nil
}

// linkTags attaches tags to a page inside an existing transaction.
func linkTags(ctx context.Context, q *store.Queries, pageID int64, tagIDs []int64) error {
	for _, tagID := range tagIDs {
		if err := q.AddTagToPage(ctx, store.AddTagToPageParams{PageID: pageID, TagID: tagID}); err != nil {
			return v2.NewError(v2.ErrInternal, "Failed to attach tag")
		}
	}
	return nil
}
