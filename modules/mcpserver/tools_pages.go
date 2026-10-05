// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/seo/markdown"
	"github.com/olegiv/ocms-go/internal/service"
)

// Body formats get_page can return.
const (
	bodyFormatHTML     = "html"
	bodyFormatMarkdown = "markdown"
)

// Defaults applied when a handler is called without schema defaults (the SDK
// fills them from the input schema for real calls).
const (
	defaultSearchPerPage = 10
	defaultPagesPerPage  = 20
)

// errDraftsHidden explains why a status=draft listing is refused.
var errDraftsHidden = &toolError{
	Code:    codeForbidden,
	Message: "Drafts are not available to this API key: the site does not expose drafts over MCP, or the key lacks pages:read",
}

// SearchPagesInput is the input of search_pages.
type SearchPagesInput struct {
	Query   string `json:"query" minLength:"1" maxLength:"200" doc:"Words to look for in page titles and bodies."`
	Page    int    `json:"page,omitempty" default:"1" minimum:"1" maximum:"21474836" doc:"1-indexed page number (max 21474836)."`
	PerPage int    `json:"per_page,omitempty" default:"10" minimum:"1" maximum:"50" doc:"Results per page (max 50)."`
}

// SearchPagesResult is the output of search_pages.
type SearchPagesResult struct {
	Results []SearchHit `json:"results"`
	Pagination
}

