// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/seo/markdown"
	"github.com/olegiv/ocms-go/internal/store"
)

// ErrHardValidation reports AI content that fails the draft contract. Only
// content problems use it: the editor can fix them in the draft.
var ErrHardValidation = errors.New("draft failed publication validation")

// ErrSiteURLRequired reports that the checks cannot run because the site URL
// is unset or invalid. Publication is refused, but it is a configuration
// problem the editor cannot fix in the draft, and it does not end a schedule.
var ErrSiteURLRequired = errors.New("publication checks need a configured site URL")

// ValidatePublication uses the shared draft contract for opted-in AI content.
// It only checks: publication never replaces the authoritative stored HTML.
// Legacy human pages retain the existing editor/REST validation policy.
func ValidatePublication(ctx context.Context, q *store.Queries, page store.Page) error {
	access, err := q.GetPageAIAccess(ctx, page.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	original, historyErr := q.GetLatestAuthoringProvenance(ctx, store.GetLatestAuthoringProvenanceParams{PageID: page.ID, Revision: page.Revision})
	if historyErr != nil && !errors.Is(historyErr, sql.ErrNoRows) {
		return historyErr
	}
	if !access.AiEditable && errors.Is(historyErr, sql.ErrNoRows) {
		return nil
	}
	if len(security.DetectSuspiciousHTMLTokens(page.Body)) > 0 {
		return ErrHardValidation
	}
	fact, err := q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: page.Revision})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var validation Validation
	if err == nil {
		if err = json.Unmarshal([]byte(fact.ValidatedJson), &validation); err != nil {
			return err
		}
		if draft.HasErrors(validation.Issues) {
			return ErrHardValidation
		}
	}
	// R1.5 recorded the configured origin in validation. Human revisions may
	// have empty evidence, so the live configured origin is the fallback.
	origin := validation.SiteURL
	if origin == "" && historyErr == nil {
		var saved Validation
		if err := json.Unmarshal([]byte(original.ValidatedJson), &saved); err != nil {
			return err
		}
		origin = saved.SiteURL
	}
	if origin == "" {
		cfg, cfgErr := q.GetConfigByKey(ctx, "site_url")
		if cfgErr != nil && !errors.Is(cfgErr, sql.ErrNoRows) {
			return fmt.Errorf("load site URL: %w", cfgErr)
		}
		origin = cfg.Value
	}
	resolver, err := grounding.NewResolver(q, origin, grounding.Access{UserID: page.AuthorID, AllDrafts: true})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSiteURLRequired, err)
	}
	options := draft.Options{Resolver: resolver.DraftResolver(), DefaultLanguage: page.LanguageCode, UILanguage: page.LanguageCode, SiteURL: origin, LinkPolicy: draft.Keep}
	derived, err := draft.DeriveMarkdown(ctx, page.Body, options)
	if errors.Is(err, markdown.ErrBodyTooLarge) {
		// Content the editor must shorten, not a fault to retry.
		return fmt.Errorf("%w: %w", ErrHardValidation, err)
	}
	if err != nil {
		return err
	}
	content := draft.Content{ID: page.ID, Title: page.Title, Slug: page.Slug, Summary: page.Summary, LanguageCode: page.LanguageCode, PageType: page.PageType, BodyHTML: page.Body, MetaTitle: page.MetaTitle, MetaDescription: page.MetaDescription}
	changes := draft.Changes{Title: draft.Set(page.Title), Summary: draft.Set(page.Summary), BodyMarkdown: draft.Set(derived.Markdown), MetaTitle: draft.SetNullable(page.MetaTitle), MetaDescription: draft.SetNullable(page.MetaDescription)}
	changes.Slug = draft.Set(page.Slug)
	keywords := []string{}
	if page.MetaKeywords != "" {
		for _, word := range strings.Split(page.MetaKeywords, ",") {
			keywords = append(keywords, strings.TrimSpace(word))
		}
	}
	changes.MetaKeywords = draft.Set(keywords)
	tags, err := q.GetTagsForPage(ctx, page.ID)
	if err != nil {
		return err
	}
	slugs := make([]string, 0, len(tags))
	for _, tag := range tags {
		slugs = append(slugs, tag.Slug)
	}
	changes.Tags = draft.Set(slugs)
	categories, err := q.GetCategoriesForPage(ctx, page.ID)
	if err != nil {
		return err
	}
	for _, category := range categories {
		ok, err := options.Resolver.Category(ctx, category.ID, page.LanguageCode)
		if err != nil {
			return err
		}
		if !ok {
			return ErrHardValidation
		}
	}
	if page.FeaturedImageID.Valid {
		// Treat this as an existing assignment: publication does not choose an
		// image or silently replace it, but the referenced media must still exist.
		content.FeaturedImageID = &page.FeaturedImageID.Int64
		changes.FeaturedImageID = draft.SetNullable(page.FeaturedImageID.Int64)
	}
	checked, err := draft.NormalizeUpdate(ctx, changes, content, options)
	if err != nil {
		return err
	}
	if checked.HasErrors() {
		return ErrHardValidation
	}
	return nil
}
