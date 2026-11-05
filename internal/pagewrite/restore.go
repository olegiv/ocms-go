// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// ParamsFor copies every stored page field for a narrow update or restore.
func ParamsFor(p store.Page) store.UpdatePageParams {
	return store.UpdatePageParams{
		ID: p.ID, Title: p.Title, Slug: p.Slug, Body: p.Body, Summary: p.Summary, Status: p.Status,
		FeaturedImageID: p.FeaturedImageID, MetaTitle: p.MetaTitle, MetaDescription: p.MetaDescription, MetaKeywords: p.MetaKeywords,
		OgImageID: p.OgImageID, NoIndex: p.NoIndex, NoFollow: p.NoFollow, CanonicalUrl: p.CanonicalUrl, ScheduledAt: p.ScheduledAt,
		LanguageCode: p.LanguageCode, HideFeaturedImage: p.HideFeaturedImage, PageType: p.PageType, ExcludeFromLists: p.ExcludeFromLists,
		PublishedAt: p.PublishedAt, UpdatedAt: time.Now().UTC(), VideoUrl: p.VideoUrl, VideoTitle: p.VideoTitle,
	}
}

// Restore restores recorded content and assignments. Identity, AI access,
// publication/scheduling and the current translation component are retained.
// A legacy row can restore only its recorded title and body.
func (t *Tx) Restore(ctx context.Context, page store.Page, version store.PageVersion, body string) error {
	if version.PageID != page.ID {
		return refuse("version does not belong to this page")
	}
	params := ParamsFor(page)
	params.Title = version.Title
	params.Body = body
	if !version.Legacy && version.SnapshotJson.Valid {
		var saved struct {
			Title             string   `json:"title"`
			Slug              string   `json:"slug"`
			Summary           string   `json:"summary"`
			LanguageCode      string   `json:"language_code"`
			PageType          string   `json:"page_type"`
			FeaturedImageID   *int64   `json:"featured_image_id"`
			OGImageID         *int64   `json:"og_image_id"`
			MetaTitle         string   `json:"meta_title"`
			MetaDescription   string   `json:"meta_description"`
			MetaKeywords      string   `json:"meta_keywords"`
			NoIndex           int64    `json:"no_index"`
			NoFollow          int64    `json:"no_follow"`
			CanonicalURL      string   `json:"canonical_url"`
			HideFeaturedImage int64    `json:"hide_featured_image"`
			ExcludeFromLists  int64    `json:"exclude_from_lists"`
			VideoURL          string   `json:"video_url"`
			VideoTitle        string   `json:"video_title"`
			CategoryIDs       []int64  `json:"category_ids"`
			TagIDs            []int64  `json:"tag_ids"`
			Aliases           []string `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(version.SnapshotJson.String), &saved); err != nil {
			return err
		}
		params.Title = saved.Title
		params.Slug = saved.Slug
		params.Summary = saved.Summary
		params.PageType = saved.PageType
		language, err := t.Q.GetLanguageByCode(ctx, saved.LanguageCode)
		if err != nil {
			return restoreReferenceError(err, "language", "pages.restore_missing_language", saved.LanguageCode)
		}
		if !language.IsActive {
			return refuse("recorded language is inactive")
		}
		// Language changes may affect translation-component uniqueness.
		if saved.LanguageCode != page.LanguageCode {
			member, memberErr := t.Q.GetTranslationComponentEntityByLanguage(ctx, store.GetTranslationComponentEntityByLanguageParams{EntityType: "page", EntityID: page.ID, LanguageCode: saved.LanguageCode})
			if memberErr == nil && member != page.ID {
				return ErrTranslationExists
			}
			if memberErr != nil && !errors.Is(memberErr, sql.ErrNoRows) {
				return memberErr
			}
			if err = t.TrackComponent(ctx, page.ID); err != nil {
				return err
			}
		}
		params.LanguageCode = saved.LanguageCode
		params.FeaturedImageID = sql.NullInt64{}
		if saved.FeaturedImageID != nil {
			media, loadErr := t.Q.GetMediaByID(ctx, *saved.FeaturedImageID)
			if loadErr != nil {
				return restoreReferenceError(loadErr, "featured image", "pages.restore_missing_featured_image", *saved.FeaturedImageID)
			}
			if err = CheckFeaturedImageDimensions(media.Width.Int64, media.Height.Int64, media.Width.Valid && media.Height.Valid); err != nil {
				return err
			}
			params.FeaturedImageID = sql.NullInt64{Int64: *saved.FeaturedImageID, Valid: true}
		}
		params.OgImageID = sql.NullInt64{}
		if saved.OGImageID != nil {
			if _, err = t.Q.GetMediaByID(ctx, *saved.OGImageID); err != nil {
				return restoreReferenceError(err, "Open Graph image", "pages.restore_missing_og_image", *saved.OGImageID)
			}
			params.OgImageID = sql.NullInt64{Int64: *saved.OGImageID, Valid: true}
		}
		for _, id := range saved.TagIDs {
			if _, err = t.Q.GetTagByID(ctx, id); err != nil {
				return restoreReferenceError(err, "tag", "pages.restore_missing_tag", id)
			}
		}
		for _, id := range saved.CategoryIDs {
			if _, err = t.Q.GetCategoryByID(ctx, id); err != nil {
				return restoreReferenceError(err, "category", "pages.restore_missing_category", id)
			}
		}
		params.MetaTitle = saved.MetaTitle
		params.MetaDescription = saved.MetaDescription
		params.MetaKeywords = saved.MetaKeywords
		params.NoIndex = saved.NoIndex
		params.NoFollow = saved.NoFollow
		params.CanonicalUrl = saved.CanonicalURL
		params.HideFeaturedImage = saved.HideFeaturedImage
		params.ExcludeFromLists = saved.ExcludeFromLists
		params.VideoUrl = saved.VideoURL
		params.VideoTitle = saved.VideoTitle
		if err = t.Q.ClearPageTags(ctx, page.ID); err != nil {
			return err
		}
		for _, id := range saved.TagIDs {
			if err = t.Q.AddTagToPage(ctx, store.AddTagToPageParams{PageID: page.ID, TagID: id}); err != nil {
				return err
			}
		}
		if err = t.Q.ClearPageCategories(ctx, page.ID); err != nil {
			return err
		}
		for _, id := range saved.CategoryIDs {
			if err = t.Q.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: page.ID, CategoryID: id}); err != nil {
				return err
			}
		}
		if err = t.Q.ClearPageAliases(ctx, page.ID); err != nil {
			return err
		}
		for _, alias := range saved.Aliases {
			if conflict, err := RouteConflict(ctx, t.Q, alias); err != nil {
				return err
			} else if conflict != "" {
				return refuse(conflict)
			}
			exists, err := t.Q.SlugOrAliasExistsExcluding(ctx, store.SlugOrAliasExistsExcludingParams{Slug: alias, ID: page.ID, Alias: alias, PageID: page.ID})
			if err != nil {
				return err
			}
			if exists != 0 {
				return refuse("recorded alias is occupied")
			}
			if _, err = t.Q.CreatePageAlias(ctx, store.CreatePageAliasParams{PageID: page.ID, Alias: alias, CreatedAt: time.Now().UTC()}); err != nil {
				return err
			}
		}
	}
	if params.Slug != page.Slug {
		if err := SlugAvailable(ctx, t.Q, params.Slug, page.ID); err != nil {
			return err
		}
	}
	_, err := t.Q.UpdatePage(ctx, params)
	return err
}

func restoreReferenceError(err error, kind, messageKey string, id any) error {
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load recorded %s %v: %w", kind, id, err)
	}
	return &Refusal{
		Reason:      fmt.Sprintf("recorded %s %v no longer exists", kind, id),
		MessageKey:  messageKey,
		MessageArgs: []any{fmt.Sprint(id)},
	}
}
