// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package taxonomy

import (
	"context"
	"math"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
)

// Register wires TaxonomyTag + TaxonomyCategory operations onto the huma API.
func Register(api huma.API, svc *Service) {
	registerListTags(api, svc)
	registerGetTag(api, svc)
	registerCreateTag(api, svc)
	registerUpdateTag(api, svc)
	registerDeleteTag(api, svc)
	registerListCategories(api, svc)
	registerGetCategory(api, svc)
	registerCreateCategory(api, svc)
	registerUpdateCategory(api, svc)
	registerDeleteCategory(api, svc)
}

// TaxonomyListMeta carries pagination metadata (domain-prefixed so huma's
// schema registry doesn't collide with pages/media).
type TaxonomyListMeta struct {
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Pages   int   `json:"pages"`
}

// -----------------------------------------------------------------------------
// TaxonomyTag operations
// -----------------------------------------------------------------------------

// ListTagsInput carries pagination params.
type ListTagsInput struct {
	Page    int `query:"page" default:"1" minimum:"1"`
	PerPage int `query:"per_page" default:"50" minimum:"1" maximum:"100"`
}

// ListTagsOutput is the paginated tags response envelope.
type ListTagsOutput struct {
	Body struct {
		Data []TaxonomyTag    `json:"data"`
		Meta TaxonomyListMeta `json:"meta"`
	}
}

func registerListTags(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "listTags",
		Method:      http.MethodGet,
		Path:        "/tags",
		Summary:     "List tags",
		Tags:        []string{"Tags"},
		Security:    []map[string][]string{},
	}, func(ctx context.Context, in *ListTagsInput) (*ListTagsOutput, error) {
		res, err := svc.ListTags(ctx, in.Page, in.PerPage)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &ListTagsOutput{}
		out.Body.Data = res.Tags
		out.Body.Meta = TaxonomyListMeta{Total: res.Total, Page: res.Page, PerPage: res.PerPage, Pages: calcPages(res.Total, res.PerPage)}
		return out, nil
	})
}

// GetTagInput carries the id path param.
type GetTagInput struct {
	ID int64 `path:"id" minimum:"1"`
}

// TagOutput wraps a single TaxonomyTag.
type TagOutput struct {
	Body struct {
		Data TaxonomyTag `json:"data"`
	}
}

func registerGetTag(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "getTag",
		Method:      http.MethodGet,
		Path:        "/tags/{id}",
		Summary:     "Get tag by ID",
		Tags:        []string{"Tags"},
		Security:    []map[string][]string{},
	}, func(ctx context.Context, in *GetTagInput) (*TagOutput, error) {
		t, err := svc.GetTag(ctx, in.ID)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &TagOutput{}
		out.Body.Data = *t
		return out, nil
	})
}

// CreateTagInput carries the JSON body.
type CreateTagInput struct {
	Body CreateTagBody `contentType:"application/json"`
}

func registerCreateTag(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "createTag",
		Method:        http.MethodPost,
		Path:          "/tags",
		Summary:       "Create a tag",
		Description:   "Requires the `taxonomy:write` permission.",
		Tags:          []string{"Tags"},
		Security:      v2.TaxonomyWriteSecurity,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *CreateTagInput) (*TagOutput, error) {
		actor := v2.ActorFromContext(ctx)
		t, err := svc.CreateTag(ctx, actor, in.Body)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &TagOutput{}
		out.Body.Data = *t
		return out, nil
	})
}

// UpdateTagInput carries the id path param + patch body.
type UpdateTagInput struct {
	ID   int64         `path:"id" minimum:"1"`
	Body UpdateTagBody `contentType:"application/json"`
}

func registerUpdateTag(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "updateTag",
		Method:      http.MethodPut,
		Path:        "/tags/{id}",
		Summary:     "Update a tag",
		Description: "Requires the `taxonomy:write` permission.",
		Tags:        []string{"Tags"},
		Security:    v2.TaxonomyWriteSecurity,
	}, func(ctx context.Context, in *UpdateTagInput) (*TagOutput, error) {
		actor := v2.ActorFromContext(ctx)
		t, err := svc.UpdateTag(ctx, actor, in.ID, in.Body)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &TagOutput{}
		out.Body.Data = *t
		return out, nil
	})
}

// DeleteTagInput carries the id path param.
type DeleteTagInput struct {
	ID int64 `path:"id" minimum:"1"`
}

