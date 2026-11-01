// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/store"
)

func emptyValidation() Validation {
	return Validation{Issues: []draft.Issue{}, References: []Reference{}, Links: []draft.Link{}}
}

func (s *Service) summary(p store.Page) DraftSummary {
	admin, preview := s.urls(s.SiteURL, p.ID, p.LanguageCode, p.Revision)
	return DraftSummary{PageID: p.ID, Revision: p.Revision, Title: p.Title, LanguageCode: p.LanguageCode, AdminURL: admin, PreviewURL: preview, UpdatedAt: p.UpdatedAt}
}

func (s *Service) Get(ctx context.Context, a v2.Actor, in PageInput) (DraftDetail, error) {
	if err := s.authorize(ctx, s.Q, a); err != nil {
		return DraftDetail{}, err
	}
	p, err := s.own(ctx, s.Q, in.PageID)
	if err != nil {
		return DraftDetail{}, err
	}
	c, categories, err := s.content(ctx, p)
	if err != nil {
		return DraftDetail{}, err
	}
	options, err := s.options(ctx)
	if err != nil {
		return DraftDetail{}, err
	}
	derived, err := draft.DeriveMarkdown(ctx, p.Body, options)
	if err != nil {
		return DraftDetail{}, err
	}
	result := DraftDetail{DraftSummary: s.summary(p), Slug: p.Slug, Summary: p.Summary, PageType: p.PageType, BodyHTML: p.Body, BodyMarkdown: derived.Markdown, MetaTitle: p.MetaTitle, MetaDescription: p.MetaDescription, MetaKeywords: c.MetaKeywords, Tags: c.Tags, CategoryIDs: categories, FeaturedImageID: c.FeaturedImageID, Validated: emptyValidation(), Verified: Verified{Source: "not_recorded"}, ConversionWarnings: append([]draft.Issue{}, derived.Issues...)}
	fact, err := s.Q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: p.ID, Revision: p.Revision})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DraftDetail{}, err
	}
	if err == nil {
		result.Verified = Verified{UserID: fact.UserID.Int64, Source: fact.Source, APIKeyID: fact.ApiKeyID.Int64, OAuthGrantID: fact.OauthGrantID, OAuthClientID: fact.OauthClientID, Transport: fact.Transport, SavedAt: fact.CreatedAt}
		if err = json.Unmarshal([]byte(fact.ValidatedJson), &result.Validated); err != nil {
			return DraftDetail{}, err
		}
		if result.Reported, err = decodeReported(fact.ReportedJson); err != nil {
			return DraftDetail{}, err
		}
	}
	basis, err := s.latestBasis(ctx, p.ID, p.Revision)
	if err != nil {
		return DraftDetail{}, err
	}
	result.TranslationStatus, err = s.basisStatus(ctx, s.Q, basis)
	if err != nil {
		return DraftDetail{}, err
	}
	if result.TranslationStatus != "unavailable" {
		result.TranslationBasis = basis
	} else {
		result.Validated.TranslationBasis = nil
	}
	r, err := grounding.NewResolver(s.Q, s.SiteURL, s.Access)
	if err != nil {
		return DraftDetail{}, err
	}
	for i, ref := range result.Validated.References {
		if _, err := r.Resolve(ctx, fmt.Sprint(ref.PageID)); errors.Is(err, grounding.ErrUnavailable) {
			result.Validated.References[i] = Reference{Role: ref.Role}
		} else if err != nil {
			return DraftDetail{}, err
		}
	}
	return result, nil
}

