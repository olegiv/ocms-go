// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	"github.com/olegiv/ocms-go/internal/webhook"
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
	eventService   *service.EventService
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
		eventService:   service.NewEventService(db),
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

	page := ParsePageParam(r)

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
		logAndInternalError(w, "failed to count media", "error", err)
		return
	}

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalCount), MediaPerPage)

	offset := int64((page - 1) * MediaPerPage)

	// Fetch media
	var mediaList []store.Medium
	switch {
	case search != "":
		mediaList, err = h.queries.SearchMedia(r.Context(), store.SearchMediaParams{
			Filename: "%" + search + "%",
			Alt:      util.NullStringFromValue("%" + search + "%"),
			Limit:    MediaPerPage,
		})
	case filter != "all":
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
	case folderID != nil:
		mediaList, err = h.queries.ListMediaInFolder(r.Context(), store.ListMediaInFolderParams{
			FolderID: util.NullInt64FromPtr(folderID),
			Limit:    MediaPerPage,
			Offset:   offset,
		})
	default:
		mediaList, err = h.queries.ListMedia(r.Context(), store.ListMediaParams{
			Limit:  MediaPerPage,
			Offset: offset,
		})
	}
	if err != nil {
		logAndInternalError(w, "failed to list media", "error", err)
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
			IsImage:      IsImageMime(m.MimeType),
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
		Pagination: BuildAdminPagination(page, int(totalCount), MediaPerPage, redirectAdminMedia, r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/media_library", render.TemplateData{
		Title: i18n.T(lang, "media.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.media"), URL: redirectAdminMedia, Active: true},
		},
	})
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
		AllowedExt: ".jpg,.jpeg,.png,.gif,.webp,.ico,.pdf,.mp4,.webm",
	}

	h.renderer.RenderPage(w, r, "admin/media_upload", render.TemplateData{
		Title: i18n.T(lang, "media.upload_title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.media"), URL: redirectAdminMedia},
			{Label: i18n.T(lang, "media.upload"), URL: redirectAdminMediaUpload, Active: true},
		},
	})
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
		flashError(w, r, h.renderer, redirectAdminMediaUpload, "Error parsing upload")
		return
	}

	// Get default language for media creation
	defaultLang, err := h.queries.GetDefaultLanguage(r.Context())
	if err != nil {
		slog.Error("failed to get default language", "error", err)
		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Error uploading file"))
			return
		}
		flashError(w, r, h.renderer, redirectAdminMediaUpload, "Error uploading file")
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
			flashError(w, r, h.renderer, redirectAdminMediaUpload, "No file uploaded")
			return
		}
		defer func() { _ = file.Close() }()

		// Process single file
		result, err := h.mediaService.Upload(r.Context(), file, header, userID, folderID, defaultLang.Code)
		if err != nil {
			slog.Error("failed to upload file", "error", err, "filename", header.Filename)
			if r.Header.Get("HX-Request") == "true" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
			flashError(w, r, h.renderer, redirectAdminMediaUpload, "Upload failed: "+err.Error())
			return
		}

		slog.Info("media uploaded", "media_id", result.Media.ID, "filename", result.Media.Filename, "uploaded_by", middleware.GetUserID(r))
		_ = h.eventService.LogMediaEvent(r.Context(), model.EventLevelInfo, "Media uploaded", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"media_id": result.Media.ID, "filename": result.Media.Filename})

		// Dispatch media.uploaded webhook event
		h.dispatchMediaEvent(r.Context(), model.EventMediaUploaded, result.Media)

		if r.Header.Get("HX-Request") == "true" {
			// Return success HTML for HTMX
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `<div class="upload-success">Uploaded: %s</div>`, result.Media.Filename)
			return
		}

		flashSuccess(w, r, h.renderer, redirectAdminMedia, "File uploaded successfully")
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

		result, err := h.mediaService.Upload(r.Context(), file, header, userID, folderID, defaultLang.Code)
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
		_, _ = fmt.Fprintf(w, `<div class="upload-success">Uploaded %d files</div>`, uploadedCount)
		return
	}

	if errorCount > 0 {
		flashAndRedirect(w, r, h.renderer, redirectAdminMedia, fmt.Sprintf("Uploaded %d files, %d failed", uploadedCount, errorCount), "warning")
	} else {
		flashSuccess(w, r, h.renderer, redirectAdminMedia, fmt.Sprintf("Uploaded %d files successfully", uploadedCount))
	}
}

// MediaTranslationData holds translation data for a language.
type MediaTranslationData struct {
	LanguageID   int64
	LanguageCode string
	LanguageName string
	NativeName   string
	Alt          string
	Caption      string
}

