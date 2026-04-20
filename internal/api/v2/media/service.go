// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// Service owns every media operation. Uploads delegate to service.MediaService
// (disk IO + variant generation); DB mutations go through the sqlc-generated
// store.Queries directly.
type Service struct {
	db        *sql.DB
	queries   *store.Queries
	events    *service.EventService
	uploader  *service.MediaService
	uploadDir string
}

// NewService constructs a v2 media Service. The uploadDir is used when the
// underlying service.MediaService writes files to disk. events may be nil in
// tests; when non-nil every successful write records an audit row.
func NewService(db *sql.DB, queries *store.Queries, events *service.EventService, uploadDir string) *Service {
	return &Service{
		db:        db,
		queries:   queries,
		events:    events,
		uploader:  service.NewMediaService(db, uploadDir),
		uploadDir: uploadDir,
	}
}

// requireWritePerm returns a domain error when the actor cannot write media.
func (s *Service) requireWritePerm(a v2.Actor) error {
	if a.APIKey == nil {
		return v2.NewError(v2.ErrUnauthorized, "API key required")
	}
	if !a.HasPermission(model.PermissionMediaWrite) {
		return v2.NewError(v2.ErrForbidden, "media:write permission required")
	}
	return nil
}

// dtoFromStore converts a store.Medium to a Media DTO, populating URLs.
func dtoFromStore(m store.Medium) Media {
	dto := Media{
		ID:         m.ID,
		UUID:       m.Uuid,
		Filename:   m.Filename,
		MimeType:   m.MimeType,
		Size:       m.Size,
		UploadedBy: m.UploadedBy,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
	if m.Width.Valid {
		dto.Width = &m.Width.Int64
	}
	if m.Height.Valid {
		dto.Height = &m.Height.Int64
	}
	if m.Alt.Valid {
		dto.Alt = m.Alt.String
	}
	if m.Caption.Valid {
		dto.Caption = m.Caption.String
	}
	if m.FolderID.Valid {
		dto.FolderID = &m.FolderID.Int64
	}
	dto.URLs = &URLs{Original: "/uploads/originals/" + m.Uuid + "/" + m.Filename}
	if handler.IsImageMime(m.MimeType) {
		dto.URLs.Thumbnail = "/uploads/thumbnail/" + m.Uuid + "/" + m.Filename
		dto.URLs.Medium = "/uploads/medium/" + m.Uuid + "/" + m.Filename
		dto.URLs.Large = "/uploads/large/" + m.Uuid + "/" + m.Filename
	}
	return dto
}

// populateIncludes attaches optional relations to a Media DTO.
func (s *Service) populateIncludes(ctx context.Context, dto *Media, mediaID int64, f ListFilter) {
	if f.IncludeVariants {
		if variants, err := s.queries.GetMediaVariants(ctx, mediaID); err == nil {
			dto.Variants = make([]Variant, 0, len(variants))
			for _, v := range variants {
				dto.Variants = append(dto.Variants, Variant{
					ID:        v.ID,
					Type:      v.Type,
					Width:     v.Width,
					Height:    v.Height,
					Size:      v.Size,
					URL:       "/uploads/" + v.Type + "/" + dto.UUID + "/" + dto.Filename,
					CreatedAt: v.CreatedAt,
				})
			}
		}
	}
	if f.IncludeFolder && dto.FolderID != nil {
		if folder, err := s.queries.GetMediaFolderByID(ctx, *dto.FolderID); err == nil {
			f := &Folder{
				ID:        folder.ID,
				Name:      folder.Name,
				Position:  folder.Position,
				CreatedAt: folder.CreatedAt,
			}
			if folder.ParentID.Valid {
				f.ParentID = &folder.ParentID.Int64
			}
			dto.Folder = f
		}
	}
	if f.IncludeTranslations {
		if translations, err := s.queries.GetMediaTranslations(ctx, mediaID); err == nil && len(translations) > 0 {
			dto.Translations = make([]Translation, 0, len(translations))
			for _, t := range translations {
				dto.Translations = append(dto.Translations, Translation{
					LanguageID:   t.LanguageID,
					LanguageCode: t.LanguageCode,
					LanguageName: t.LanguageName,
					Alt:          t.Alt,
					Caption:      t.Caption,
				})
			}
		}
	}
}

// List returns a paginated slice of media with optional filters.
func (s *Service) List(ctx context.Context, a v2.Actor, f ListFilter) (*ListResult, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PerPage <= 0 || f.PerPage > 100 {
		f.PerPage = 20
	}
	limit := int64(f.PerPage)
	offset := int64((f.Page - 1) * f.PerPage)

	var rows []store.Medium
	var total int64
	var err error
	switch {
	case f.Search != "":
		pattern := "%" + f.Search + "%"
		rows, err = s.queries.SearchMedia(ctx, store.SearchMediaParams{
			Filename: pattern,
			Alt:      util.NullStringFromValue(pattern),
			Limit:    limit,
		})
		total = int64(len(rows))
	case f.FolderID != nil:
		rows, err = s.queries.ListMediaInFolder(ctx, store.ListMediaInFolderParams{
			FolderID: util.NullInt64FromPtr(f.FolderID),
			Limit:    limit,
			Offset:   offset,
		})
		if err == nil {
			total, err = s.queries.CountMediaInFolder(ctx, util.NullInt64FromPtr(f.FolderID))
		}
	case f.Type != "":
		pattern, ok := mimePatternForType(f.Type)
		if !ok {
			return nil, v2.NewValidationError(map[string]string{"type": "type must be image, document, or video"}, "Validation failed")
		}
		rows, err = s.queries.ListMediaByType(ctx, store.ListMediaByTypeParams{MimeType: pattern, Limit: limit, Offset: offset})
		if err == nil {
			total, err = s.queries.CountMediaByType(ctx, pattern)
		}
	default:
		rows, err = s.queries.ListMedia(ctx, store.ListMediaParams{Limit: limit, Offset: offset})
		if err == nil {
			total, err = s.queries.CountMedia(ctx)
		}
	}
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to list media")
	}

	dtos := make([]Media, 0, len(rows))
	for _, m := range rows {
		dto := dtoFromStore(m)
		s.populateIncludes(ctx, &dto, m.ID, f)
		dtos = append(dtos, dto)
	}
	_ = a // no per-actor visibility rules today; kept for symmetry with Pages
	return &ListResult{Media: dtos, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}

// Get loads a single media item by ID.
func (s *Service) Get(ctx context.Context, a v2.Actor, id int64, f ListFilter) (*Media, error) {
	m, err := s.queries.GetMediaByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, v2.NewError(v2.ErrNotFound, fmt.Sprintf("media %d not found", id))
		}
		return nil, v2.NewError(v2.ErrInternal, "Failed to load media")
	}
	dto := dtoFromStore(m)
	s.populateIncludes(ctx, &dto, m.ID, f)
	_ = a
	return &dto, nil
}

