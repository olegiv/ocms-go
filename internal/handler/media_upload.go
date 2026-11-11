// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

type mediaAPIItem struct {
	ID        int64  `json:"id"`
	Filename  string `json:"filename"`
	Filepath  string `json:"filepath"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Mimetype  string `json:"mimetype"`
	Size      int64  `json:"size"`
}

func (h *MediaHandler) pickerItem(ctx context.Context, m store.Medium) mediaAPIItem {
	item := mediaAPIItem{
		ID: m.ID, Filename: m.Filename, Mimetype: m.MimeType, Size: m.Size,
		Filepath: model.MediaURL(model.VariantOriginal, m.Uuid, m.Filename),
	}
	if IsImageMime(m.MimeType) {
		// Icons and uploads with incomplete variant generation still have an original.
		if _, err := h.queries.GetMediaVariant(ctx, store.GetMediaVariantParams{MediaID: m.ID, Type: "thumbnail"}); err == nil {
			item.Thumbnail = model.MediaURL("thumbnail", m.Uuid, m.Filename)
		} else if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "failed to get picker thumbnail", "error", err, "media_id", m.ID)
		}
	}
	return item
}

func acceptsMediaUploadJSON(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || mediaType != "application/json" {
			continue
		}
		if quality, ok := params["q"]; ok {
			q, err := strconv.ParseFloat(quality, 64)
			if err != nil || !(q > 0 && q <= 1) {
				continue
			}
		}
		return true
	}
	return false
}

// mediaFailure separates safe response text from operation-specific diagnostics.
// Classification has no logging side effects; cancellations are silent (T-15/N-4).
type mediaFailure struct {
	status   int
	message  string
	client   bool
	canceled bool
}

func classifyMediaError(err error, message string) mediaFailure {
	failure := mediaFailure{status: http.StatusInternalServerError, message: message}
	if errors.Is(err, context.Canceled) {
		failure.canceled = true
		return failure
	}
	var clientError *service.ClientError
	if errors.As(err, &clientError) {
		failure.status, failure.message, failure.client = http.StatusBadRequest, clientError.Message, true
	}
	return failure
}

func (failure mediaFailure) log(ctx context.Context, message string, err error, attrs ...any) {
	if failure.canceled {
		return
	}
	level := slog.LevelError
	if failure.client {
		level = slog.LevelInfo
	}
	slog.Log(ctx, level, message, append(attrs, "error", err)...)
}

func (h *MediaHandler) mediaUploadLanguage(ctx context.Context) (store.Language, bool) {
	language, err := h.queries.GetDefaultLanguage(ctx)
	if err != nil {
		classifyMediaError(err, "").log(ctx, "failed to get default language", err)
		return language, false
	}
	if !isRoutableContentLanguage(language) {
		slog.ErrorContext(ctx, "default language is not routable", "language_code", language.Code)
		return language, false
	}
	return language, true
}

// uploadJSON is the single-file picker transport. Browser forms and HTMX keep
// their existing responses in Upload; this path never redirects on an error.
func (h *MediaHandler) uploadJSON(w http.ResponseWriter, r *http.Request) {
	if middleware.IsDemoMode() {
		writeJSONError(w, http.StatusForbidden, middleware.DemoModeMessageDetailed(middleware.RestrictionContentReadOnly))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMediaUploadRequestBytes)
	err := r.ParseMultipartForm(service.MaxUploadSize)
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSONError(w, status, "File too large or invalid form")
		return
	}
	files := r.MultipartForm.File["file"]
	count := 0
	for _, entries := range r.MultipartForm.File {
		count += len(entries)
	}
	if len(files) != 1 || count != 1 {
		writeJSONError(w, http.StatusBadRequest, "Upload exactly one image using the file field")
		return
	}
	header := files[0]
	if header.Size > service.MaxUploadSize {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "Image exceeds the 20 MB upload limit")
		return
	}
	language, ok := h.mediaUploadLanguage(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "Error uploading image")
		return
	}
	file, err := header.Open()
	if err != nil {
		classifyMediaError(err, "").log(r.Context(), "failed to open picker upload", err, "filename", header.Filename)
		writeJSONError(w, http.StatusInternalServerError, "Error uploading image")
		return
	}
	defer func() { _ = file.Close() }()
	result, err := h.mediaService.UploadImage(r.Context(), file, header, middleware.GetUserID(r), nil, language.Code)
	if err != nil {
		failure := classifyMediaError(err, "Error uploading image")
		failure.log(r.Context(), "failed to upload picker image", err, "filename", header.Filename)
		writeJSONError(w, failure.status, failure.message)
		return
	}
	if err := h.eventService.LogMediaEvent(r.Context(), model.EventLevelInfo, "Media uploaded", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"media_id": result.Media.ID, "filename": result.Media.Filename}); err != nil && !errors.Is(err, context.Canceled) {
		slog.ErrorContext(service.WithoutEventLog(r.Context()), "failed to log picker upload event", "error", err, "media_id", result.Media.ID)
	}
	h.dispatchMediaEvent(r.Context(), model.EventMediaUploaded, result.Media)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(struct {
		Item mediaAPIItem `json:"item"`
	}{h.pickerItem(r.Context(), result.Media)})
}