// Reports made through the R1.3 foundation used the bare agent object. New
// transport metadata stays separate, while both formats retain omission rules.
func decodeReported(raw string) (Reported, error) {
	var wrapper struct {
		Agent json.RawMessage `json:"agent"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return Reported{}, err
	}
	var result Reported
	var err error
	if wrapper.Agent != nil {
		err = json.Unmarshal([]byte(raw), &result)
	} else {
		err = json.Unmarshal([]byte(raw), &result.Agent)
	}
	return result, err
}

func (s *Service) List(ctx context.Context, a v2.Actor, in ListInput) (ListResult, error) {
	if err := s.authorize(ctx, s.Q, a); err != nil {
		return ListResult{}, err
	}
	if in.LanguageCode != "" {
		if _, err := contentpath.RouteLanguage(ctx, s.Q, in.LanguageCode); err != nil {
			return ListResult{}, v2.NewValidationError(map[string]string{"language_code": "Choose an active language"}, "Validation failed")
		}
	}
	if in.Page == 0 {
		in.Page = 1
	}
	if in.PerPage == 0 {
		in.PerPage = 20
	}
	if in.Page < 1 || in.Page > 21474836 || in.PerPage < 1 || in.PerPage > 100 {
		return ListResult{}, v2.NewValidationError(map[string]string{"page": "Use bounded pagination"}, "Validation failed")
	}
	rows, err := s.Q.ListOwnAIDrafts(ctx, store.ListOwnAIDraftsParams{UserID: a.ActingUserID(), LanguageCode: in.LanguageCode, ResultLimit: int64(in.PerPage), ResultOffset: int64(in.Page-1) * int64(in.PerPage)})
	if err != nil {
		return ListResult{}, err
	}
	total, err := s.Q.CountOwnAIDrafts(ctx, store.CountOwnAIDraftsParams{UserID: a.ActingUserID(), LanguageCode: in.LanguageCode})
	if err != nil {
		return ListResult{}, err
	}
	out := ListResult{Drafts: []DraftSummary{}, Total: total, Page: in.Page, PerPage: in.PerPage}
	for _, p := range rows {
		out.Drafts = append(out.Drafts, s.summary(p))
	}
	return out, nil
}

func (s *Service) TranslationStatus(ctx context.Context, a v2.Actor, in PageInput) (TranslationResult, error) {
	if err := s.authorize(ctx, s.Q, a); err != nil {
		return TranslationResult{}, err
	}
	r, err := grounding.NewResolver(s.Q, s.SiteURL, s.Access)
	if err != nil {
		return TranslationResult{}, err
	}
	root, err := r.Resolve(ctx, fmt.Sprint(in.PageID))
	if errors.Is(err, grounding.ErrUnavailable) {
		return TranslationResult{}, v2.NewError(v2.ErrNotFound, "Page not found")
	}
	if err != nil {
		return TranslationResult{}, err
	}
	members, err := s.Q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: root.ID})
	if err != nil {
		return TranslationResult{}, err
	}
	byLanguage := map[string]int64{root.LanguageCode: root.ID}
	for _, member := range members {
		byLanguage[member.LanguageCode] = member.EntityID
	}
	languages, err := s.Q.ListActiveLanguages(ctx)
	if err != nil {
		return TranslationResult{}, err
	}
	out := TranslationResult{PageID: root.ID, Translations: []Translation{}}
	for _, language := range languages {
		entry := Translation{LanguageCode: language.Code, Status: "missing"}
		if id := byLanguage[language.Code]; id != 0 {
			p, err := r.Resolve(ctx, fmt.Sprint(id))
			if errors.Is(err, grounding.ErrUnavailable) {
				entry.Status = "unavailable"
				out.Translations = append(out.Translations, entry)
				continue
			}
			if err != nil {
				return out, err
			}
			entry.PageID, entry.Revision, entry.Status = p.ID, p.Revision, p.Status
			if p.Status == "draft" {
				entry.Status = "draft"
				access, err := s.Q.GetPageAIAccess(ctx, p.ID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return out, err
				}
				if access.AiEditable {
					entry.Status = "ai_draft"
				}
			}
			basis, err := s.latestBasis(ctx, p.ID, p.Revision)
			if err != nil {
				return out, err
			}
			entry.Freshness, err = s.basisStatus(ctx, s.Q, basis)
			if err != nil {
				return out, err
			}
			if entry.Freshness == "stale" {
				entry.Status = "stale"
			}
		}
		out.Translations = append(out.Translations, entry)
	}
	return out, nil
}
