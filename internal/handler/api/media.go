// Package api provides REST API handlers for the CMS.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/middleware"
	"ocms-go/internal/service"
	"ocms-go/internal/store"
)

// MediaResponse represents a media item in API responses.
type MediaResponse struct {
	ID         int64             `json:"id"`
	UUID       string            `json:"uuid"`
	Filename   string            `json:"filename"`
	MimeType   string            `json:"mime_type"`
	Size       int64             `json:"size"`
	Width      *int64            `json:"width,omitempty"`
	Height     *int64            `json:"height,omitempty"`
	Alt        string            `json:"alt"`
	Caption    string            `json:"caption"`
	FolderID   *int64            `json:"folder_id,omitempty"`
	UploadedBy int64             `json:"uploaded_by"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	URLs       *MediaURLs        `json:"urls,omitempty"`
	Variants   []VariantResponse `json:"variants,omitempty"`
	Folder     *FolderResponse   `json:"folder,omitempty"`
}

// MediaURLs contains URLs for different media sizes.
type MediaURLs struct {
	Original  string `json:"original"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Medium    string `json:"medium,omitempty"`
	Large     string `json:"large,omitempty"`
}

// VariantResponse represents a media variant in API responses.
type VariantResponse struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Width     int64     `json:"width"`
	Height    int64     `json:"height"`
	Size      int64     `json:"size"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// FolderResponse represents a media folder in API responses.
type FolderResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	ParentID  *int64    `json:"parent_id,omitempty"`
	Position  int64     `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

// UpdateMediaRequest represents the request body for updating media metadata.
type UpdateMediaRequest struct {
	Filename *string `json:"filename,omitempty"`
	Alt      *string `json:"alt,omitempty"`
	Caption  *string `json:"caption,omitempty"`
	FolderID *int64  `json:"folder_id,omitempty"`
}

// storeMediaToResponse converts a store.Medium to MediaResponse.
func storeMediaToResponse(m store.Medium) MediaResponse {
	resp := MediaResponse{
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
		resp.Width = &m.Width.Int64
	}
	if m.Height.Valid {
		resp.Height = &m.Height.Int64
	}
	if m.Alt.Valid {
		resp.Alt = m.Alt.String
	}
	if m.Caption.Valid {
		resp.Caption = m.Caption.String
	}
	if m.FolderID.Valid {
		resp.FolderID = &m.FolderID.Int64
	}

	// Add URLs
	resp.URLs = &MediaURLs{
		Original: "/uploads/originals/" + m.Uuid + "/" + m.Filename,
	}
	if isImageMime(m.MimeType) {
		resp.URLs.Thumbnail = "/uploads/thumbnail/" + m.Uuid + "/" + m.Filename
		resp.URLs.Medium = "/uploads/medium/" + m.Uuid + "/" + m.Filename
		resp.URLs.Large = "/uploads/large/" + m.Uuid + "/" + m.Filename
	}

	return resp
}

// isImageMime checks if the MIME type is an image.
func isImageMime(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

// ListMedia handles GET /api/v1/media
// Public: returns all media
// With API key: enhanced access (same for now, could add private media later)
func (h *Handler) ListMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse query parameters
	typeFilter := r.URL.Query().Get("type")
	folderIDStr := r.URL.Query().Get("folder")
	searchQuery := r.URL.Query().Get("search")
	pageStr := r.URL.Query().Get("page")
	perPageStr := r.URL.Query().Get("per_page")
	include := r.URL.Query().Get("include")

	// Pagination defaults
	page := 1
	perPage := 20
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if perPageStr != "" {
		if pp, err := strconv.Atoi(perPageStr); err == nil && pp > 0 && pp <= 100 {
			perPage = pp
		}
	}
	offset := (page - 1) * perPage

	var media []store.Medium
	var total int64
	var err error

	// Apply filters
	if searchQuery != "" {
		// Search by filename or alt text
		searchPattern := "%" + searchQuery + "%"
		media, err = h.queries.SearchMedia(ctx, store.SearchMediaParams{
			Filename: searchPattern,
			Alt:      sql.NullString{String: searchPattern, Valid: true},
			Limit:    int64(perPage),
		})
		if err == nil {
			// For search, we don't have a count query, so use the results length
			total = int64(len(media))
		}
	} else if folderIDStr != "" {
		folderID, parseErr := strconv.ParseInt(folderIDStr, 10, 64)
		if parseErr != nil {
			WriteBadRequest(w, "Invalid folder ID", nil)
			return
		}
		media, err = h.queries.ListMediaInFolder(ctx, store.ListMediaInFolderParams{
			FolderID: sql.NullInt64{Int64: folderID, Valid: true},
			Limit:    int64(perPage),
			Offset:   int64(offset),
		})
		if err == nil {
			total, err = h.queries.CountMediaInFolder(ctx, sql.NullInt64{Int64: folderID, Valid: true})
		}
	} else if typeFilter != "" {
		// Filter by type (image, document, video)
		var mimePattern string
		switch typeFilter {
		case "image":
			mimePattern = "image/%"
		case "document":
			mimePattern = "application/pdf"
		case "video":
			mimePattern = "video/%"
		default:
			WriteBadRequest(w, "Invalid type filter. Use: image, document, or video", nil)
			return
		}
		media, err = h.queries.ListMediaByType(ctx, store.ListMediaByTypeParams{
			MimeType: mimePattern,
			Limit:    int64(perPage),
			Offset:   int64(offset),
		})
		if err == nil {
			total, err = h.queries.CountMediaByType(ctx, mimePattern)
		}
	} else {
		// List all media
		media, err = h.queries.ListMedia(ctx, store.ListMediaParams{
			Limit:  int64(perPage),
			Offset: int64(offset),
		})
		if err == nil {
			total, err = h.queries.CountMedia(ctx)
		}
	}

	if err != nil {
		WriteInternalError(w, "Failed to list media")
		return
	}

	// Parse includes
	includeVariants := false
	includeFolder := false
	if include != "" {
		includes := strings.Split(include, ",")
		for _, inc := range includes {
			switch strings.TrimSpace(inc) {
			case "variants":
				includeVariants = true
			case "folder":
				includeFolder = true
			}
		}
	}

	// Convert to response
	responses := make([]MediaResponse, 0, len(media))
	for _, m := range media {
		resp := storeMediaToResponse(m)
		h.populateMediaIncludes(ctx, &resp, m.ID, includeVariants, includeFolder)
		responses = append(responses, resp)
	}

	// Calculate total pages
	totalPages := int(total) / perPage
	if int(total)%perPage != 0 {
		totalPages++
	}

	WriteSuccess(w, responses, &Meta{
		Total:   total,
		Page:    page,
		PerPage: perPage,
		Pages:   totalPages,
	})
}

