package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/service"
	"ocms-go/internal/store"
	"ocms-go/internal/webhook"
)

// MediaPerPage is the number of media items to display per page.
const MediaPerPage = 24

// MediaHandler handles media library routes.
type MediaHandler struct {
	db             *sql.DB
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	mediaService   *service.MediaService
	uploadDir      string
	dispatcher     *webhook.Dispatcher
}

// NewMediaHandler creates a new MediaHandler.
func NewMediaHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, uploadDir string) *MediaHandler {
	if uploadDir == "" {
		uploadDir = "./uploads"
	}
	return &MediaHandler{
		db:             db,
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		mediaService:   service.NewMediaService(db, uploadDir),
		uploadDir:      uploadDir,
	}
}

// SetDispatcher sets the webhook dispatcher for event dispatching.
func (h *MediaHandler) SetDispatcher(d *webhook.Dispatcher) {
	h.dispatcher = d
}

// dispatchMediaEvent dispatches a media-related webhook event.
func (h *MediaHandler) dispatchMediaEvent(ctx context.Context, eventType string, media store.Medium) {
	if h.dispatcher == nil {
		return
	}

	data := webhook.MediaEventData{
		ID:         media.ID,
		UUID:       media.Uuid,
		Filename:   media.Filename,
		MimeType:   media.MimeType,
		Size:       media.Size,
		UploaderID: media.UploadedBy,
	}

	if err := h.dispatcher.DispatchEvent(ctx, eventType, data); err != nil {
		slog.Error("failed to dispatch webhook event",
			"error", err,
			"event_type", eventType,
			"media_id", media.ID)
	}
}

// MediaItem represents a media item with additional computed fields.
type MediaItem struct {
	store.Medium
	ThumbnailURL string
	OriginalURL  string
	IsImage      bool
	TypeIcon     string
}

// MediaLibraryData holds data for the media library template.
type MediaLibraryData struct {
	Media      []MediaItem
	Folders    []store.MediaFolder
	TotalCount int64
	Filter     string // images, documents, videos, all
	FolderID   *int64
	Search     string
	Pagination AdminPagination
}

