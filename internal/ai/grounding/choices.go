// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package grounding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

const choiceLimit = 20
const imageLimit = 8
const linkLimit = 10

// Choices retains deterministic follow-up positions when an offered set is capped.
type Choices[T any] struct {
	Items      []T    `json:"items"`
	Offset     int64  `json:"offset"`
	NextOffset *int64 `json:"next_offset,omitempty"`
	Truncated  bool   `json:"truncated"`
}

type Taxon struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	LanguageCode string `json:"language_code"`
}
type Language struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}
type ImageVariant struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	Width  int64  `json:"width"`
	Height int64  `json:"height"`
}
type Image struct {
	ID                int64          `json:"id"`
	MarkdownReference string         `json:"markdown_reference"`
	URL               string         `json:"url"`
	Alt               string         `json:"alt"`
	Caption           string         `json:"caption"`
	Width             int64          `json:"width"`
	Height            int64          `json:"height"`
	FeaturedEligible  bool           `json:"featured_eligible"`
	Truncated         bool           `json:"truncated"`
	Variants          []ImageVariant `json:"variants"`
}
type LinkCandidate struct {
	ID           int64  `json:"id"`
	Revision     int64  `json:"revision"`
	Title        string `json:"title"`
	LanguageCode string `json:"language_code"`
	Status       string `json:"status"`
	URL          string `json:"url,omitempty"`
}
type Offered struct {
	Categories Choices[Taxon]         `json:"categories"`
	Tags       Choices[Taxon]         `json:"tags"`
	Languages  Choices[Language]      `json:"languages"`
	Images     Choices[Image]         `json:"images"`
	Links      Choices[LinkCandidate] `json:"link_candidates"`
	Truncated  bool                   `json:"truncated"`
}

func pageChoices[T any](items []T, offset int64, limit int) Choices[T] {
	result := Choices[T]{Items: items, Offset: offset}
	if len(items) > limit {
		result.Items = items[:limit]
		next := offset + int64(limit)
		result.NextOffset = &next
		result.Truncated = true
	}
	return result
}

