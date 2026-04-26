// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"context"
	"math"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/olegiv/ocms-go/internal/api/v2"
)

// Register wires Pages operations onto the v2 huma API. The handler layer is
// thin: parse huma input, delegate to Service, format output.
func Register(api huma.API, svc *Service) {
	registerList(api, svc)
	registerGet(api, svc)
	registerGetBySlug(api, svc)
	registerCreate(api, svc)
	registerUpdate(api, svc)
	registerDelete(api, svc)
}

// ListPagesInput carries pagination, filter, and include flags.
type ListPagesInput struct {
	Page     int    `query:"page" default:"1" minimum:"1" doc:"1-indexed page number."`
	PerPage  int    `query:"per_page" default:"20" minimum:"1" maximum:"100" doc:"Items per page (max 100)."`
	Status   string `query:"status" enum:"draft,published" doc:"Filter by page status. Requires pages:read for non-published."`
	Category int64  `query:"category" doc:"Restrict results to this category id."`
	Tag      int64  `query:"tag" doc:"Restrict results to this tag id."`
	Include  string `query:"include" doc:"Comma-separated relations to populate: author, categories, tags."`
}

// PagesListMeta carries pagination metadata in list responses.
type PagesListMeta struct {
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Pages   int   `json:"pages"`
}

// ListPagesOutput is the paginated response envelope.
type ListPagesOutput struct {
	Body struct {
		Data []Page        `json:"data"`
		Meta PagesListMeta `json:"meta"`
	}
}

func registerList(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "listPages",
		Method:      http.MethodGet,
		Path:        "/pages",
		Summary:     "List pages",
		Description: "Returns a paginated list of pages. Unauthenticated and non-pages:read keys receive only published pages.",
		Tags:        []string{"Pages"},
		Security:    []map[string][]string{{"ApiKeyAuth": {}}, {}},
	}, func(ctx context.Context, in *ListPagesInput) (*ListPagesOutput, error) {
		actor := v2.ActorFromContext(ctx)
		f := ListFilter{
			Page:       in.Page,
			PerPage:    in.PerPage,
			Status:     in.Status,
			CategoryID: in.Category,
			TagID:      in.Tag,
		}
		f.IncludeAuthor, f.IncludeCategories, f.IncludeTags = parseIncludes(in.Include)
		result, err := svc.List(ctx, actor, f)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &ListPagesOutput{}
		out.Body.Data = result.Pages
		out.Body.Meta = PagesListMeta{
			Total:   result.Total,
			Page:    result.Page,
			PerPage: result.PerPage,
			Pages:   calcPages(result.Total, result.PerPage),
		}
		return out, nil
	})
}

// GetPageInput carries the id path param and optional include flags.
type GetPageInput struct {
	ID      int64  `path:"id" minimum:"1"`
	Include string `query:"include" doc:"Comma-separated relations to populate: author, categories, tags."`
}

// PageOutput wraps a single Page in {data: …}.
type PageOutput struct {
	Body struct {
		Data Page `json:"data"`
	}
}

func registerGet(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "getPage",
		Method:      http.MethodGet,
		Path:        "/pages/{id}",
		Summary:     "Get page by ID",
		Tags:        []string{"Pages"},
		Security:    []map[string][]string{{"ApiKeyAuth": {}}, {}},
	}, func(ctx context.Context, in *GetPageInput) (*PageOutput, error) {
		actor := v2.ActorFromContext(ctx)
		f := ListFilter{}
		f.IncludeAuthor, f.IncludeCategories, f.IncludeTags = parseIncludes(in.Include)
		page, err := svc.Get(ctx, actor, in.ID, f)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &PageOutput{}
		out.Body.Data = *page
		return out, nil
	})
}

// GetPageBySlugInput carries the slug path param.
type GetPageBySlugInput struct {
	Slug    string `path:"slug" minLength:"1"`
	Include string `query:"include"`
}

func registerGetBySlug(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "getPageBySlug",
		Method:      http.MethodGet,
		Path:        "/pages/slug/{slug}",
		Summary:     "Get page by slug",
		Tags:        []string{"Pages"},
		Security:    []map[string][]string{{"ApiKeyAuth": {}}, {}},
	}, func(ctx context.Context, in *GetPageBySlugInput) (*PageOutput, error) {
		actor := v2.ActorFromContext(ctx)
		f := ListFilter{}
		f.IncludeAuthor, f.IncludeCategories, f.IncludeTags = parseIncludes(in.Include)
		page, err := svc.GetBySlug(ctx, actor, in.Slug, f)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &PageOutput{}
		out.Body.Data = *page
		return out, nil
	})
}

// CreatePageInput carries the request body.
type CreatePageInput struct {
	Body CreatePageBody `contentType:"application/json"`
}

func registerCreate(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "createPage",
		Method:        http.MethodPost,
		Path:          "/pages",
		Summary:       "Create a page",
		Description:   "Requires the `pages:write` permission. Creates a page with optional category / tag links in a single transaction.",
		Tags:          []string{"Pages"},
		Security:      v2.PagesWriteSecurity,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *CreatePageInput) (*PageOutput, error) {
		actor := v2.ActorFromContext(ctx)
		page, err := svc.Create(ctx, actor, in.Body)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &PageOutput{}
		out.Body.Data = *page
		return out, nil
	})
}

// UpdatePageInput carries the id path param plus the partial update body.
type UpdatePageInput struct {
	ID   int64          `path:"id" minimum:"1"`
	Body UpdatePageBody `contentType:"application/json"`
}

func registerUpdate(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "updatePage",
		Method:      http.MethodPut,
		Path:        "/pages/{id}",
		Summary:     "Update a page",
		Description: "Requires the `pages:write` permission. Only provided fields are modified; nullable columns can be cleared by sending null.",
		Tags:        []string{"Pages"},
		Security:    v2.PagesWriteSecurity,
	}, func(ctx context.Context, in *UpdatePageInput) (*PageOutput, error) {
		actor := v2.ActorFromContext(ctx)
		page, err := svc.Update(ctx, actor, in.ID, in.Body)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &PageOutput{}
		out.Body.Data = *page
		return out, nil
	})
}

// DeletePageInput carries just the id path param.
type DeletePageInput struct {
	ID int64 `path:"id" minimum:"1"`
}

func registerDelete(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "deletePage",
		Method:        http.MethodDelete,
		Path:          "/pages/{id}",
		Summary:       "Delete a page",
		Description:   "Requires the `pages:write` permission. Cascades to page categories, tags, and versions.",
		Tags:          []string{"Pages"},
		Security:      v2.PagesWriteSecurity,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *DeletePageInput) (*struct{}, error) {
		actor := v2.ActorFromContext(ctx)
		if err := svc.Delete(ctx, actor, in.ID); err != nil {
			return nil, v2.ToHuma(err)
		}
		return nil, nil
	})
}

// parseIncludes converts a "author,categories,tags" query string into booleans.
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

func calcPages(total int64, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) / float64(perPage)))
}