// Library handles GET /admin/media - displays the media library.
func (h *MediaHandler) Library(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get page number from query string
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get filter
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "all"
	}

	// Get folder ID
	var folderID *int64
	folderIDStr := r.URL.Query().Get("folder")
	if folderIDStr != "" {
		if fid, err := strconv.ParseInt(folderIDStr, 10, 64); err == nil {
			folderID = &fid
		}
	}

	// Get search query
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	// Get total count
	totalCount, err := h.queries.CountMedia(r.Context())
	if err != nil {
		slog.Error("failed to count media", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Calculate pagination
	totalPages := int((totalCount + MediaPerPage - 1) / MediaPerPage)
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := int64((page - 1) * MediaPerPage)

	// Fetch media
	var mediaList []store.Medium
	if search != "" {
		mediaList, err = h.queries.SearchMedia(r.Context(), store.SearchMediaParams{
			Filename: "%" + search + "%",
			Alt:      sql.NullString{String: "%" + search + "%", Valid: true},
			Limit:    MediaPerPage,
		})
	} else if filter != "all" {
		var mimePattern string
		switch filter {
		case "images":
			mimePattern = "image/%"
		case "documents":
			mimePattern = "application/%"
		case "videos":
			mimePattern = "video/%"
		default:
			mimePattern = "%"
		}
		mediaList, err = h.queries.ListMediaByType(r.Context(), store.ListMediaByTypeParams{
			MimeType: mimePattern,
			Limit:    MediaPerPage,
			Offset:   offset,
		})
	} else if folderID != nil {
		mediaList, err = h.queries.ListMediaInFolder(r.Context(), store.ListMediaInFolderParams{
			FolderID: sql.NullInt64{Int64: *folderID, Valid: true},
			Limit:    MediaPerPage,
			Offset:   offset,
		})
	} else {
		mediaList, err = h.queries.ListMedia(r.Context(), store.ListMediaParams{
			Limit:  MediaPerPage,
			Offset: offset,
		})
	}
	if err != nil {
		slog.Error("failed to list media", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get folders
	folders, err := h.queries.ListMediaFolders(r.Context())
	if err != nil {
		slog.Error("failed to list folders", "error", err)
		folders = []store.MediaFolder{}
	}

	// Build media items with computed fields
	mediaItems := make([]MediaItem, len(mediaList))
	for i, m := range mediaList {
		mediaItems[i] = MediaItem{
			Medium:       m,
			ThumbnailURL: h.mediaService.GetThumbnailURL(m),
			OriginalURL:  h.mediaService.GetURL(m, "original"),
			IsImage:      isImageMime(m.MimeType),
			TypeIcon:     getTypeIcon(m.MimeType),
		}
	}

	data := MediaLibraryData{
		Media:      mediaItems,
		Folders:    folders,
		TotalCount: totalCount,
		Filter:     filter,
		FolderID:   folderID,
		Search:     search,
		Pagination: BuildAdminPagination(page, int(totalCount), MediaPerPage, "/admin/media", r.URL.Query()),
	}

	if err := h.renderer.Render(w, r, "admin/media_library", render.TemplateData{
		Title: i18n.T(lang, "media.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.media"), URL: "/admin/media", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// UploadFormData holds data for the upload form template.
type UploadFormData struct {
	Folders    []store.MediaFolder
	MaxSize    int64
	AllowedExt string
}

// UploadForm handles GET /admin/media/upload - displays the upload form.
func (h *MediaHandler) UploadForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get folders
	folders, err := h.queries.ListMediaFolders(r.Context())
	if err != nil {
		slog.Error("failed to list folders", "error", err)
		folders = []store.MediaFolder{}
	}

	data := UploadFormData{
		Folders:    folders,
		MaxSize:    service.MaxUploadSize,
		AllowedExt: ".jpg,.jpeg,.png,.gif,.webp,.pdf,.mp4,.webm",
	}

	if err := h.renderer.Render(w, r, "admin/media_upload", render.TemplateData{
		Title: i18n.T(lang, "media.upload_title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.media"), URL: "/admin/media"},
			{Label: i18n.T(lang, "media.upload"), URL: "/admin/media/upload", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Upload handles POST /admin/media/upload - processes file upload.
func (h *MediaHandler) Upload(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)

	// Parse multipart form with max memory
	if err := r.ParseMultipartForm(service.MaxUploadSize); err != nil {
		slog.Error("failed to parse multipart form", "error", err)
		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("File too large or invalid form"))
			return
		}
		h.renderer.SetFlash(r, "Error parsing upload", "error")
		http.Redirect(w, r, "/admin/media/upload", http.StatusSeeOther)
		return
	}

	// Get folder ID
	var folderID *int64
	folderIDStr := r.FormValue("folder_id")
	if folderIDStr != "" && folderIDStr != "0" {
		if fid, err := strconv.ParseInt(folderIDStr, 10, 64); err == nil {
			folderID = &fid
		}
	}

	// Get uploaded files
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		// Try single file field
		file, header, err := r.FormFile("file")
		if err != nil {
			if r.Header.Get("HX-Request") == "true" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("No file uploaded"))
				return
			}
			h.renderer.SetFlash(r, "No file uploaded", "error")
			http.Redirect(w, r, "/admin/media/upload", http.StatusSeeOther)
			return
		}
		defer func() { _ = file.Close() }()

		// Process single file
		result, err := h.mediaService.Upload(r.Context(), file, header, userID, folderID)
		if err != nil {
			slog.Error("failed to upload file", "error", err, "filename", header.Filename)
			if r.Header.Get("HX-Request") == "true" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
			h.renderer.SetFlash(r, "Upload failed: "+err.Error(), "error")
			http.Redirect(w, r, "/admin/media/upload", http.StatusSeeOther)
			return
		}

		slog.Info("media uploaded", "media_id", result.Media.ID, "filename", result.Media.Filename, "uploaded_by", middleware.GetUserID(r))

		// Dispatch media.uploaded webhook event
		h.dispatchMediaEvent(r.Context(), model.EventMediaUploaded, result.Media)

		if r.Header.Get("HX-Request") == "true" {
			// Return success HTML for HTMX
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf(`<div class="upload-success">Uploaded: %s</div>`, result.Media.Filename)))
			return
		}

		h.renderer.SetFlash(r, "File uploaded successfully", "success")
		http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
		return
	}

	// Process multiple files
	var uploadedCount int
	var errorCount int
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			slog.Error("failed to open file", "error", err, "filename", header.Filename)
			errorCount++
			continue
		}

		result, err := h.mediaService.Upload(r.Context(), file, header, userID, folderID)
		_ = file.Close()

		if err != nil {
			slog.Error("failed to upload file", "error", err, "filename", header.Filename)
			errorCount++
			continue
		}

		// Dispatch media.uploaded webhook event for each file
		h.dispatchMediaEvent(r.Context(), model.EventMediaUploaded, result.Media)

		uploadedCount++
	}

	slog.Info("batch upload complete", "uploaded", uploadedCount, "errors", errorCount, "uploaded_by", middleware.GetUserID(r))

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf(`<div class="upload-success">Uploaded %d files</div>`, uploadedCount)))
		return
	}

	if errorCount > 0 {
		h.renderer.SetFlash(r, fmt.Sprintf("Uploaded %d files, %d failed", uploadedCount, errorCount), "warning")
	} else {
		h.renderer.SetFlash(r, fmt.Sprintf("Uploaded %d files successfully", uploadedCount), "success")
	}
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

