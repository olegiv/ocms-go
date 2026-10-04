// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"

	"github.com/olegiv/ocms-go/internal/api/v2/media"
)

// defaultMediaPerPage mirrors the per_page default of GET /api/v2/media.
const defaultMediaPerPage = 20

// IDInput is the input of the get_* tools that take a numeric id. It mirrors
// the {id} path parameter of the REST v2 get operations.
type IDInput struct {
	ID int64 `json:"id" minimum:"1" doc:"Id of the item to return."`
}

// ListMediaInput is the input of list_media. Filters and paging mirror the
// query parameters of GET /api/v2/media (drift-tested).
type ListMediaInput struct {
	Type     string `json:"type,omitempty" enum:"image,document,video" doc:"Only media of this kind."`
	FolderID int64  `json:"folder_id,omitempty" doc:"Only media in this folder id."`
	Search   string `json:"search,omitempty" doc:"Match against filename and alt text."`
	Page     int    `json:"page,omitempty" default:"1" minimum:"1" doc:"1-indexed page number."`
	PerPage  int    `json:"per_page,omitempty" default:"20" minimum:"1" maximum:"100" doc:"Items per page (max 100)."`
}

// ListMediaResult is the output of list_media.
type ListMediaResult struct {
	Media []media.Media `json:"media"`
	Pagination
}

// listMedia implements list_media over media.Service.List.
func (m *Module) listMedia(ctx context.Context, call *toolCall, in ListMediaInput) (ListMediaResult, error) {
	page, perPage := normalizePaging(in.Page, in.PerPage, defaultMediaPerPage)
	filter := media.ListFilter{
		Page:    page,
		PerPage: perPage,
		Type:    in.Type,
		Search:  in.Search,
	}
	if in.FolderID > 0 {
		folderID := in.FolderID
		filter.FolderID = &folderID
	}
	result, err := m.svc.media.List(ctx, call.actor, filter)
	if err != nil {
		return ListMediaResult{}, err
	}
	items := result.Media
	if items == nil {
		items = []media.Media{}
	}
	return ListMediaResult{
		Media:      items,
		Pagination: newPagination(result.Total, result.Page, result.PerPage),
	}, nil
}

// getMedia implements get_media over media.Service.Get, with every relation.
func (m *Module) getMedia(ctx context.Context, call *toolCall, in IDInput) (media.Media, error) {
	item, err := m.svc.media.Get(ctx, call.actor, in.ID, media.ListFilter{
		IncludeVariants:     true,
		IncludeFolder:       true,
		IncludeTranslations: true,
	})
	if err != nil {
		return media.Media{}, err
	}
	return *item, nil
}
