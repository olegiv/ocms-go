// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"

	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
)

// defaultTagsPerPage mirrors the per_page default of GET /api/v2/tags.
const defaultTagsPerPage = 50

// ListTagsInput is the input of list_tags, mirroring GET /api/v2/tags.
type ListTagsInput struct {
	Page    int `json:"page,omitempty" default:"1" minimum:"1" maximum:"21474836" doc:"1-indexed page number (max 21474836)."`
	PerPage int `json:"per_page,omitempty" default:"50" minimum:"1" maximum:"100" doc:"Items per page (max 100)."`
}

// ListTagsResult is the output of list_tags.
type ListTagsResult struct {
	Tags []taxonomy.TaxonomyTag `json:"tags"`
	Pagination
}

// ListCategoriesInput is the input of list_categories, mirroring
// GET /api/v2/categories.
type ListCategoriesInput struct {
	Flat bool `json:"flat,omitempty" doc:"Return a flat list instead of a nested tree."`
}

// ListCategoriesResult is the output of list_categories.
type ListCategoriesResult struct {
	Categories []*taxonomy.TaxonomyCategory `json:"categories" doc:"Root categories with nested children, or every category when flat."`
}

// listTags implements list_tags over taxonomy.Service.ListTags.
func (m *Module) listTags(ctx context.Context, _ *toolCall, in ListTagsInput) (ListTagsResult, error) {
	page, perPage := normalizePaging(in.Page, in.PerPage, defaultTagsPerPage)
	result, err := m.svc.taxonomy.ListTags(ctx, page, perPage)
	if err != nil {
		return ListTagsResult{}, err
	}
	tags := result.Tags
	if tags == nil {
		tags = []taxonomy.TaxonomyTag{}
	}
	return ListTagsResult{
		Tags:       tags,
		Pagination: newPagination(result.Total, result.Page, result.PerPage),
	}, nil
}

// getTag implements get_tag over taxonomy.Service.GetTag.
func (m *Module) getTag(ctx context.Context, _ *toolCall, in IDInput) (taxonomy.TaxonomyTag, error) {
	tag, err := m.svc.taxonomy.GetTag(ctx, in.ID)
	if err != nil {
		return taxonomy.TaxonomyTag{}, err
	}
	return *tag, nil
}

// listCategories implements list_categories over taxonomy.Service.ListCategories.
func (m *Module) listCategories(ctx context.Context, _ *toolCall, in ListCategoriesInput) (ListCategoriesResult, error) {
	categories, err := m.svc.taxonomy.ListCategories(ctx, !in.Flat)
	if err != nil {
		return ListCategoriesResult{}, err
	}
	if categories == nil {
		categories = []*taxonomy.TaxonomyCategory{}
	}
	return ListCategoriesResult{Categories: categories}, nil
}

// getCategory implements get_category over taxonomy.Service.GetCategory.
func (m *Module) getCategory(ctx context.Context, _ *toolCall, in IDInput) (taxonomy.TaxonomyCategory, error) {
	category, err := m.svc.taxonomy.GetCategory(ctx, in.ID)
	if err != nil {
		return taxonomy.TaxonomyCategory{}, err
	}
	return *category, nil
}