// SearchHit is one search result.
type SearchHit struct {
	ID              int64      `json:"id"`
	Title           string     `json:"title"`
	Slug            string     `json:"slug"`
	Status          string     `json:"status" doc:"draft or published."`
	Excerpt         string     `json:"excerpt" doc:"Plain-text excerpt around the first match."`
	FeaturedImageID *int64     `json:"featured_image_id,omitempty" doc:"Media id of the featured image (see get_media)."`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ListPagesInput is the input of list_pages. Filters and paging mirror the
// query parameters of GET /api/v2/pages (drift-tested).
type ListPagesInput struct {
	Status          string `json:"status,omitempty" enum:"draft,published" doc:"Only pages with this status. Drafts require draft visibility (see get_site_info)."`
	CategoryID      int64  `json:"category_id,omitempty" minimum:"1" doc:"Only pages in this category id."`
	TagID           int64  `json:"tag_id,omitempty" minimum:"1" doc:"Only pages with this tag id."`
	Page            int    `json:"page,omitempty" default:"1" minimum:"1" maximum:"21474836" doc:"1-indexed page number (max 21474836)."`
	PerPage         int    `json:"per_page,omitempty" default:"20" minimum:"1" maximum:"100" doc:"Items per page (max 100)."`
	IncludeTaxonomy bool   `json:"include_taxonomy,omitempty" doc:"Also return each page's categories and tags."`
}

// ListPagesResult is the output of list_pages.
type ListPagesResult struct {
	Pages []PageSummary `json:"pages"`
	Pagination
}

// PageSummary is a page without its body.
type PageSummary struct {
	ID              int64            `json:"id"`
	Title           string           `json:"title"`
	Slug            string           `json:"slug"`
	Status          string           `json:"status" doc:"draft or published."`
	PageType        string           `json:"page_type" doc:"post or page."`
	LanguageCode    string           `json:"language_code"`
	Summary         string           `json:"summary,omitempty"`
	URL             string           `json:"url,omitempty" doc:"Public URL; set for published pages once the site URL is configured."`
	FeaturedImageID *int64           `json:"featured_image_id,omitempty" doc:"Media id of the featured image (see get_media)."`
	PublishedAt     *time.Time       `json:"published_at,omitempty"`
	ScheduledAt     *time.Time       `json:"scheduled_at,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	Categories      []pages.Category `json:"categories,omitempty"`
	Tags            []pages.Tag      `json:"tags,omitempty"`
}

// GetPageInput is the input of get_page. id and slug mirror the path
// parameters of GET /api/v2/pages/{id} and /pages/slug/{slug}.
type GetPageInput struct {
	ID         int64  `json:"id,omitempty" minimum:"1" doc:"Page id. Give either id or slug."`
	Slug       string `json:"slug,omitempty" minLength:"1" doc:"Page slug. Give either id or slug."`
	BodyFormat string `json:"body_format,omitempty" enum:"html,markdown" default:"html" doc:"html returns the stored body; markdown returns a compact conversion that drops embeds and styling."`
}

// PageDetail is a page with its body and metadata.
type PageDetail struct {
	PageSummary
	Body              string     `json:"body" doc:"Page body in body_format."`
	BodyFormat        string     `json:"body_format" doc:"html or markdown."`
	Author            *AuthorRef `json:"author,omitempty"`
	MetaTitle         string     `json:"meta_title,omitempty"`
	MetaDescription   string     `json:"meta_description,omitempty"`
	MetaKeywords      string     `json:"meta_keywords,omitempty"`
	CanonicalURL      string     `json:"canonical_url,omitempty"`
	OGImageID         *int64     `json:"og_image_id,omitempty" doc:"Media id of the social sharing image."`
	NoIndex           bool       `json:"no_index"`
	NoFollow          bool       `json:"no_follow"`
	HideFeaturedImage bool       `json:"hide_featured_image"`
	ExcludeFromLists  bool       `json:"exclude_from_lists"`
	VideoURL          string     `json:"video_url,omitempty"`
	VideoTitle        string     `json:"video_title,omitempty"`
}

// AuthorRef names a page's author. The email address is never exposed to
// agents.
type AuthorRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// searchPages implements search_pages: full-text search of published pages
// (the public site search) or, when drafts are visible, a match over every
// page as in the admin search.
func (m *Module) searchPages(ctx context.Context, call *toolCall, in SearchPagesInput) (SearchPagesResult, error) {
	page, perPage := normalizePaging(in.Page, in.PerPage, defaultSearchPerPage)
	params := service.SearchParams{
		Query:  in.Query,
		Limit:  perPage,
		Offset: (page - 1) * perPage,
	}
	var (
		results []service.SearchResult
		total   int64
		err     error
	)
	if draftsVisible(call.actor) {
		results, total, err = m.svc.search.SearchAllPages(ctx, params)
	} else {
		results, total, err = m.svc.search.SearchPublishedPages(ctx, params)
	}
	if err != nil {
		return SearchPagesResult{}, fmt.Errorf("searching pages: %w", err)
	}
	out := SearchPagesResult{
		Results:    make([]SearchHit, 0, len(results)),
		Pagination: newPagination(total, page, perPage),
	}
	for _, r := range results {
		hit := SearchHit{
			ID:     r.ID,
			Title:  r.Title,
			Slug:   r.Slug,
			Status: r.Status,
			// The excerpt is cut by bytes and may split a multi-byte rune.
			Excerpt:   strings.ToValidUTF8(r.Excerpt, ""),
			UpdatedAt: r.UpdatedAt,
		}
		if r.FeaturedImageID.Valid {
			id := r.FeaturedImageID.Int64
			hit.FeaturedImageID = &id
		}
		if r.PublishedAt.Valid {
			published := r.PublishedAt.Time
			hit.PublishedAt = &published
		}
		out.Results = append(out.Results, hit)
	}
	return out, nil
}

// listPages implements list_pages over pages.Service.List.
func (m *Module) listPages(ctx context.Context, call *toolCall, in ListPagesInput) (ListPagesResult, error) {
	if in.Status == model.PageStatusDraft && !draftsVisible(call.actor) {
		return ListPagesResult{}, errDraftsHidden
	}
	if in.CategoryID > 0 && in.TagID > 0 {
		return ListPagesResult{}, newValidationError("filters", "Use only one of category_id or tag_id")
	}
	if in.Status == model.PageStatusDraft && (in.CategoryID > 0 || in.TagID > 0) {
		return ListPagesResult{}, newValidationError("filters", "Draft status cannot be combined with category_id or tag_id")
	}
	page, perPage := normalizePaging(in.Page, in.PerPage, defaultPagesPerPage)
	result, err := m.svc.pages.List(ctx, call.actor, pages.ListFilter{
		Page:              page,
		PerPage:           perPage,
		Status:            in.Status,
		CategoryID:        in.CategoryID,
		TagID:             in.TagID,
		IncludeCategories: in.IncludeTaxonomy,
		IncludeTags:       in.IncludeTaxonomy,
	})
	if err != nil {
		return ListPagesResult{}, err
	}
	urls, err := m.newPageURLs(ctx)
	if err != nil {
		return ListPagesResult{}, err
	}
	out := ListPagesResult{
		Pages:      make([]PageSummary, 0, len(result.Pages)),
		Pagination: newPagination(result.Total, result.Page, result.PerPage),
	}
	for _, p := range result.Pages {
		out.Pages = append(out.Pages, summarizePage(p, urls))
	}
	return out, nil
}

// getPage implements get_page over pages.Service.Get / GetBySlug.
func (m *Module) getPage(ctx context.Context, call *toolCall, in GetPageInput) (PageDetail, error) {
	includes := pages.ListFilter{IncludeAuthor: true, IncludeCategories: true, IncludeTags: true}
	var (
		page *pages.Page
		err  error
	)
	switch {
	case in.ID > 0 && in.Slug != "":
		return PageDetail{}, newValidationError("id", "Give either id or slug, not both")
	case in.ID > 0:
		page, err = m.svc.pages.Get(ctx, call.actor, in.ID, includes)
	case in.Slug != "":
		page, err = m.svc.pages.GetBySlug(ctx, call.actor, in.Slug, includes)
	default:
		return PageDetail{}, newValidationError("id", "Give either id or slug")
	}
	if err != nil {
		return PageDetail{}, err
	}

	body, format := page.Body, bodyFormatHTML
	if in.BodyFormat == bodyFormatMarkdown {
		converted, convErr := markdown.HTMLToMarkdown(page.Body)
		switch {
		case errors.Is(convErr, markdown.ErrBodyTooLarge):
			// A property of the content, not a fault: the agent can ask for
			// the HTML instead.
			return PageDetail{}, newValidationError("body_format",
				`This page body is too large to convert to Markdown; request body_format "html"`)
		case convErr != nil:
			return PageDetail{}, fmt.Errorf("converting page %d body to Markdown: %w", page.ID, convErr)
		}
		body, format = converted, bodyFormatMarkdown
	}
	urls, err := m.newPageURLs(ctx)
	if err != nil {
		return PageDetail{}, err
	}

	detail := PageDetail{
		PageSummary:       summarizePage(*page, urls),
		Body:              body,
		BodyFormat:        format,
		MetaTitle:         page.MetaTitle,
		MetaDescription:   page.MetaDescription,
		MetaKeywords:      page.MetaKeywords,
		CanonicalURL:      page.CanonicalURL,
		OGImageID:         page.OGImageID,
		NoIndex:           page.NoIndex,
		NoFollow:          page.NoFollow,
		HideFeaturedImage: page.HideFeaturedImage,
		ExcludeFromLists:  page.ExcludeFromLists,
		VideoURL:          page.VideoURL,
		VideoTitle:        page.VideoTitle,
	}
	if page.Author != nil {
		detail.Author = &AuthorRef{ID: page.Author.ID, Name: page.Author.Name}
	}
	return detail, nil
}

// summarizePage maps a v2 page DTO to the MCP summary.
func summarizePage(p pages.Page, urls pageURLs) PageSummary {
	return PageSummary{
		ID:              p.ID,
		Title:           p.Title,
		Slug:            p.Slug,
		Status:          p.Status,
		PageType:        p.PageType,
		LanguageCode:    p.LanguageCode,
		Summary:         p.Summary,
		URL:             urls.forPage(p.Status, p.Slug, p.LanguageCode),
		FeaturedImageID: p.FeaturedImageID,
		PublishedAt:     p.PublishedAt,
		ScheduledAt:     p.ScheduledAt,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
		Categories:      p.Categories,
		Tags:            p.Tags,
	}
}

// normalizePaging guards handlers called without schema defaults: the SDK
// applies the defaults and bounds from the input schema for real calls.
func normalizePaging(page, perPage, defaultPerPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > maxPerPage {
		perPage = defaultPerPage
	}
	return page, perPage
}