// MediaEditData holds data for the media edit template.
type MediaEditData struct {
	Media      MediaItem
	Variants   []store.MediaVariant
	Folders    []store.MediaFolder
	Errors     map[string]string
	FormValues map[string]string
}

// EditForm handles GET /admin/media/{id} - displays the edit form.
func (h *MediaHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get media ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid media ID", "error")
		http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
		return
	}

	// Get media from database
	media, err := h.queries.GetMediaByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderer.SetFlash(r, "Media not found", "error")
		} else {
			slog.Error("failed to get media", "error", err, "media_id", id)
			h.renderer.SetFlash(r, "Error loading media", "error")
		}
		http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
		return
	}

	// Get variants
	variants, err := h.queries.GetMediaVariants(r.Context(), id)
	if err != nil {
		slog.Error("failed to get variants", "error", err, "media_id", id)
		variants = []store.MediaVariant{}
	}

	// Get folders
	folders, err := h.queries.ListMediaFolders(r.Context())
	if err != nil {
		slog.Error("failed to list folders", "error", err)
		folders = []store.MediaFolder{}
	}

	mediaItem := MediaItem{
		Medium:       media,
		ThumbnailURL: h.mediaService.GetThumbnailURL(media),
		OriginalURL:  h.mediaService.GetURL(media, "original"),
		IsImage:      isImageMime(media.MimeType),
		TypeIcon:     getTypeIcon(media.MimeType),
	}

	data := MediaEditData{
		Media:      mediaItem,
		Variants:   variants,
		Folders:    folders,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
	}

	if err := h.renderer.Render(w, r, "admin/media_edit", render.TemplateData{
		Title: i18n.T(lang, "media.edit_title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.media"), URL: "/admin/media"},
			{Label: media.Filename, URL: fmt.Sprintf("/admin/media/%d", media.ID), Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Update handles PUT /admin/media/{id} - updates media metadata.
func (h *MediaHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get media ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid media ID", "error")
		http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
		return
	}

	// Get existing media
	media, err := h.queries.GetMediaByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderer.SetFlash(r, "Media not found", "error")
		} else {
			slog.Error("failed to get media", "error", err, "media_id", id)
			h.renderer.SetFlash(r, "Error loading media", "error")
		}
		http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/media/%d", id), http.StatusSeeOther)
		return
	}

	// Get form values
	filename := strings.TrimSpace(r.FormValue("filename"))
	alt := strings.TrimSpace(r.FormValue("alt"))
	caption := strings.TrimSpace(r.FormValue("caption"))
	folderIDStr := r.FormValue("folder_id")

	// Parse folder ID
	var folderID sql.NullInt64
	if folderIDStr != "" && folderIDStr != "0" {
		if fid, err := strconv.ParseInt(folderIDStr, 10, 64); err == nil {
			folderID = sql.NullInt64{Int64: fid, Valid: true}
		}
	}

	// Validate
	validationErrors := make(map[string]string)
	if filename == "" {
		validationErrors["filename"] = "Filename is required"
	}

	if len(validationErrors) > 0 {
		// Get folders for re-render
		folders, _ := h.queries.ListMediaFolders(r.Context())
		variants, _ := h.queries.GetMediaVariants(r.Context(), id)

		mediaItem := MediaItem{
			Medium:       media,
			ThumbnailURL: h.mediaService.GetThumbnailURL(media),
			OriginalURL:  h.mediaService.GetURL(media, "original"),
			IsImage:      isImageMime(media.MimeType),
			TypeIcon:     getTypeIcon(media.MimeType),
		}

		data := MediaEditData{
			Media:    mediaItem,
			Variants: variants,
			Folders:  folders,
			Errors:   validationErrors,
			FormValues: map[string]string{
				"filename":  filename,
				"alt":       alt,
				"caption":   caption,
				"folder_id": folderIDStr,
			},
		}

		if err := h.renderer.Render(w, r, "admin/media_edit", render.TemplateData{
			Title: i18n.T(lang, "media.edit_title"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "nav.media"), URL: "/admin/media"},
				{Label: media.Filename, URL: fmt.Sprintf("/admin/media/%d", id), Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Update media
	now := time.Now()
	_, err = h.queries.UpdateMedia(r.Context(), store.UpdateMediaParams{
		ID:        id,
		Filename:  filename,
		Alt:       sql.NullString{String: alt, Valid: alt != ""},
		Caption:   sql.NullString{String: caption, Valid: caption != ""},
		FolderID:  folderID,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to update media", "error", err, "media_id", id)
		h.renderer.SetFlash(r, "Error updating media", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/media/%d", id), http.StatusSeeOther)
		return
	}

	slog.Info("media updated", "media_id", id, "updated_by", middleware.GetUserID(r))
	h.renderer.SetFlash(r, "Media updated successfully", "success")
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

// Delete handles DELETE /admin/media/{id} - deletes media and files.
func (h *MediaHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Get media ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid media ID", http.StatusBadRequest)
		return
	}

	// Get media to verify it exists
	media, err := h.queries.GetMediaByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Media not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get media", "error", err, "media_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Delete media (files and DB records)
	err = h.mediaService.Delete(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete media", "error", err, "media_id", id)
		http.Error(w, "Error deleting media", http.StatusInternalServerError)
		return
	}

	slog.Info("media deleted", "media_id", id, "filename", media.Filename, "deleted_by", middleware.GetUserID(r))

	// Dispatch media.deleted webhook event
	h.dispatchMediaEvent(r.Context(), model.EventMediaDeleted, media)

	// For HTMX requests, return empty response
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For regular requests, redirect
	h.renderer.SetFlash(r, "Media deleted successfully", "success")
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

// CreateFolder handles POST /admin/media/folders - creates a new folder.
func (h *MediaHandler) CreateFolder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Folder name is required", http.StatusBadRequest)
		return
	}

	// Parse parent ID
	var parentID sql.NullInt64
	parentIDStr := r.FormValue("parent_id")
	if parentIDStr != "" && parentIDStr != "0" {
		if pid, err := strconv.ParseInt(parentIDStr, 10, 64); err == nil {
			parentID = sql.NullInt64{Int64: pid, Valid: true}
		}
	}

	// Get position (count of siblings + 1)
	var position int64
	if parentID.Valid {
		siblings, _ := h.queries.ListChildMediaFolders(r.Context(), parentID)
		position = int64(len(siblings))
	} else {
		siblings, _ := h.queries.ListRootMediaFolders(r.Context())
		position = int64(len(siblings))
	}

	// Create folder
	folder, err := h.queries.CreateMediaFolder(r.Context(), store.CreateMediaFolderParams{
		Name:      name,
		ParentID:  parentID,
		Position:  position,
		CreatedAt: time.Now(),
	})
	if err != nil {
		slog.Error("failed to create folder", "error", err)
		http.Error(w, "Error creating folder", http.StatusInternalServerError)
		return
	}

	slog.Info("folder created", "folder_id", folder.ID, "name", folder.Name, "created_by", middleware.GetUserID(r))

	// For HTMX requests, return the new folder item HTML
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "folderCreated")
		w.WriteHeader(http.StatusOK)
		// Return folder list item HTML
		html := fmt.Sprintf(`<li class="folder-item" data-folder-id="%d">
			<a href="/admin/media?folder=%d" class="folder-link">
				<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>
				<span class="folder-name">%s</span>
				<span class="folder-count">0</span>
			</a>
		</li>`, folder.ID, folder.ID, folder.Name)
		_, _ = w.Write([]byte(html))
		return
	}

	h.renderer.SetFlash(r, "Folder created successfully", "success")
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

// UpdateFolder handles PUT /admin/media/folders/{id} - renames or moves a folder.
func (h *MediaHandler) UpdateFolder(w http.ResponseWriter, r *http.Request) {
	// Get folder ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}

	// Get existing folder
	folder, err := h.queries.GetMediaFolderByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Folder not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get folder", "error", err, "folder_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// Get form values (use existing values as defaults)
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = folder.Name
	}

	// Parse parent ID
	parentID := folder.ParentID
	if parentIDStr := r.FormValue("parent_id"); parentIDStr != "" {
		if parentIDStr == "0" {
			parentID = sql.NullInt64{Valid: false}
		} else if pid, err := strconv.ParseInt(parentIDStr, 10, 64); err == nil {
			// Prevent setting parent to self
			if pid == id {
				http.Error(w, "Cannot move folder into itself", http.StatusBadRequest)
				return
			}
			parentID = sql.NullInt64{Int64: pid, Valid: true}
		}
	}

	// Parse position (keep existing if not provided)
	position := folder.Position
	if posStr := r.FormValue("position"); posStr != "" {
		if pos, err := strconv.ParseInt(posStr, 10, 64); err == nil {
			position = pos
		}
	}

	// Update folder
	_, err = h.queries.UpdateMediaFolder(r.Context(), store.UpdateMediaFolderParams{
		ID:       id,
		Name:     name,
		ParentID: parentID,
		Position: position,
	})
	if err != nil {
		slog.Error("failed to update folder", "error", err, "folder_id", id)
		http.Error(w, "Error updating folder", http.StatusInternalServerError)
		return
	}

	slog.Info("folder updated", "folder_id", id, "name", name, "updated_by", middleware.GetUserID(r))

	// For HTMX requests, return success
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "folderUpdated")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(name))
		return
	}

	h.renderer.SetFlash(r, "Folder updated successfully", "success")
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