func (r *Resolver) choices(ctx context.Context, in Input, language string) (Offered, error) {
	out := Offered{}
	tags, err := r.Queries.ListGroundingTags(ctx, store.ListGroundingTagsParams{LanguageCode: language, ResultLimit: choiceLimit + 1, ResultOffset: in.TaxonomyOffset})
	if err != nil {
		return out, err
	}
	tagItems := make([]Taxon, 0, len(tags))
	for _, tag := range tags {
		tagItems = append(tagItems, Taxon{ID: tag.ID, Name: clip(tag.Name, 100), Slug: tag.Slug, LanguageCode: tag.LanguageCode})
	}
	out.Tags = pageChoices(tagItems, in.TaxonomyOffset, choiceLimit)
	for _, row := range tags {
		if clip(row.Name, 100) != row.Name {
			out.Tags.Truncated = true
		}
	}
	categories, err := r.Queries.ListGroundingCategories(ctx, store.ListGroundingCategoriesParams{LanguageCode: language, ResultLimit: choiceLimit + 1, ResultOffset: in.TaxonomyOffset})
	if err != nil {
		return out, err
	}
	categoryItems := make([]Taxon, 0, len(categories))
	for _, category := range categories {
		categoryItems = append(categoryItems, Taxon{ID: category.ID, Name: clip(category.Name, 100), Slug: category.Slug, LanguageCode: category.LanguageCode})
	}
	out.Categories = pageChoices(categoryItems, in.TaxonomyOffset, choiceLimit)
	for _, row := range categories {
		if clip(row.Name, 100) != row.Name {
			out.Categories.Truncated = true
		}
	}
	languages, err := r.Queries.ListGroundingLanguages(ctx, store.ListGroundingLanguagesParams{ResultLimit: choiceLimit + 1, ResultOffset: in.LanguageOffset})
	if err != nil {
		return out, err
	}
	languageItems := make([]Language, 0, len(languages))
	for _, lang := range languages[:min(len(languages), choiceLimit)] {
		if util.IsRoutableLanguageCode(lang.Code) {
			languageItems = append(languageItems, Language{Code: lang.Code, Name: clip(lang.Name, 100), IsDefault: lang.IsDefault})
		}
	}
	out.Languages = sparseChoices(languageItems, in.LanguageOffset, choiceLimit, len(languages))
	for _, row := range languages {
		if clip(row.Name, 100) != row.Name {
			out.Languages.Truncated = true
		}
	}
	pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(in.MediaSearch) + "%"
	media, err := r.Queries.ListGroundingImages(ctx, store.ListGroundingImagesParams{Search: in.MediaSearch, SearchPattern: pattern, ResultLimit: imageLimit + 1, ResultOffset: in.MediaOffset})
	if err != nil {
		return out, err
	}
	images := make([]Image, 0, len(media))
	library := draft.StoreResolver{Queries: r.Queries}
	lang, err := r.Queries.GetLanguageByCode(ctx, language)
	if err != nil {
		return out, err
	}
	for _, row := range media[:min(len(media), imageLimit)] {
		image, err := library.Media(ctx, row.ID)
		if errors.Is(err, draft.ErrUnavailable) {
			continue
		}
		if err != nil {
			return out, err
		}
		choice := Image{ID: row.ID, MarkdownReference: fmt.Sprintf("media:%d", row.ID), URL: model.MediaURL(model.VariantOriginal, row.Uuid, row.Filename), Alt: image.Alt, Caption: image.Caption, Width: image.Width, Height: image.Height, FeaturedEligible: !image.DimensionsKnown || image.Width >= model.MinFeaturedImageWidth && image.Height >= model.MinFeaturedImageHeight, Variants: []ImageVariant{}}
		translation, err := r.Queries.GetMediaTranslation(ctx, store.GetMediaTranslationParams{MediaID: row.ID, LanguageID: lang.ID})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if err == nil {
			if translation.Alt != "" {
				choice.Alt = translation.Alt
			}
			if translation.Caption != "" {
				choice.Caption = translation.Caption
			}
		}
		for _, variant := range image.Variants {
			if _, supported := model.ImageVariants[variant.Type]; supported && variant.Width > 0 && variant.Height > 0 {
				choice.Variants = append(choice.Variants, ImageVariant{Type: variant.Type, URL: model.MediaURL(variant.Type, image.UUID, image.Filename), Width: variant.Width, Height: variant.Height})
			}
		}
		rawAlt, rawCaption := choice.Alt, choice.Caption
		choice.Alt, choice.Caption = clip(rawAlt, 200), clip(rawCaption, 300)
		choice.Truncated = choice.Alt != rawAlt || choice.Caption != rawCaption
		slices.SortFunc(choice.Variants, func(a, b ImageVariant) int { return strings.Compare(a.Type, b.Type) })
		images = append(images, choice)
	}
	out.Images = sparseChoices(images, in.MediaOffset, imageLimit, len(media))
	for _, item := range images {
		out.Images.Truncated = out.Images.Truncated || item.Truncated
	}
	pages, err := r.Queries.ListGroundingPages(ctx, store.ListGroundingPagesParams{LanguageCode: language, AllDrafts: r.access.AllDrafts, OwnDrafts: r.access.OwnDrafts, UserID: r.access.UserID, ResultLimit: linkLimit + 1, ResultOffset: in.LinkOffset})
	if err != nil {
		return out, err
	}
	links := make([]LinkCandidate, 0, len(pages))
	for _, page := range pages[:min(len(pages), linkLimit)] {
		_, ok, err := contentpath.PageLanguagePrefix(ctx, r.Queries, page)
		if err != nil {
			return out, err
		}
		if !ok || util.IsReservedPageSlug(page.Slug) || util.ReservedRoutePath(page.Slug) != "" {
			continue
		}
		publicURL, err := r.URL(ctx, page)
		if err != nil {
			return out, err
		}
		links = append(links, LinkCandidate{ID: page.ID, Revision: page.Revision, Title: clip(page.Title, 255), LanguageCode: page.LanguageCode, Status: page.Status, URL: publicURL})
	}
	out.Links = sparseChoices(links, in.LinkOffset, linkLimit, len(pages))
	for _, page := range pages {
		if clip(page.Title, 255) != page.Title {
			out.Links.Truncated = true
		}
	}
	out.Truncated = out.Tags.Truncated || out.Categories.Truncated || out.Languages.Truncated || out.Images.Truncated || out.Links.Truncated
	return out, nil
}

// sparseChoices advances by storage rows even when unsafe historical rows are omitted.
func sparseChoices[T any](items []T, offset int64, limit, rawCount int) Choices[T] {
	result := pageChoices(items, offset, limit)
	if rawCount > limit {
		next := offset + int64(limit)
		result.NextOffset = &next
		result.Truncated = true
	}
	return result
}