// UploadedFile is the minimal interface Service.Upload needs: a reader plus
// the metadata usually carried by *multipart.FileHeader. This keeps the
// service callable from tests without a real HTTP request.
type UploadedFile struct {
	Reader      multipart.File
	Filename    string
	Size        int64
	ContentType string
}

// UploadBatchResult is returned by UploadBatch and carries per-file errors.
type UploadBatchResult struct {
	Uploaded []Media
	Errors   []UploadError
}

// UploadError associates a single-file failure with its filename.
type UploadError struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// Upload ingests a single file via the underlying MediaService.
func (s *Service) Upload(ctx context.Context, a v2.Actor, in UploadMediaMetadata, file UploadedFile) (*Media, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	defaultLang, err := s.queries.GetDefaultLanguage(ctx)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to resolve default language")
	}
	header := &multipart.FileHeader{
		Filename: file.Filename,
		Size:     file.Size,
		Header:   make(textproto.MIMEHeader),
	}
	if file.ContentType != "" {
		header.Header.Set("Content-Type", file.ContentType)
	}
	result, err := s.uploader.Upload(ctx, file.Reader, header, a.APIKey.CreatedBy, in.FolderID, defaultLang.Code)
	if err != nil {
		var clientErr *service.ClientError
		if errors.As(err, &clientErr) {
			return nil, v2.NewValidationError(map[string]string{"file": clientErr.Message}, "Upload failed")
		}
		return nil, v2.NewError(v2.ErrInternal, "Upload failed")
	}

	if in.Alt != "" || in.Caption != "" {
		params := store.UpdateMediaParams{
			ID:           result.Media.ID,
			Filename:     result.Media.Filename,
			Alt:          util.NullStringFromValue(in.Alt),
			Caption:      util.NullStringFromValue(in.Caption),
			FolderID:     result.Media.FolderID,
			LanguageCode: result.Media.LanguageCode,
			UpdatedAt:    time.Now(),
		}
		updated, err := s.queries.UpdateMedia(ctx, params)
		if err != nil {
			// File is on disk and the media row exists. Failing the whole upload
			// would orphan the file; instead log so operators can reconcile and
			// return the row without the alt/caption applied, matching the state
			// actually persisted to the database.
			slog.Warn("failed to apply alt/caption to uploaded media",
				"error", err, "media_id", result.Media.ID)
		} else {
			result.Media = updated
		}
	}

	dto := dtoFromStore(result.Media)
	dto.Variants = variantsToDTOs(result.Variants, dto.UUID, dto.Filename)
	s.logMediaAudit(ctx, a, "API: Media uploaded", map[string]any{
		"media_id": result.Media.ID,
		"filename": result.Media.Filename,
		"mime":     result.Media.MimeType,
		"size":     result.Media.Size,
	})
	return &dto, nil
}