// MediaEditData holds data for the media edit template.
type MediaEditData struct {
	Media        MediaItem
	Variants     []store.MediaVariant
	Folders      []store.MediaFolder
	Languages    []store.Language                // All active languages (except default)
	Translations map[string]MediaTranslationData // Keyed by language code
	Errors       map[string]string
	FormValues   map[string]string
}

// EditForm handles GET /admin/media/{id} - displays the edit form.
func (h *MediaHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminMedia, "Invalid media ID")
		return
	}

	media, ok := h.requireMediaWithRedirect(w, r, id)
	if !ok {
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

	// Get all active languages (excluding default - that's in the media table)
	allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)
	var nonDefaultLanguages []store.Language
	for _, l := range allLanguages {
		if !l.IsDefault {
			nonDefaultLanguages = append(nonDefaultLanguages, l)
		}
	}

	// Get existing translations for this media
	translations := make(map[string]MediaTranslationData)
	if len(nonDefaultLanguages) > 0 {
		existingTranslations, err := h.queries.GetMediaTranslations(r.Context(), id)
		if err != nil {
			slog.Error("failed to get media translations", "error", err, "media_id", id)
		} else {
			for _, t := range existingTranslations {
				translations[t.LanguageCode] = MediaTranslationData{
					LanguageID:   t.LanguageID,
					LanguageCode: t.LanguageCode,
					LanguageName: t.LanguageName,
					Alt:          t.Alt,
					Caption:      t.Caption,
				}
			}
		}
	}

	mediaItem := MediaItem{
		Medium:       media,
		ThumbnailURL: h.mediaService.GetThumbnailURL(media),
		OriginalURL:  h.mediaService.GetURL(media, "original"),
		IsImage:      IsImageMime(media.MimeType),
		TypeIcon:     getTypeIcon(media.MimeType),
	}

	data := MediaEditData{
		Media:        mediaItem,
		Variants:     variants,
		Folders:      folders,
		Languages:    nonDefaultLanguages,
		Translations: translations,
		Errors:       make(map[string]string),
		FormValues:   make(map[string]string),
	}

	renderEntityEditPage(w, r, h.renderer, "admin/media_edit", i18n.T(lang, "media.edit_title"), data, lang, "nav.media", redirectAdminMedia, media.Filename, fmt.Sprintf(redirectAdminMediaID, media.ID))
}

// Update handles PUT /admin/media/{id} - updates media metadata.
func (h *MediaHandler) Update(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminMedia, "Invalid media ID")
		return
	}

	media, ok := h.requireMediaWithRedirect(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminMediaID, id)) {
		return
	}

	// Get form values
	filename := strings.TrimSpace(r.FormValue("filename"))
	alt := strings.TrimSpace(r.FormValue("alt"))
	caption := strings.TrimSpace(r.FormValue("caption"))
	folderIDStr := r.FormValue("folder_id")

	// Parse folder ID
	folderID := util.ParseNullInt64(folderIDStr)

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
			IsImage:      IsImageMime(media.MimeType),
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

		renderEntityEditPage(w, r, h.renderer, "admin/media_edit",
			i18n.T(lang, "media.edit_title"), data, lang,
			"nav.media", redirectAdminMedia,
			media.Filename, fmt.Sprintf(redirectAdminMediaID, id))
		return
	}

	// Update media
	now := time.Now()
	_, err = h.queries.UpdateMedia(r.Context(), store.UpdateMediaParams{
		ID:           id,
		Filename:     filename,
		Alt:          util.NullStringFromValue(alt),
		Caption:      util.NullStringFromValue(caption),
		FolderID:     folderID,
		LanguageCode: media.LanguageCode,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to update media", "error", err, "media_id", id)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminMediaID, id), "Error updating media")
		return
	}

	// Save translations for non-default languages
	allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)
	for _, l := range allLanguages {
		if l.IsDefault {
			continue // Default language values are in the media table
		}

		// Get translated alt/caption from form
		translatedAlt := strings.TrimSpace(r.FormValue("alt_" + l.Code))
		translatedCaption := strings.TrimSpace(r.FormValue("caption_" + l.Code))

		// If both are empty, delete translation if exists
		if translatedAlt == "" && translatedCaption == "" {
			if err := h.queries.DeleteMediaTranslation(r.Context(), store.DeleteMediaTranslationParams{
				MediaID:    id,
				LanguageID: l.ID,
			}); err != nil {
				slog.Error("failed to delete media translation", "error", err, "media_id", id, "language_id", l.ID)
			}
			continue
		}

		// Upsert translation
		if _, err := h.queries.UpsertMediaTranslation(r.Context(), store.UpsertMediaTranslationParams{
			MediaID:    id,
			LanguageID: l.ID,
			Alt:        translatedAlt,
			Caption:    translatedCaption,
		}); err != nil {
			slog.Error("failed to upsert media translation", "error", err, "media_id", id, "language_id", l.ID)
		}
	}

	slog.Info("media updated", "media_id", id, "updated_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminMedia, "Media updated successfully")
}

