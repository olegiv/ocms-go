// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	"github.com/olegiv/ocms-go/internal/ai/prompts"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/store"
)

func (s *Service) own(ctx context.Context, q *store.Queries, id int64) (store.Page, error) {
	p, err := q.GetPageByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Page{}, v2.NewError(v2.ErrNotFound, "Draft not found")
	}
	if err != nil {
		return store.Page{}, err
	}
	access, err := q.GetPageAIAccess(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.Page{}, err
	}
	if p.AuthorID != s.Access.UserID || p.Status != "draft" || p.ScheduledAt.Valid || !access.AiEditable {
		return store.Page{}, v2.NewError(v2.ErrNotFound, "Draft not found")
	}
	return p, nil
}

func (s *Service) content(ctx context.Context, p store.Page) (draft.Content, []int64, error) {
	c := draft.Content{ID: p.ID, Title: p.Title, Slug: p.Slug, LanguageCode: p.LanguageCode, PageType: p.PageType, Summary: p.Summary, BodyHTML: p.Body, MetaTitle: p.MetaTitle, MetaDescription: p.MetaDescription, MetaKeywords: []string{}, Tags: []string{}}
	if p.MetaKeywords != "" {
		for _, word := range strings.Split(p.MetaKeywords, ",") {
			c.MetaKeywords = append(c.MetaKeywords, strings.TrimSpace(word))
		}
	}
	if p.FeaturedImageID.Valid {
		id := p.FeaturedImageID.Int64
		c.FeaturedImageID = &id
	}
	cats, err := s.Q.GetCategoriesForPage(ctx, p.ID)
	if err != nil {
		return c, nil, err
	}
	ids := []int64{}
	for _, cat := range cats {
		ids = append(ids, cat.ID)
	}
	if len(ids) > 0 {
		c.CategoryID = &ids[0]
	}
	tags, err := s.Q.GetTagsForPage(ctx, p.ID)
	if err != nil {
		return c, nil, err
	}
	for _, tag := range tags {
		c.Tags = append(c.Tags, tag.Slug)
	}
	return c, ids, nil
}

func (s *Service) tagIDs(ctx context.Context, slugs []string, language string) ([]int64, error) {
	ids := []int64{}
	for _, slug := range slugs {
		tag, err := s.Q.GetTagBySlug(ctx, slug)
		if errors.Is(err, sql.ErrNoRows) || err == nil && tag.LanguageCode != language {
			return nil, invalid([]draft.Issue{draft.NewIssue("unknown_tag", "tags", language)})
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, tag.ID)
	}
	return ids, nil
}

func (s *Service) createBody(ctx context.Context, c draft.Content) (pages.CreatePageBody, error) {
	tags, err := s.tagIDs(ctx, c.Tags, c.LanguageCode)
	if err != nil {
		return pages.CreatePageBody{}, err
	}
	body := pages.CreatePageBody{Title: c.Title, Slug: c.Slug, Body: c.BodyHTML, Summary: c.Summary, PageType: c.PageType, LanguageCode: &c.LanguageCode, MetaTitle: c.MetaTitle, MetaDescription: c.MetaDescription, MetaKeywords: strings.Join(c.MetaKeywords, ", "), FeaturedImageID: c.FeaturedImageID, TagIDs: tags}
	if c.CategoryID != nil {
		body.CategoryIDs = []int64{*c.CategoryID}
	}
	return body, nil
}

func (s *Service) updateBody(ctx context.Context, in UpdateInput, c draft.Content) (pages.UpdatePageBody, error) {
	body := pages.UpdatePageBody{BaseRevision: &in.BaseRevision}
	changes := in.Changes
	if changes.Title.Present {
		body.Title = &c.Title
	}
	if changes.Slug.Present {
		body.Slug = &c.Slug
	}
	if changes.Summary.Present {
		body.Summary = &c.Summary
	}
	if changes.BodyMarkdown.Present {
		body.Body = &c.BodyHTML
	}
	if changes.MetaTitle.Present {
		body.MetaTitle = &c.MetaTitle
	}
	if changes.MetaDescription.Present {
		body.MetaDescription = &c.MetaDescription
	}
	if changes.MetaKeywords.Present {
		value := strings.Join(c.MetaKeywords, ", ")
		body.MetaKeywords = &value
	}
	if changes.FeaturedImageID.Present {
		body.FeaturedImageID = c.FeaturedImageID
		body.ClearFeaturedImage = c.FeaturedImageID == nil
	}
	if changes.CategoryID.Present {
		ids := []int64{}
		if c.CategoryID != nil {
			ids = append(ids, *c.CategoryID)
		}
		body.CategoryIDs = &ids
	}
	if changes.Tags.Present {
		ids, err := s.tagIDs(ctx, c.Tags, c.LanguageCode)
		if err != nil {
			return body, err
		}
		body.TagIDs = &ids
	}
	return body, nil
}

