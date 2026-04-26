// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package media

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
)

// Register wires all /media operations onto the huma API.
func Register(api huma.API, svc *Service) {
	registerList(api, svc)
	registerGet(api, svc)
	registerUploadSingle(api, svc)
	registerUploadBatch(api, svc)
	registerUpdate(api, svc)
	registerDelete(api, svc)
}

// ListMediaInput carries pagination, filter, and include flags.
type ListMediaInput struct {
	Page    int    `query:"page" default:"1" minimum:"1"`
	PerPage int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
	Type    string `query:"type" enum:"image,document,video" doc:"Filter by media kind."`
	Folder  int64  `query:"folder" doc:"Restrict to this folder id."`
	Search  string `query:"search" doc:"Fuzzy match against filename / alt text."`
	Include string `query:"include" doc:"Comma-separated relations: variants, folder, translations."`
}

// MediaListMeta mirrors pages.MediaListMeta — small duplicate rather than a v2-wide
// cross-domain import, since list response envelopes are per-domain.
type MediaListMeta struct {
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Pages   int   `json:"pages"`
}

// ListMediaOutput is the paginated response envelope.
type ListMediaOutput struct {
	Body struct {
		Data []Media       `json:"data"`
		Meta MediaListMeta `json:"meta"`
	}
}

func registerList(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "listMedia",
		Method:      http.MethodGet,
		Path:        "/media",
		Summary:     "List media",
		Description: "Returns a paginated list of media. Use `type`, `folder`, or `search` to narrow the result set.",
		Tags:        []string{"Media"},
		Security:    []map[string][]string{{"ApiKeyAuth": {}}, {}},
	}, func(ctx context.Context, in *ListMediaInput) (*ListMediaOutput, error) {
		actor := v2.ActorFromContext(ctx)
		f := ListFilter{
			Page:    in.Page,
			PerPage: in.PerPage,
			Type:    in.Type,
			Search:  in.Search,
		}
		if in.Folder > 0 {
			folder := in.Folder
			f.FolderID = &folder
		}
		f.IncludeVariants, f.IncludeFolder, f.IncludeTranslations = parseIncludes(in.Include)
		result, err := svc.List(ctx, actor, f)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &ListMediaOutput{}
		out.Body.Data = result.Media
		out.Body.Meta = MediaListMeta{
			Total:   result.Total,
			Page:    result.Page,
			PerPage: result.PerPage,
			Pages:   calcPages(result.Total, result.PerPage),
		}
		return out, nil
	})
}

// GetMediaInput carries the id path param and optional includes.
type GetMediaInput struct {
	ID      int64  `path:"id" minimum:"1"`
	Include string `query:"include"`
}

// MediaOutput wraps a single Media in {data: …}.
type MediaOutput struct {
	Body struct {
		Data Media `json:"data"`
	}
}

func registerGet(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "getMedia",
		Method:      http.MethodGet,
		Path:        "/media/{id}",
		Summary:     "Get media by ID",
		Tags:        []string{"Media"},
		Security:    []map[string][]string{{"ApiKeyAuth": {}}, {}},
	}, func(ctx context.Context, in *GetMediaInput) (*MediaOutput, error) {
		actor := v2.ActorFromContext(ctx)
		f := ListFilter{}
		f.IncludeVariants, f.IncludeFolder, f.IncludeTranslations = parseIncludes(in.Include)
		m, err := svc.Get(ctx, actor, in.ID, f)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &MediaOutput{}
		out.Body.Data = *m
		return out, nil
	})
}

// UploadMediaInput carries the multipart form body for single-file upload.
type UploadMediaInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File     huma.FormFile `form:"file" contentType:"application/octet-stream,image/*,application/pdf,video/*" required:"true"`
		FolderID string        `form:"folder_id"`
		Alt      string        `form:"alt"`
		Caption  string        `form:"caption"`
	}]
}

func registerUploadSingle(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "uploadMedia",
		Method:        http.MethodPost,
		Path:          "/media",
		Summary:       "Upload a single media file",
		Description:   "Requires `media:write`. For multiple files use `POST /media/batch`.",
		Tags:          []string{"Media"},
		Security:      v2.MediaWriteSecurity,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *UploadMediaInput) (*MediaOutput, error) {
		actor := v2.ActorFromContext(ctx)
		data := in.RawBody.Data()
		if !data.File.IsSet {
			return nil, v2.ToHuma(v2.NewValidationError(map[string]string{"file": "File is required"}, "Validation failed"))
		}
		folderID := parseFolderID(data.FolderID)
		m, err := svc.Upload(ctx, actor, UploadMediaMetadata{FolderID: folderID, Alt: data.Alt, Caption: data.Caption}, UploadedFile{
			Reader:      data.File.File,
			Filename:    data.File.Filename,
			Size:        data.File.Size,
			ContentType: data.File.ContentType,
		})
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &MediaOutput{}
		out.Body.Data = *m
		return out, nil
	})
}