// DeleteFolder handles DELETE /admin/media/folders/{id} - deletes a folder.
func (h *MediaHandler) DeleteFolder(w http.ResponseWriter, r *http.Request) {
	// Get folder ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}

	// Check if folder exists
	folder, err := h.queries.GetMediaFolderByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Folder not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get folder", "error", err, "folder_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Check for child folders
	children, err := h.queries.ListChildMediaFolders(r.Context(), sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		slog.Error("failed to check child folders", "error", err, "folder_id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if len(children) > 0 {
		http.Error(w, "Cannot delete folder with subfolders", http.StatusBadRequest)
		return
	}

	// Check for media in folder
	mediaCount, err := h.queries.CountMediaInFolder(r.Context(), sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		slog.Error("failed to count media in folder", "error", err, "folder_id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if mediaCount > 0 {
		http.Error(w, "Cannot delete folder with media. Move or delete the media first.", http.StatusBadRequest)
		return
	}

	// Delete folder
	err = h.queries.DeleteMediaFolder(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete folder", "error", err, "folder_id", id)
		http.Error(w, "Error deleting folder", http.StatusInternalServerError)
		return
	}

	slog.Info("folder deleted", "folder_id", id, "name", folder.Name, "deleted_by", middleware.GetUserID(r))

	// For HTMX requests, return empty response
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "folderDeleted")
		w.WriteHeader(http.StatusOK)
		return
	}

	h.renderer.SetFlash(r, "Folder deleted successfully", "success")
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

// MoveMedia handles POST /admin/media/{id}/move - moves media to a different folder.
func (h *MediaHandler) MoveMedia(w http.ResponseWriter, r *http.Request) {
	// Get media ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid media ID", http.StatusBadRequest)
		return
	}

	// Verify media exists
	_, err = h.queries.GetMediaByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Media not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get media", "error", err, "media_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// Parse folder ID (0 or empty means root/uncategorized)
	var folderID sql.NullInt64
	folderIDStr := r.FormValue("folder_id")
	if folderIDStr != "" && folderIDStr != "0" {
		if fid, err := strconv.ParseInt(folderIDStr, 10, 64); err == nil {
			// Verify folder exists
			_, err := h.queries.GetMediaFolderByID(r.Context(), fid)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.Error(w, "Folder not found", http.StatusNotFound)
				} else {
					slog.Error("failed to get folder", "error", err, "folder_id", fid)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
				return
			}
			folderID = sql.NullInt64{Int64: fid, Valid: true}
		}
	}

	// Move media to folder
	err = h.queries.MoveMediaToFolder(r.Context(), store.MoveMediaToFolderParams{
		ID:        id,
		FolderID:  folderID,
		UpdatedAt: time.Now(),
	})
	if err != nil {
		slog.Error("failed to move media", "error", err, "media_id", id)
		http.Error(w, "Error moving media", http.StatusInternalServerError)
		return
	}

	slog.Info("media moved", "media_id", id, "folder_id", folderID, "moved_by", middleware.GetUserID(r))

	// For HTMX requests, return success
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "mediaMoved")
		w.WriteHeader(http.StatusOK)
		return
	}

	h.renderer.SetFlash(r, "Media moved successfully", "success")
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