func (s *Service) validation(ctx context.Context, result draft.Result, body string, basis *Basis) (Validation, error) {
	v := Validation{Issues: append([]draft.Issue{}, result.Issues...), References: []Reference{}, Links: append([]draft.Link{}, result.Links...), TranslationBasis: basis, SiteURL: strings.TrimRight(s.SiteURL, "/"), PromptContractVersion: prompts.Version}
	r, err := grounding.NewResolver(s.Q, s.SiteURL, s.Access)
	if err != nil {
		return v, err
	}
	if !result.BodyChanged {
		if len(result.Content.BodyHTML) > draft.MaxBodyBytes {
			v.LinksTruncated = true
			v.Issues = append(v.Issues, draft.NewIssue("html_conversion_lossy", "body_html", result.Content.LanguageCode))
		} else {
			path := "/" + result.Content.Slug
			defaultLanguage, err := s.Q.GetDefaultLanguage(ctx)
			if err != nil {
				return v, err
			}
			if result.Content.LanguageCode != defaultLanguage.Code {
				path = "/" + result.Content.LanguageCode + path
			}
			links, issues, err := draft.InspectHTMLLinks(ctx, result.Content.BodyHTML, draft.Options{Resolver: r.DraftResolver(), SiteURL: s.SiteURL, PagePath: path, UILanguage: result.Content.LanguageCode})
			if err != nil {
				return v, err
			}
			v.Links = append([]draft.Link{}, links...)
			v.Issues = append(v.Issues, issues...)
		}
	}
	examples := []grounding.Reference{}
	seen := map[int64]store.Page{}
	for _, ref := range result.References {
		p, err := r.Resolve(ctx, ref.Ref)
		if errors.Is(err, grounding.ErrUnavailable) {
			return v, invalid([]draft.Issue{draft.NewIssue("reference_unresolved", "references", "en")})
		}
		if err != nil {
			return v, err
		}
		if previous, ok := seen[p.ID]; ok {
			p = previous
		} else {
			seen[p.ID] = p
		}
		v.References = append(v.References, Reference{PageID: p.ID, Revision: p.Revision, Role: ref.Role})
		if ref.Role == "example" && result.BodyChanged {
			derived, err := draft.DeriveMarkdown(ctx, p.Body, draft.Options{Resolver: r.DraftResolver(), SiteURL: s.SiteURL, UILanguage: p.LanguageCode})
			if err != nil {
				return v, err
			}
			runes := []rune(derived.Markdown)
			if len(runes) > grounding.MaxReferenceRunes {
				runes = runes[:grounding.MaxReferenceRunes]
			}
			examples = append(examples, grounding.Reference{PageID: p.ID, Revision: p.Revision, Role: ref.Role, Markdown: string(runes)})
		}
	}
	_, warnings := grounding.CheckOverlap(body, examples, result.Content.LanguageCode)
	v.Issues = append(v.Issues, warnings...)
	return v, nil
}

func sourceConflict(language string) error {
	return &Error{Code: "source_revision_conflict", Message: "The source content changed; read it again before translating", Issues: []draft.Issue{draft.NewIssue("source_revision_conflict", "source_revision", language)}}
}

func referenceInputs(refs []Reference) []draft.Reference {
	result := make([]draft.Reference, 0, len(refs))
	for _, r := range refs {
		result = append(result, draft.Reference{Ref: fmt.Sprint(r.PageID), Role: r.Role})
	}
	return result
}

func (s *Service) inheritReferences(ctx context.Context, p store.Page, v *Validation, body string, bodyChanged bool) ([]draft.Reference, error) {
	fact, err := s.Q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: p.ID, Revision: p.Revision})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	previous := emptyValidation()
	if err = json.Unmarshal([]byte(fact.ValidatedJson), &previous); err != nil {
		return nil, err
	}
	v.References = previous.References
	requested := referenceInputs(v.References)
	if !bodyChanged {
		return requested, nil
	}
	r, err := grounding.NewResolver(s.Q, s.SiteURL, s.Access)
	if err != nil {
		return nil, err
	}
	examples := []grounding.Reference{}
	for _, ref := range previous.References {
		if ref.Role != "example" {
			continue
		}
		if _, err = r.Resolve(ctx, fmt.Sprint(ref.PageID)); errors.Is(err, grounding.ErrUnavailable) {
			return nil, invalid([]draft.Issue{draft.NewIssue("reference_unresolved", "references", p.LanguageCode)})
		}
		if err != nil {
			return nil, err
		}
		version, err := s.Q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: ref.PageID, Revision: sql.NullInt64{Int64: ref.Revision, Valid: true}})
		if errors.Is(err, sql.ErrNoRows) {
			// Retention keeps referenced revisions; a missing one is reported like
			// an unavailable reference, not as an internal error.
			return nil, invalid([]draft.Issue{draft.NewIssue("reference_unresolved", "references", p.LanguageCode)})
		}
		if err != nil {
			return nil, err
		}
		derived, err := draft.DeriveMarkdown(ctx, version.Body, draft.Options{Resolver: r.DraftResolver(), SiteURL: s.SiteURL, UILanguage: p.LanguageCode})
		if err != nil {
			return nil, err
		}
		runes := []rune(derived.Markdown)
		runes = runes[:min(len(runes), grounding.MaxReferenceRunes)]
		examples = append(examples, grounding.Reference{PageID: ref.PageID, Revision: ref.Revision, Role: ref.Role, Markdown: string(runes)})
	}
	_, issues := grounding.CheckOverlap(body, examples, p.LanguageCode)
	v.Issues = append(v.Issues, issues...)
	return requested, nil
}