// UploadMediaBatchInput carries multipart form with a list of files.
type UploadMediaBatchInput struct {
	RawBody huma.MultipartFormFiles[struct {
		Files    []huma.FormFile `form:"files" contentType:"application/octet-stream,image/*,application/pdf,video/*" required:"true"`
		FolderID string          `form:"folder_id"`
		Alt      string          `form:"alt"`
		Caption  string          `form:"caption"`
	}]
}

// UploadBatchOutput is the response for a multi-file upload.
type UploadBatchOutput struct {
	Body struct {
		Data struct {
			Uploaded []Media       `json:"uploaded"`
			Errors   []UploadError `json:"errors,omitempty"`
		} `json:"data"`
	}
}

func registerUploadBatch(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "uploadMediaBatch",
		Method:        http.MethodPost,
		Path:          "/media/batch",
		Summary:       "Upload multiple media files",
		Description:   "Requires `media:write`. Per-file errors are returned in `data.errors`; at least one file must succeed.",
		Tags:          []string{"Media"},
		Security:      v2.MediaWriteSecurity,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *UploadMediaBatchInput) (*UploadBatchOutput, error) {
		actor := v2.ActorFromContext(ctx)
		data := in.RawBody.Data()
		files := make([]UploadedFile, 0, len(data.Files))
		for _, f := range data.Files {
			if !f.IsSet {
				continue
			}
			files = append(files, UploadedFile{
				Reader:      f.File,
				Filename:    f.Filename,
				Size:        f.Size,
				ContentType: f.ContentType,
			})
		}
		folderID := parseFolderID(data.FolderID)
		res, err := svc.UploadBatch(ctx, actor, UploadMediaMetadata{FolderID: folderID, Alt: data.Alt, Caption: data.Caption}, files)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &UploadBatchOutput{}
		out.Body.Data.Uploaded = res.Uploaded
		out.Body.Data.Errors = res.Errors
		return out, nil
	})
}

// UpdateMediaInput carries the id path param plus the partial update body.
type UpdateMediaInput struct {
	ID   int64           `path:"id" minimum:"1"`
	Body UpdateMediaBody `contentType:"application/json"`
}

func registerUpdate(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "updateMedia",
		Method:      http.MethodPut,
		Path:        "/media/{id}",
		Summary:     "Update media metadata",
		Description: "Requires `media:write`. Pass `folder_id: 0` or null to move the item to the root folder.",
		Tags:        []string{"Media"},
		Security:    v2.MediaWriteSecurity,
	}, func(ctx context.Context, in *UpdateMediaInput) (*MediaOutput, error) {
		actor := v2.ActorFromContext(ctx)
		m, err := svc.Update(ctx, actor, in.ID, in.Body)
		if err != nil {
			return nil, v2.ToHuma(err)
		}
		out := &MediaOutput{}
		out.Body.Data = *m
		return out, nil
	})
}

// DeleteMediaInput carries only the id path param.
type DeleteMediaInput struct {
	ID int64 `path:"id" minimum:"1"`
}

func registerDelete(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "deleteMedia",
		Method:        http.MethodDelete,
		Path:          "/media/{id}",
		Summary:       "Delete media",
		Description:   "Requires `media:write`. Removes the stored file, all generated variants, and the database row.",
		Tags:          []string{"Media"},
		Security:      v2.MediaWriteSecurity,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *DeleteMediaInput) (*struct{}, error) {
		actor := v2.ActorFromContext(ctx)
		if err := svc.Delete(ctx, actor, in.ID); err != nil {
			return nil, v2.ToHuma(err)
		}
		return nil, nil
	})
}

func parseIncludes(include string) (variants, folder, translations bool) {
	if include == "" {
		return
	}
	for _, part := range strings.Split(include, ",") {
		switch strings.TrimSpace(part) {
		case "variants":
			variants = true
		case "folder":
			folder = true
		case "translations":
			translations = true
		}
	}
	return
}

func parseFolderID(raw string) *int64 {
	if raw == "" {
		return nil
	}
	// strconv.ParseInt rejects non-numeric input AND int64 overflow (returning
	// ErrRange), both of which the old hand-rolled scan silently wrapped into
	// bogus positive IDs that could route a caller's upload into an unrelated
	// folder. Keep the same `nil = unset` contract for invalid input.
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	return &id
}

func calcPages(total int64, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) / float64(perPage)))
}
