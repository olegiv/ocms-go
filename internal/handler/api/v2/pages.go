// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/olegiv/ocms-go/internal/handler/api"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// Meta is the pagination envelope reused across list responses.
type Meta struct {
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Pages   int   `json:"pages"`
}

// calcPages returns the total number of pages for `total` items at `perPage`.
func calcPages(total int64, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 0
	}
	return int((total + int64(perPage) - 1) / int64(perPage))
}

// registerPages wires the /pages operations onto the huma API.
func registerPages(h *Handler) {
	registerListPages(h)
	registerGetPage(h)
	registerGetPageBySlug(h)
}

// ListPagesInput carries query parameters for GET /api/v2/pages.
type ListPagesInput struct {
	Page     int    `query:"page" default:"1" minimum:"1" doc:"1-indexed page number."`
	PerPage  int    `query:"per_page" default:"20" minimum:"1" maximum:"100" doc:"Items per page."`
	Status   string `query:"status" enum:"draft,published" doc:"Filter by page status. Requires pages:read for non-published."`
	Category int64  `query:"category" doc:"Restrict results to a category id."`
	Tag      int64  `query:"tag" doc:"Restrict results to a tag id."`
	Include  string `query:"include" doc:"Comma-separated relations to populate: author, categories, tags."`
}

// ListPagesOutput is the paginated response for GET /api/v2/pages.
type ListPagesOutput struct {
	Body struct {
		Data []api.PageResponse `json:"data"`
		Meta Meta               `json:"meta"`
	}
}

func registerListPages(h *Handler) {
	huma.Register(h.API, huma.Operation{
		OperationID: "listPages",
		Method:      http.MethodGet,
		Path:        "/pages",
		Summary:     "List pages",
		Description: "Returns a paginated list of pages. Unauthenticated and non-pages:read keys receive only published pages.",
		Tags:        []string{"Pages"},
		Security:    []map[string][]string{{"ApiKeyAuth": {}}, {}},
	}, func(ctx context.Context, in *ListPagesInput) (*ListPagesOutput, error) {
		apiKey := apiKeyFromContext(ctx)
		canReadNonPublished := api.ApiKeyHasPermission(apiKey, model.PermissionPagesRead)

		status := in.Status
		if !canReadNonPublished && status != "" && status != model.PageStatusPublished {
			return nil, huma.Error403Forbidden("pages:read permission required to view non-published pages")
		}
		if !canReadNonPublished {
			status = model.PageStatusPublished
		}

		limit := int64(in.PerPage)
		offset := int64((in.Page - 1) * in.PerPage)
		publishedOnly := status == model.PageStatusPublished

		var pages []store.Page
		var total int64
		var err error
		q := h.Deps.Queries
		switch {
		case in.Category > 0:
			pages, total, err = listPagesByCategory(ctx, q, publishedOnly, in.Category, limit, offset)
		case in.Tag > 0:
			pages, total, err = listPagesByTag(ctx, q, publishedOnly, in.Tag, limit, offset)
		case status != "":
			pages, err = q.ListPagesByStatus(ctx, store.ListPagesByStatusParams{Status: status, Limit: limit, Offset: offset})
			if err == nil {
				total, err = q.CountPagesByStatus(ctx, status)
			}
		default:
			pages, err = q.ListPages(ctx, store.ListPagesParams{Limit: limit, Offset: offset})
			if err == nil {
				total, err = q.CountPages(ctx)
			}
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list pages", err)
		}

		includeAuthor, includeCategories, includeTags := parseIncludes(in.Include)
		out := &ListPagesOutput{}
		out.Body.Data = make([]api.PageResponse, 0, len(pages))
		for _, p := range pages {
			resp := api.StorePageToResponse(p)
			if includeAuthor {
				populateAuthor(ctx, q, &resp, p.ID, canReadNonPublished)
			}
			if includeCategories {
				populateCategories(ctx, q, &resp, p.ID)
			}
			if includeTags {
				populateTags(ctx, q, &resp, p.ID)
			}
			out.Body.Data = append(out.Body.Data, resp)
		}
		out.Body.Meta = Meta{Total: total, Page: in.Page, PerPage: in.PerPage, Pages: calcPages(total, in.PerPage)}
		return out, nil
	})
}

// GetPageInput carries path params for GET /api/v2/pages/{id}.
type GetPageInput struct {
	ID      int64  `path:"id" minimum:"1"`
	Include string `query:"include" doc:"Comma-separated relations to populate: author, categories, tags."`
}

// PageOutput wraps a single page response.
type PageOutput struct {
	Body struct {
		Data api.PageResponse `json:"data"`
	}
}

func registerGetPage(h *Handler) {
	huma.Register(h.API, huma.Operation{
		OperationID: "getPage",
		Method:      http.MethodGet,
		Path:        "/pages/{id}",
		Summary:     "Get page by ID",
		Tags:        []string{"Pages"},
		Security:    []map[string][]string{{"ApiKeyAuth": {}}, {}},
	}, func(ctx context.Context, in *GetPageInput) (*PageOutput, error) {
		q := h.Deps.Queries
		page, err := q.GetPageByID(ctx, in.ID)
		if err != nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("page %d not found", in.ID))
		}
		apiKey := apiKeyFromContext(ctx)
		canReadNonPublished := api.ApiKeyHasPermission(apiKey, model.PermissionPagesRead)
		if page.Status != model.PageStatusPublished && !canReadNonPublished {
			return nil, huma.Error404NotFound(fmt.Sprintf("page %d not found", in.ID))
		}
		resp := api.StorePageToResponse(page)
		populateIncludes(ctx, q, &resp, page.ID, in.Include, canReadNonPublished)
		out := &PageOutput{}
		out.Body.Data = resp
		return out, nil
	})
}