func registerDeleteTag(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "deleteTag",
		Method:        http.MethodDelete,
		Path:          "/tags/{id}",
		Summary:       "Delete a tag",
		Description:   "Requires the `taxonomy:write` permission.",
		Tags:          []string{"Tags"},
		Security:      v2.TaxonomyWriteSecurity,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *DeleteTagInput) (*struct{}, error) {
		actor := v2.ActorFromContext(ctx)
		if err := svc.DeleteTag(ctx, actor, in.ID); err != nil {
			return nil, v2.ToHuma(err)
		}
		return nil, nil
	})
}

// -----------------------------------------------------------------------------
// TaxonomyCategory operations
// -----------------------------------------------------------------------------

// ListCategoriesInput toggles between nested tree and flat list output.
type ListCategoriesInput struct {
	Flat bool `query:"flat" doc:"Return a flat list instead of a nested tree."`
}

// ListCategoriesOutput is the categories response envelope.
type ListCategoriesOutput struct {
	Body struct {
		Data []*TaxonomyCategory `json:"data"`
	}
}

func registerListCategories(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "listCategories",
		Method:      http.MethodGet,
		Path:        "/categories",
		Summary:     "List categories",
		Description: "Defaults to a nested tree (parent → children). Pass `flat=true` for a flat list.",
		Tags:        []string{"Categories"},
		Security:    []map[string][]string{},
	}, func(ctx context.Context, in *ListCategoriesInput) (*ListCategoriesOutput, error) {
		cats, err := svc.ListCategories(ctx, !in.Flat)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &ListCategoriesOutput{}
		out.Body.Data = cats
		return out, nil
	})
}

// GetCategoryInput carries the id path param.
type GetCategoryInput struct {
	ID int64 `path:"id" minimum:"1"`
}

// CategoryOutput wraps a single TaxonomyCategory.
type CategoryOutput struct {
	Body struct {
		Data TaxonomyCategory `json:"data"`
	}
}

func registerGetCategory(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "getCategory",
		Method:      http.MethodGet,
		Path:        "/categories/{id}",
		Summary:     "Get category by ID",
		Description: "Includes direct children; does NOT recurse grandchildren.",
		Tags:        []string{"Categories"},
		Security:    []map[string][]string{},
	}, func(ctx context.Context, in *GetCategoryInput) (*CategoryOutput, error) {
		c, err := svc.GetCategory(ctx, in.ID)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &CategoryOutput{}
		out.Body.Data = *c
		return out, nil
	})
}

// CreateCategoryInput carries the JSON body.
type CreateCategoryInput struct {
	Body CreateCategoryBody `contentType:"application/json"`
}

func registerCreateCategory(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "createCategory",
		Method:        http.MethodPost,
		Path:          "/categories",
		Summary:       "Create a category",
		Description:   "Requires the `taxonomy:write` permission.",
		Tags:          []string{"Categories"},
		Security:      v2.TaxonomyWriteSecurity,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *CreateCategoryInput) (*CategoryOutput, error) {
		actor := v2.ActorFromContext(ctx)
		c, err := svc.CreateCategory(ctx, actor, in.Body)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &CategoryOutput{}
		out.Body.Data = *c
		return out, nil
	})
}

// UpdateCategoryInput carries the id path param + patch body.
type UpdateCategoryInput struct {
	ID   int64              `path:"id" minimum:"1"`
	Body UpdateCategoryBody `contentType:"application/json"`
}

func registerUpdateCategory(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "updateCategory",
		Method:      http.MethodPut,
		Path:        "/categories/{id}",
		Summary:     "Update a category",
		Description: "Requires the `taxonomy:write` permission. Circular parent references are rejected.",
		Tags:        []string{"Categories"},
		Security:    v2.TaxonomyWriteSecurity,
	}, func(ctx context.Context, in *UpdateCategoryInput) (*CategoryOutput, error) {
		actor := v2.ActorFromContext(ctx)
		c, err := svc.UpdateCategory(ctx, actor, in.ID, in.Body)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &CategoryOutput{}
		out.Body.Data = *c
		return out, nil
	})
}

// DeleteCategoryInput carries the id path param.
type DeleteCategoryInput struct {
	ID int64 `path:"id" minimum:"1"`
}

func registerDeleteCategory(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "deleteCategory",
		Method:        http.MethodDelete,
		Path:          "/categories/{id}",
		Summary:       "Delete a category",
		Description:   "Requires the `taxonomy:write` permission. Returns 409 if child categories still exist.",
		Tags:          []string{"Categories"},
		Security:      v2.TaxonomyWriteSecurity,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *DeleteCategoryInput) (*struct{}, error) {
		actor := v2.ActorFromContext(ctx)
		if err := svc.DeleteCategory(ctx, actor, in.ID); err != nil {
			return nil, v2.ToHuma(err)
		}
		return nil, nil
	})
}

func calcPages(total int64, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) / float64(perPage)))
}