// GetMedia handles GET /api/v1/media/{id}
func (h *Handler) GetMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	include := r.URL.Query().Get("include")

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		WriteBadRequest(w, "Invalid media ID", nil)
		return
	}

	media, err := h.queries.GetMediaByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteNotFound(w, "Media not found")
		} else {
			WriteInternalError(w, "Failed to retrieve media")
		}
		return
	}

	resp := storeMediaToResponse(media)

	// Parse includes
	includeVariants := false
	includeFolder := false
	if include != "" {
		includes := strings.Split(include, ",")
		for _, inc := range includes {
			switch strings.TrimSpace(inc) {
			case "variants":
				includeVariants = true
			case "folder":
				includeFolder = true
			}
		}
	}

	h.populateMediaIncludes(ctx, &resp, media.ID, includeVariants, includeFolder)

	WriteSuccess(w, resp, nil)
}

// UploadMedia handles POST /api/v1/media
// Requires media:write permission
// Accepts multipart/form-data with file(s)
func (h *Handler) UploadMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse multipart form (20MB max)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		WriteBadRequest(w, "Failed to parse multipart form", nil)
		return
	}

	// Get API key to determine uploader
	apiKey := middleware.GetAPIKey(r)
	if apiKey == nil {
		WriteUnauthorized(w, "API key required")
		return
	}

	// Get optional folder_id from form
	var folderID *int64
	if folderIDStr := r.FormValue("folder_id"); folderIDStr != "" {
		if fid, err := strconv.ParseInt(folderIDStr, 10, 64); err == nil {
			folderID = &fid
		}
	}

	// Create media service
	mediaService := service.NewMediaService(h.db, "./uploads")

	// Handle file(s) - support both single "file" and multiple "files[]"
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		files = r.MultipartForm.File["files[]"]
	}
	if len(files) == 0 {
		// Try single file upload
		files = r.MultipartForm.File["files"]
	}
	if len(files) == 0 {
		WriteBadRequest(w, "No file provided. Use 'file', 'files', or 'files[]' field", nil)
		return
	}

	// Process uploads
	var responses []MediaResponse
	var errors []map[string]string

	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			errors = append(errors, map[string]string{
				"filename": fileHeader.Filename,
				"error":    "Failed to open file",
			})
			continue
		}

		result, err := mediaService.Upload(ctx, file, fileHeader, apiKey.CreatedBy, folderID)
		_ = file.Close()

		if err != nil {
			errors = append(errors, map[string]string{
				"filename": fileHeader.Filename,
				"error":    err.Error(),
			})
			continue
		}

		resp := storeMediaToResponse(result.Media)

		// Add variants to response
		if len(result.Variants) > 0 {
			resp.Variants = make([]VariantResponse, 0, len(result.Variants))
			for _, v := range result.Variants {
				resp.Variants = append(resp.Variants, VariantResponse{
					ID:        v.ID,
					Type:      v.Type,
					Width:     v.Width,
					Height:    v.Height,
					Size:      v.Size,
					URL:       "/uploads/" + v.Type + "/" + result.Media.Uuid + "/" + result.Media.Filename,
					CreatedAt: v.CreatedAt,
				})
			}
		}

		responses = append(responses, resp)
	}

	// If all uploads failed, return error
	if len(responses) == 0 && len(errors) > 0 {
		WriteError(w, http.StatusBadRequest, "upload_failed", "All uploads failed", nil)
		return
	}

	// Return response
	if len(files) == 1 {
		// Single file upload - return the media directly
		if len(responses) > 0 {
			WriteCreated(w, responses[0])
		} else {
			WriteError(w, http.StatusBadRequest, "upload_failed", errors[0]["error"], nil)
		}
	} else {
		// Multiple file upload - return array with any errors
		type BatchUploadResponse struct {
			Uploaded []MediaResponse     `json:"uploaded"`
			Errors   []map[string]string `json:"errors,omitempty"`
		}
		WriteCreated(w, BatchUploadResponse{
			Uploaded: responses,
			Errors:   errors,
		})
	}
}