// UploadBatch ingests multiple files, returning partial success with per-file
// errors rather than failing the whole request on the first bad file.
func (s *Service) UploadBatch(ctx context.Context, a v2.Actor, in UploadMediaMetadata, files []UploadedFile) (*UploadBatchResult, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, v2.NewValidationError(map[string]string{"files": "At least one file is required"}, "Validation failed")
	}
	res := &UploadBatchResult{}
	for _, f := range files {
		dto, err := s.Upload(ctx, a, in, f)
		if err != nil {
			res.Errors = append(res.Errors, UploadError{Filename: f.Filename, Error: scrubUploadError(err)})
			continue
		}
		res.Uploaded = append(res.Uploaded, *dto)
	}
	if len(res.Uploaded) == 0 {
		return nil, v2.NewValidationError(map[string]string{"files": "All uploads failed"}, "Upload failed")
	}
	// Per-file uploads already logged by Upload itself; this event records the
	// batch shape so operators see "one batch" instead of N individual entries
	// when reconstructing a bulk import.
	s.logMediaAudit(ctx, a, "API: Media uploaded (batch)", map[string]any{
		"uploaded_count": len(res.Uploaded),
		"error_count":    len(res.Errors),
	})
	return res, nil
}

// Update changes metadata on a media item.
func (s *Service) Update(ctx context.Context, a v2.Actor, id int64, in UpdateMediaBody) (*Media, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	existing, err := s.queries.GetMediaByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, v2.NewError(v2.ErrNotFound, fmt.Sprintf("media %d not found", id))
		}
		return nil, v2.NewError(v2.ErrInternal, "Failed to load media")
	}
	params := store.UpdateMediaParams{
		ID:           existing.ID,
		Filename:     existing.Filename,
		Alt:          existing.Alt,
		Caption:      existing.Caption,
		FolderID:     existing.FolderID,
		LanguageCode: existing.LanguageCode,
		UpdatedAt:    time.Now(),
	}
	if in.Filename != nil && strings.TrimSpace(*in.Filename) != "" {
		safe, err := sanitizeDisplayFilename(*in.Filename)
		if err != nil {
			return nil, err
		}
		params.Filename = safe
	}
	if in.Alt != nil {
		params.Alt = util.NullStringFromValue(*in.Alt)
	}
	if in.Caption != nil {
		params.Caption = util.NullStringFromValue(*in.Caption)
	}
	if in.FolderID.IsSet {
		if in.FolderID.IsNull || in.FolderID.Value == 0 {
			params.FolderID = sql.NullInt64{}
		} else {
			params.FolderID = sql.NullInt64{Int64: in.FolderID.Value, Valid: true}
		}
	}
	updated, err := s.queries.UpdateMedia(ctx, params)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to update media")
	}
	dto := dtoFromStore(updated)
	if variants, err := s.queries.GetMediaVariants(ctx, updated.ID); err == nil {
		dto.Variants = variantsToDTOs(variants, dto.UUID, dto.Filename)
	}
	s.logMediaAudit(ctx, a, "API: Media updated", map[string]any{
		"media_id": updated.ID,
		"filename": updated.Filename,
	})
	return &dto, nil
}

// Delete removes a media item and its variants via the underlying MediaService.
func (s *Service) Delete(ctx context.Context, a v2.Actor, id int64) error {
	if err := s.requireWritePerm(a); err != nil {
		return err
	}
	if _, err := s.queries.GetMediaByID(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v2.NewError(v2.ErrNotFound, fmt.Sprintf("media %d not found", id))
		}
		return v2.NewError(v2.ErrInternal, "Failed to load media")
	}
	if err := s.uploader.Delete(ctx, id); err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to delete media")
	}
	s.logMediaAudit(ctx, a, "API: Media deleted", map[string]any{
		"media_id": id,
	})
	return nil
}