// Delete handles DELETE /admin/media/{id} - deletes media and files.
func (h *MediaHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid media ID", http.StatusBadRequest)
		return
	}

	media, ok := h.requireMediaWithError(w, r, id)
	if !ok {
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
	_ = h.eventService.LogMediaEvent(r.Context(), model.EventLevelInfo, "Media deleted", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"media_id": id, "filename": media.Filename})

	// Dispatch media.deleted webhook event
	h.dispatchMediaEvent(r.Context(), model.EventMediaDeleted, media)

	// For HTMX requests, return empty response
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For regular requests, redirect
	flashSuccess(w, r, h.renderer, redirectAdminMedia, "Media deleted successfully")
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
	parentIDStr := r.FormValue("parent_id")
	parentID := util.ParseNullInt64(parentIDStr)

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
		logAndHTTPError(w, "Error creating folder", http.StatusInternalServerError, "failed to create folder", "error", err)
		return
	}

	slog.Info("folder created", "folder_id", folder.ID, "name", folder.Name, "created_by", middleware.GetUserID(r))

	// For HTMX requests, return the new folder item HTML
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "folderCreated")
		w.WriteHeader(http.StatusOK)
		// Return folder list item HTML
		folderHTML := fmt.Sprintf(`<li class="folder-item" data-folder-id="%d">
			<a href="/admin/media?folder=%d" class="folder-link">
				<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>
				<span class="folder-name">%s</span>
				<span class="folder-count">0</span>
			</a>
		</li>`, folder.ID, folder.ID, folder.Name)
		_, _ = w.Write([]byte(folderHTML))
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminMedia, "Folder created successfully")
}

// UpdateFolder handles PUT /admin/media/folders/{id} - renames or moves a folder.
func (h *MediaHandler) UpdateFolder(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}

	folder, ok := h.requireFolderWithError(w, r, id)
	if !ok {
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
			parentID = sql.NullInt64{}
		} else if pid, err := strconv.ParseInt(parentIDStr, 10, 64); err == nil {
			// Prevent setting parent to self
			if pid == id {
				http.Error(w, "Cannot move folder into itself", http.StatusBadRequest)
				return
			}
			parentID = util.NullInt64FromValue(pid)
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
		logAndHTTPError(w, "Error updating folder", http.StatusInternalServerError, "failed to update folder", "error", err, "folder_id", id)
		return
	}

	slog.Info("folder updated", "folder_id", id, "name", name, "updated_by", middleware.GetUserID(r))

	// For HTMX requests, return success
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "folderUpdated")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html.EscapeString(name)))
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminMedia, "Folder updated successfully")
}

// DeleteFolder handles DELETE /admin/media/folders/{id} - deletes a folder.
func (h *MediaHandler) DeleteFolder(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}

	folder, ok := h.requireFolderWithError(w, r, id)
	if !ok {
		return
	}

	// Check for child folders
	children, err := h.queries.ListChildMediaFolders(r.Context(), util.NullInt64FromValue(id))
	if err != nil {
		logAndInternalError(w, "failed to check child folders", "error", err, "folder_id", id)
		return
	}
	if len(children) > 0 {
		http.Error(w, "Cannot delete folder with subfolders", http.StatusBadRequest)
		return
	}

	// Check for media in folder
	mediaCount, err := h.queries.CountMediaInFolder(r.Context(), util.NullInt64FromValue(id))
	if err != nil {
		logAndInternalError(w, "failed to count media in folder", "error", err, "folder_id", id)
		return
	}
	if mediaCount > 0 {
		http.Error(w, "Cannot delete folder with media. Move or delete the media first.", http.StatusBadRequest)
		return
	}

	// Delete folder
	err = h.queries.DeleteMediaFolder(r.Context(), id)
	if err != nil {
		logAndHTTPError(w, "Error deleting folder", http.StatusInternalServerError, "failed to delete folder", "error", err, "folder_id", id)
		return
	}

	slog.Info("folder deleted", "folder_id", id, "name", folder.Name, "deleted_by", middleware.GetUserID(r))

	// For HTMX requests, return empty response
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "folderDeleted")
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminMedia, "Folder deleted successfully")
}