// GetPageBySlugInput carries the slug path param for GET /api/v2/pages/slug/{slug}.
type GetPageBySlugInput struct {
	Slug    string `path:"slug" minLength:"1"`
	Include string `query:"include"`
}

func registerGetPageBySlug(h *Handler) {
	huma.Register(h.API, huma.Operation{
		OperationID: "getPageBySlug",
		Method:      http.MethodGet,
		Path:        "/pages/slug/{slug}",
		Summary:     "Get page by slug",
		Tags:        []string{"Pages"},
		Security:    []map[string][]string{{"ApiKeyAuth": {}}, {}},
	}, func(ctx context.Context, in *GetPageBySlugInput) (*PageOutput, error) {
		q := h.Deps.Queries
		page, err := q.GetPageBySlug(ctx, in.Slug)
		if err != nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("page %q not found", in.Slug))
		}
		apiKey := apiKeyFromContext(ctx)
		canReadNonPublished := api.ApiKeyHasPermission(apiKey, model.PermissionPagesRead)
		if page.Status != model.PageStatusPublished && !canReadNonPublished {
			return nil, huma.Error404NotFound(fmt.Sprintf("page %q not found", in.Slug))
		}
		resp := api.StorePageToResponse(page)
		populateIncludes(ctx, q, &resp, page.ID, in.Include, canReadNonPublished)
		out := &PageOutput{}
		out.Body.Data = resp
		return out, nil
	})
}

// parseIncludes turns an "author,categories,tags" query string into three flags.
func parseIncludes(include string) (author, categories, tags bool) {
	if include == "" {
		return
	}
	for _, part := range strings.Split(include, ",") {
		switch strings.TrimSpace(part) {
		case "author":
			author = true
		case "categories":
			categories = true
		case "tags":
			tags = true
		}
	}
	return
}

func populateIncludes(ctx context.Context, q *store.Queries, resp *api.PageResponse, pageID int64, include string, authenticated bool) {
	a, c, t := parseIncludes(include)
	if a {
		populateAuthor(ctx, q, resp, pageID, authenticated)
	}
	if c {
		populateCategories(ctx, q, resp, pageID)
	}
	if t {
		populateTags(ctx, q, resp, pageID)
	}
}

func populateAuthor(ctx context.Context, q *store.Queries, resp *api.PageResponse, pageID int64, authenticated bool) {
	author, err := q.GetPageAuthor(ctx, pageID)
	if err != nil {
		return
	}
	a := &api.AuthorResponse{ID: author.ID, Name: author.Name}
	if authenticated {
		a.Email = author.Email
	}
	resp.Author = a
}

func populateCategories(ctx context.Context, q *store.Queries, resp *api.PageResponse, pageID int64) {
	cats, err := q.GetCategoriesForPage(ctx, pageID)
	if err != nil {
		return
	}
	out := make([]api.CategoryResponse, 0, len(cats))
	for _, c := range cats {
		out = append(out, api.StoreCategoryToResponse(c))
	}
	resp.Categories = out
}

func populateTags(ctx context.Context, q *store.Queries, resp *api.PageResponse, pageID int64) {
	tags, err := q.GetTagsForPage(ctx, pageID)
	if err != nil {
		return
	}
	out := make([]api.TagResponse, 0, len(tags))
	for _, t := range tags {
		out = append(out, api.StoreTagToResponse(t))
	}
	resp.Tags = out
}

func listPagesByCategory(ctx context.Context, q *store.Queries, publishedOnly bool, categoryID, limit, offset int64) ([]store.Page, int64, error) {
	var pages []store.Page
	var total int64
	var err error
	if publishedOnly {
		pages, err = q.ListPublishedPagesByCategory(ctx, store.ListPublishedPagesByCategoryParams{CategoryID: categoryID, Limit: limit, Offset: offset})
		if err == nil {
			total, err = q.CountPublishedPagesByCategory(ctx, categoryID)
		}
	} else {
		pages, err = q.ListPagesByCategory(ctx, store.ListPagesByCategoryParams{CategoryID: categoryID, Limit: limit, Offset: offset})
		if err == nil {
			total, err = q.CountPagesByCategory(ctx, categoryID)
		}
	}
	return pages, total, err
}

func listPagesByTag(ctx context.Context, q *store.Queries, publishedOnly bool, tagID, limit, offset int64) ([]store.Page, int64, error) {
	var pages []store.Page
	var total int64
	var err error
	if publishedOnly {
		pages, err = q.ListPublishedPagesForTag(ctx, store.ListPublishedPagesForTagParams{TagID: tagID, Limit: limit, Offset: offset})
		if err == nil {
			total, err = q.CountPublishedPagesForTag(ctx, tagID)
		}
	} else {
		pages, err = q.GetPagesForTag(ctx, store.GetPagesForTagParams{TagID: tagID, Limit: limit, Offset: offset})
		if err == nil {
			total, err = q.CountPagesForTag(ctx, tagID)
		}
	}
	return pages, total, err
}