// logMediaAudit records an audit-level event for a successful Media mutation.
// Best-effort: logging failures are swallowed because the upload/delete side
// effect is already committed; see the pages equivalent for the same rationale.
func (s *Service) logMediaAudit(ctx context.Context, a v2.Actor, message string, meta map[string]any) {
	if s.events == nil || a.APIKey == nil {
		return
	}
	userID := a.APIKey.CreatedBy
	_ = s.events.LogMediaEvent(ctx, model.EventLevelInfo, message, &userID, "", "", meta)
}

// variantsToDTOs converts store.MediaVariant slices to Variant DTO slices,
// constructing the public URL per variant.
func variantsToDTOs(variants []store.MediaVariant, uuid, filename string) []Variant {
	if len(variants) == 0 {
		return nil
	}
	out := make([]Variant, 0, len(variants))
	for _, v := range variants {
		out = append(out, Variant{
			ID:        v.ID,
			Type:      v.Type,
			Width:     v.Width,
			Height:    v.Height,
			Size:      v.Size,
			URL:       "/uploads/" + v.Type + "/" + uuid + "/" + filename,
			CreatedAt: v.CreatedAt,
		})
	}
	return out
}

// scrubUploadError returns an API-safe description of a per-file upload failure.
// Domain errors keep their curated message; every other error (filesystem path,
// imaging library diagnostics, etc.) is flattened to a generic string so we do
// not leak internal details in the response body.
func scrubUploadError(err error) string {
	var de *v2.Error
	if errors.As(err, &de) {
		return de.Msg
	}
	return "Upload failed"
}

// sanitizeDisplayFilename hardens a caller-supplied filename for the metadata
// update path: the file on disk never moves, but the display filename is shown
// in admin templates and must not carry path separators or HTML-dangerous chars.
func sanitizeDisplayFilename(raw string) (string, error) {
	base := filepath.Base(strings.TrimSpace(raw))
	if base == "" || base == "." || base == ".." {
		return "", v2.NewValidationError(
			map[string]string{"filename": "Invalid filename"},
			"Validation failed",
		)
	}
	replacer := strings.NewReplacer(
		" ", "-",
		"'", "",
		"\"", "",
		"<", "",
		">", "",
		"&", "",
		"#", "",
		"?", "",
		"%", "",
		"\\", "",
	)
	cleaned := replacer.Replace(base)
	if cleaned == "" {
		return "", v2.NewValidationError(
			map[string]string{"filename": "Filename is empty after sanitization"},
			"Validation failed",
		)
	}
	return cleaned, nil
}

// mimePatternForType maps a friendly type filter to a mime pattern for the
// ListMediaByType / CountMediaByType queries. Returns (pattern, ok).
func mimePatternForType(t string) (string, bool) {
	switch t {
	case "image":
		return "image/%", true
	case "video":
		return "video/%", true
	case "document":
		return "application/pdf", true
	}
	return "", false
}

// UploadedFileFromReader is a helper for tests: builds an UploadedFile from an
// in-memory reader.
func UploadedFileFromReader(r io.Reader, filename, contentType string, size int64) UploadedFile {
	return UploadedFile{Reader: nopCloser{Reader: r}, Filename: filename, Size: size, ContentType: contentType}
}

// nopCloser adapts an io.Reader to multipart.File (adds a no-op Close and
// lets the reader be type-asserted to io.ReadSeeker when needed).
type nopCloser struct {
	io.Reader
}

func (nopCloser) Close() error { return nil }

// Read / Seek delegate to the embedded reader; if the underlying reader isn't
// seekable, Seek returns an error. Upload callers mostly need Read.
func (n nopCloser) ReadAt(p []byte, off int64) (int, error) {
	if ra, ok := n.Reader.(io.ReaderAt); ok {
		return ra.ReadAt(p, off)
	}
	return 0, io.ErrUnexpectedEOF
}

// Seek satisfies multipart.File. Non-seekable readers will error at runtime.
func (n nopCloser) Seek(offset int64, whence int) (int64, error) {
	if s, ok := n.Reader.(io.Seeker); ok {
		return s.Seek(offset, whence)
	}
	return 0, io.ErrUnexpectedEOF
}