// API handles GET /admin/media/api - returns media items as JSON for the media picker.
func (h *MediaHandler) API(w http.ResponseWriter, r *http.Request) {
	// Get page number
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get limit
	limitStr := r.URL.Query().Get("limit")
	limit := 12
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 50 {
			limit = l
		}
	}

	// Get type filter (image or document)
	typeFilter := r.URL.Query().Get("type")

	// Get search query
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	// Get total count and items based on filters
	var totalCount int64
	var mediaList []store.Medium
	var err error

	offset := int64((page - 1) * limit)

	if search != "" {
		mediaList, err = h.queries.SearchMedia(r.Context(), store.SearchMediaParams{
			Filename: "%" + search + "%",
			Alt:      sql.NullString{String: "%" + search + "%", Valid: true},
			Limit:    int64(limit),
		})
		// For search, total is approximated by result count
		totalCount = int64(len(mediaList))
	} else if typeFilter == "image" {
		totalCount, err = h.queries.CountMediaByType(r.Context(), "image/%")
		if err == nil {
			mediaList, err = h.queries.ListMediaByType(r.Context(), store.ListMediaByTypeParams{
				MimeType: "image/%",
				Limit:    int64(limit),
				Offset:   offset,
			})
		}
	} else if typeFilter == "document" {
		totalCount, err = h.queries.CountMediaByType(r.Context(), "application/%")
		if err == nil {
			mediaList, err = h.queries.ListMediaByType(r.Context(), store.ListMediaByTypeParams{
				MimeType: "application/%",
				Limit:    int64(limit),
				Offset:   offset,
			})
		}
	} else {
		totalCount, err = h.queries.CountMedia(r.Context())
		if err == nil {
			mediaList, err = h.queries.ListMedia(r.Context(), store.ListMediaParams{
				Limit:  int64(limit),
				Offset: offset,
			})
		}
	}

	if err != nil {
		slog.Error("failed to fetch media for API", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"Internal server error"}`))
		return
	}

	// Calculate total pages
	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))
	if totalPages < 1 {
		totalPages = 1
	}

	// Build response
	type MediaAPIItem struct {
		ID        int64  `json:"id"`
		Filename  string `json:"filename"`
		Filepath  string `json:"filepath"`
		Thumbnail string `json:"thumbnail,omitempty"`
		Mimetype  string `json:"mimetype"`
		Size      int64  `json:"size"`
	}

	type APIResponse struct {
		Items      []MediaAPIItem `json:"items"`
		TotalCount int64          `json:"totalCount"`
		TotalPages int            `json:"totalPages"`
		Page       int            `json:"page"`
	}

	items := make([]MediaAPIItem, len(mediaList))
	for i, m := range mediaList {
		item := MediaAPIItem{
			ID:       m.ID,
			Filename: m.Filename,
			Filepath: fmt.Sprintf("/uploads/originals/%s/%s", m.Uuid, m.Filename),
			Mimetype: m.MimeType,
			Size:     m.Size,
		}
		// Add thumbnail path for images
		if strings.HasPrefix(m.MimeType, "image/") {
			item.Thumbnail = fmt.Sprintf("/uploads/thumbnail/%s/%s", m.Uuid, m.Filename)
		}
		items[i] = item
	}

	response := APIResponse{
		Items:      items,
		TotalCount: totalCount,
		TotalPages: totalPages,
		Page:       page,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("failed to encode media API response", "error", err)
	}
}

// Helper functions

func isImageMime(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

func getTypeIcon(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case mimeType == model.MimeTypePDF:
		return "file-text"
	default:
		return "file"
	}
}