// UpdateMedia handles PUT /api/v1/media/{id}
// Requires media:write permission
func (h *Handler) UpdateMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		WriteBadRequest(w, "Invalid media ID", nil)
		return
	}

	// Get existing media
	existing, err := h.queries.GetMediaByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteNotFound(w, "Media not found")
		} else {
			WriteInternalError(w, "Failed to retrieve media")
		}
		return
	}

	var req UpdateMediaRequest
	if err := decodeJSON(r, &req); err != nil {
		WriteBadRequest(w, "Invalid JSON body", nil)
		return
	}

	// Build update params, starting with existing values
	params := store.UpdateMediaParams{
		ID:        id,
		Filename:  existing.Filename,
		Alt:       existing.Alt,
		Caption:   existing.Caption,
		FolderID:  existing.FolderID,
		UpdatedAt: time.Now(),
	}

	// Apply updates
	if req.Filename != nil && *req.Filename != "" {
		params.Filename = *req.Filename
	}
	if req.Alt != nil {
		params.Alt = sql.NullString{String: *req.Alt, Valid: true}
	}
	if req.Caption != nil {
		params.Caption = sql.NullString{String: *req.Caption, Valid: true}
	}
	if req.FolderID != nil {
		if *req.FolderID == 0 {
			params.FolderID = sql.NullInt64{Valid: false}
		} else {
			params.FolderID = sql.NullInt64{Int64: *req.FolderID, Valid: true}
		}
	}

	// Update media
	media, err := h.queries.UpdateMedia(ctx, params)
	if err != nil {
		WriteInternalError(w, "Failed to update media")
		return
	}

	resp := storeMediaToResponse(media)

	// Include variants in response
	variants, _ := h.queries.GetMediaVariants(ctx, media.ID)
	if len(variants) > 0 {
		resp.Variants = make([]VariantResponse, 0, len(variants))
		for _, v := range variants {
			resp.Variants = append(resp.Variants, VariantResponse{
				ID:        v.ID,
				Type:      v.Type,
				Width:     v.Width,
				Height:    v.Height,
				Size:      v.Size,
				URL:       "/uploads/" + v.Type + "/" + media.Uuid + "/" + media.Filename,
				CreatedAt: v.CreatedAt,
			})
		}
	}

	WriteSuccess(w, resp, nil)
}

// DeleteMedia handles DELETE /api/v1/media/{id}
// Requires media:write permission
func (h *Handler) DeleteMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		WriteBadRequest(w, "Invalid media ID", nil)
		return
	}

	// Check if media exists
	_, err = h.queries.GetMediaByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteNotFound(w, "Media not found")
		} else {
			WriteInternalError(w, "Failed to retrieve media")
		}
		return
	}

	// Create media service and delete
	mediaService := service.NewMediaService(h.db, "./uploads")
	if err := mediaService.Delete(ctx, id); err != nil {
		WriteInternalError(w, "Failed to delete media")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// populateMediaIncludes adds related data to a media response.
func (h *Handler) populateMediaIncludes(ctx context.Context, resp *MediaResponse, mediaID int64, includeVariants, includeFolder bool) {
	if includeVariants {
		variants, err := h.queries.GetMediaVariants(ctx, mediaID)
		if err == nil && len(variants) > 0 {
			resp.Variants = make([]VariantResponse, 0, len(variants))
			for _, v := range variants {
				resp.Variants = append(resp.Variants, VariantResponse{
					ID:        v.ID,
					Type:      v.Type,
					Width:     v.Width,
					Height:    v.Height,
					Size:      v.Size,
					URL:       "/uploads/" + v.Type + "/" + resp.UUID + "/" + resp.Filename,
					CreatedAt: v.CreatedAt,
				})
			}
		}
	}

	if includeFolder && resp.FolderID != nil {
		folder, err := h.queries.GetMediaFolderByID(ctx, *resp.FolderID)
		if err == nil {
			folderResp := &FolderResponse{
				ID:        folder.ID,
				Name:      folder.Name,
				Position:  folder.Position,
				CreatedAt: folder.CreatedAt,
			}
			if folder.ParentID.Valid {
				folderResp.ParentID = &folder.ParentID.Int64
			}
			resp.Folder = folderResp
		}
	}
}

// decodeJSON decodes JSON from request body.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