// RegenerateVariants handles POST /admin/media/{id}/regenerate - regenerates image variants.
func (h *MediaHandler) RegenerateVariants(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid media ID", http.StatusBadRequest)
		return
	}

	media, ok := h.requireMediaWithError(w, r, id)
	if !ok {
		return
	}

	// Check if it's an image
	if !IsImageMime(media.MimeType) {
		http.Error(w, "Only images can have variants regenerated", http.StatusBadRequest)
		return
	}

	// Regenerate variants
	variants, err := h.mediaService.RegenerateVariants(r.Context(), id)
	if err != nil {
		slog.Error("failed to regenerate variants", "error", err, "media_id", id)
		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminMediaID, id), "Error regenerating variants: "+err.Error())
		return
	}

	slog.Info("variants regenerated", "media_id", id, "variants_count", len(variants), "regenerated_by", middleware.GetUserID(r))

	// For HTMX requests, return success message
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `<span class="text-success">%s</span>`, i18n.T(lang, "media.variants_regenerated"))
		return
	}

	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminMediaID, id), "Variants regenerated successfully")
}

// MoveMedia handles POST /admin/media/{id}/move - moves media to a different folder.
func (h *MediaHandler) MoveMedia(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid media ID", http.StatusBadRequest)
		return
	}

	if _, ok := h.requireMediaWithError(w, r, id); !ok {
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
			if _, ok := h.requireFolderWithError(w, r, fid); !ok {
				return
			}
			folderID = util.NullInt64FromValue(fid)
		}
	}

	// Move media to folder
	err = h.queries.MoveMediaToFolder(r.Context(), store.MoveMediaToFolderParams{
		ID:        id,
		FolderID:  folderID,
		UpdatedAt: time.Now(),
	})
	if err != nil {
		logAndHTTPError(w, "Error moving media", http.StatusInternalServerError, "failed to move media", "error", err, "media_id", id)
		return
	}

	slog.Info("media moved", "media_id", id, "folder_id", folderID, "moved_by", middleware.GetUserID(r))

	// For HTMX requests, return success
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "mediaMoved")
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminMedia, "Media moved successfully")
}

// API handles GET /admin/media/api - returns media items as JSON for the media picker.
func (h *MediaHandler) API(w http.ResponseWriter, r *http.Request) {
	page := ParsePageParam(r)

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
			Alt:      util.NullStringFromValue("%" + search + "%"),
			Limit:    int64(limit),
		})
		// For search, total is approximated by result count
		totalCount = int64(len(mediaList))
	} else if mimePattern := mimePatternForType(typeFilter); mimePattern != "" {
		mediaList, totalCount, err = ListAndCount(
			func() ([]store.Medium, error) {
				return h.queries.ListMediaByType(r.Context(), store.ListMediaByTypeParams{
					MimeType: mimePattern,
					Limit:    int64(limit),
					Offset:   offset,
				})
			},
			func() (int64, error) { return h.queries.CountMediaByType(r.Context(), mimePattern) },
		)
	} else {
		mediaList, totalCount, err = ListAndCount(
			func() ([]store.Medium, error) {
				return h.queries.ListMedia(r.Context(), store.ListMediaParams{
					Limit:  int64(limit),
					Offset: offset,
				})
			},
			func() (int64, error) { return h.queries.CountMedia(r.Context()) },
		)
	}

	if err != nil {
		slog.Error("failed to fetch media for API", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	// Calculate total pages
	totalPages := CalculateTotalPages(int(totalCount), limit)

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

// requireMediaWithRedirect fetches media by ID and handles errors with flash messages and redirect.
func (h *MediaHandler) requireMediaWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Medium, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, redirectAdminMedia, "Media", id,
		func(id int64) (store.Medium, error) { return h.queries.GetMediaByID(r.Context(), id) })
}

// requireMediaWithError fetches media by ID and handles errors with http.Error.
func (h *MediaHandler) requireMediaWithError(w http.ResponseWriter, r *http.Request, id int64) (store.Medium, bool) {
	return requireEntityWithError(w, "Media", id,
		func(id int64) (store.Medium, error) { return h.queries.GetMediaByID(r.Context(), id) })
}

// requireFolderWithError fetches folder by ID and handles errors with http.Error.
func (h *MediaHandler) requireFolderWithError(w http.ResponseWriter, r *http.Request, id int64) (store.MediaFolder, bool) {
	return requireEntityWithError(w, "Folder", id,
		func(id int64) (store.MediaFolder, error) { return h.queries.GetMediaFolderByID(r.Context(), id) })
}

// IsImageMime checks if the MIME type is an image.
func IsImageMime(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

// mimePatternForType returns the MIME type pattern for a media type filter.
func mimePatternForType(typeFilter string) string {
	switch typeFilter {
	case "image":
		return "image/%"
	case "document":
		return "application/%"
	default:
		return ""
	}
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
