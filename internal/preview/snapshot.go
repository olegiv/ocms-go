// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// Snapshot contains recorded values only. Relations are IDs from the saved revision.
type Snapshot struct {
	Page                store.Page
	CategoryIDs, TagIDs []int64
}

func Load(ctx context.Context, q *store.Queries, id, revision int64) (Snapshot, error) {
	version, err := q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: id, Revision: sql.NullInt64{Int64: revision, Valid: true}})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, fmt.Errorf("load preview revision: %w", err)
	}
	if err != nil || version.Legacy || !version.SnapshotJson.Valid {
		return Snapshot{}, ErrUnavailable
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(version.SnapshotJson.String), &fields) != nil {
		return Snapshot{}, ErrUnavailable
	}
	for _, key := range []string{"id", "revision", "title", "slug", "body", "summary", "language_code", "author_id", "featured_image_id", "meta_title", "meta_description", "meta_keywords", "category_ids", "tag_ids"} {
		if _, ok := fields[key]; !ok {
			return Snapshot{}, ErrUnavailable
		}
	}
	var value struct {
		ID                int64   `json:"id"`
		Revision          int64   `json:"revision"`
		Title             string  `json:"title"`
		Slug              string  `json:"slug"`
		Body              string  `json:"body"`
		Summary           string  `json:"summary"`
		Status            string  `json:"status"`
		Language          string  `json:"language_code"`
		PageType          string  `json:"page_type"`
		AuthorID          int64   `json:"author_id"`
		FeaturedImageID   *int64  `json:"featured_image_id"`
		MetaTitle         string  `json:"meta_title"`
		MetaDescription   string  `json:"meta_description"`
		MetaKeywords      string  `json:"meta_keywords"`
		HideFeaturedImage int64   `json:"hide_featured_image"`
		CreatedAt         string  `json:"created_at"`
		UpdatedAt         string  `json:"updated_at"`
		CategoryIDs       []int64 `json:"category_ids"`
		TagIDs            []int64 `json:"tag_ids"`
		VideoURL          string  `json:"video_url"`
		VideoTitle        string  `json:"video_title"`
	}
	if json.Unmarshal([]byte(version.SnapshotJson.String), &value) != nil || value.ID != id || value.Revision != revision || value.Language == "" {
		return Snapshot{}, ErrUnavailable
	}
	page := store.Page{ID: id, Revision: revision, Title: value.Title, Slug: value.Slug, Body: value.Body, Summary: value.Summary, Status: value.Status, LanguageCode: value.Language, PageType: value.PageType, AuthorID: value.AuthorID,
		MetaTitle: value.MetaTitle, MetaDescription: value.MetaDescription, MetaKeywords: value.MetaKeywords, HideFeaturedImage: value.HideFeaturedImage, VideoUrl: value.VideoURL, VideoTitle: value.VideoTitle}
	if value.FeaturedImageID != nil {
		page.FeaturedImageID = sql.NullInt64{Int64: *value.FeaturedImageID, Valid: true}
	}
	page.CreatedAt, err = snapshotTime(value.CreatedAt)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	page.UpdatedAt, err = snapshotTime(value.UpdatedAt)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	return Snapshot{Page: page, CategoryIDs: value.CategoryIDs, TagIDs: value.TagIDs}, nil
}

func snapshotTime(raw string) (time.Time, error) {
	var value time.Time
	var err error
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999-07:00 MST", "2006-01-02 15:04:05.999999999 +0000 UTC", "2006-01-02 15:04:05.999999999"} {
		value, err = time.Parse(layout, raw)
		if err == nil {
			return value, nil
		}
	}
	return value, err
}
