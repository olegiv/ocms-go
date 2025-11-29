package handler

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/service"
	"ocms-go/internal/store"
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
	Media       []MediaItem
	Folders     []store.MediaFolder
	CurrentPage int
	TotalPages  int
	TotalCount  int64
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
	Filter      string // images, documents, videos, all
	FolderID    *int64
	Search      string
}

// Library handles GET /admin/media - displays the media library.
func (h *MediaHandler) Library(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

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
		Media:       mediaItems,
		Folders:     folders,
		CurrentPage: page,
		TotalPages:  totalPages,
		TotalCount:  totalCount,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
		Filter:      filter,
		FolderID:    folderID,
		Search:      search,
	}

	if err := h.renderer.Render(w, r, "admin/media_library", render.TemplateData{
		Title: "Media Library",
		User:  user,
		Data:  data,
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
		Title: "Upload Media",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Upload handles POST /admin/media/upload - processes file upload.
func (h *MediaHandler) Upload(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Parse multipart form with max memory
	if err := r.ParseMultipartForm(service.MaxUploadSize); err != nil {
		slog.Error("failed to parse multipart form", "error", err)
		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("File too large or invalid form"))
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
				w.Write([]byte("No file uploaded"))
				return
			}
			h.renderer.SetFlash(r, "No file uploaded", "error")
			http.Redirect(w, r, "/admin/media/upload", http.StatusSeeOther)
			return
		}
		defer file.Close()

		// Process single file
		result, err := h.mediaService.Upload(r.Context(), file, header, user.ID, folderID)
		if err != nil {
			slog.Error("failed to upload file", "error", err, "filename", header.Filename)
			if r.Header.Get("HX-Request") == "true" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(err.Error()))
				return
			}
			h.renderer.SetFlash(r, "Upload failed: "+err.Error(), "error")
			http.Redirect(w, r, "/admin/media/upload", http.StatusSeeOther)
			return
		}

		slog.Info("media uploaded", "media_id", result.Media.ID, "filename", result.Media.Filename, "uploaded_by", user.ID)

		if r.Header.Get("HX-Request") == "true" {
			// Return success HTML for HTMX
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf(`<div class="upload-success">Uploaded: %s</div>`, result.Media.Filename)))
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

		_, err = h.mediaService.Upload(r.Context(), file, header, user.ID, folderID)
		file.Close()

		if err != nil {
			slog.Error("failed to upload file", "error", err, "filename", header.Filename)
			errorCount++
			continue
		}

		uploadedCount++
	}

	slog.Info("batch upload complete", "uploaded", uploadedCount, "errors", errorCount, "uploaded_by", user.ID)

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf(`<div class="upload-success">Uploaded %d files</div>`, uploadedCount)))
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
		if err == sql.ErrNoRows {
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
		Title: "Edit Media",
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Update handles PUT /admin/media/{id} - updates media metadata.
func (h *MediaHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

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
		if err == sql.ErrNoRows {
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
	errors := make(map[string]string)
	if filename == "" {
		errors["filename"] = "Filename is required"
	}

	if len(errors) > 0 {
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
			Errors:   errors,
			FormValues: map[string]string{
				"filename":  filename,
				"alt":       alt,
				"caption":   caption,
				"folder_id": folderIDStr,
			},
		}

		if err := h.renderer.Render(w, r, "admin/media_edit", render.TemplateData{
			Title: "Edit Media",
			User:  user,
			Data:  data,
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

	slog.Info("media updated", "media_id", id, "updated_by", user.ID)
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
		if err == sql.ErrNoRows {
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

	user := middleware.GetUser(r)
	slog.Info("media deleted", "media_id", id, "filename", media.Filename, "deleted_by", user.ID)

	// For HTMX requests, return empty response
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For regular requests, redirect
	h.renderer.SetFlash(r, "Media deleted successfully", "success")
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
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
