// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strconv"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/review"
)

// ReviewTranslation is protected by the normal editor session and CSRF
// middleware. Only an explicitly acknowledged, body-bound review is accepted.
func (h *PagesHandler) ReviewTranslation(w http.ResponseWriter, r *http.Request) {
	if middleware.IsDemoMode() {
		http.Error(w, "Read only", http.StatusForbidden)
		return
	}
	lang := h.renderer.GetAdminLang(r)
	id, err := ParseIDParam(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" || r.URL.RawQuery != "" {
		http.Error(w, i18n.T(lang, "review.translation_conflict"), http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err = r.ParseForm(); err != nil {
		http.Error(w, i18n.T(lang, "review.translation_conflict"), http.StatusBadRequest)
		return
	}
	acknowledge := r.PostForm["acknowledge"]
	if len(acknowledge) != 1 || acknowledge[0] != "on" {
		http.Error(w, i18n.T(lang, "review.translation_acknowledge"), http.StatusForbidden)
		return
	}
	in := review.TranslationReviewInput{PageID: id}
	for key, destination := range map[string]*int64{"base_revision": &in.Revision, "source_page_id": &in.SourcePageID, "source_revision": &in.SourceRevision} {
		values := r.PostForm[key]
		if len(values) != 1 {
			http.Error(w, i18n.T(lang, "review.translation_conflict"), http.StatusBadRequest)
			return
		}
		*destination, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || *destination <= 0 {
			http.Error(w, i18n.T(lang, "review.translation_conflict"), http.StatusBadRequest)
			return
		}
	}
	page, err := review.MarkTranslationReviewed(r.Context(), h.queries, middleware.GetUserID(r), in)
	if err != nil {
		var conflict *pagewrite.Conflict
		switch {
		case errors.Is(err, pagewrite.ErrEditorRequired):
			http.Error(w, i18n.T(lang, "pages.error_editor_required"), http.StatusForbidden)
		case errors.As(err, &conflict), errors.Is(err, review.ErrTranslationReviewUnavailable), errors.Is(err, sql.ErrNoRows):
			http.Error(w, i18n.T(lang, "review.translation_conflict"), http.StatusConflict)
		default:
			slog.Error("failed to review translation", "error", err, "page_id", id)
			http.Error(w, i18n.T(lang, "review.translation_failed"), http.StatusInternalServerError)
		}
		return
	}
	h.invalidatePageCache(page.ID)
	h.dispatchPageEvent(r.Context(), model.EventPageUpdated, page, middleware.GetUserEmail(r))
	_ = h.eventService.LogPageEvent(r.Context(), model.EventLevelInfo, "Translation reviewed", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"page_id": page.ID, "revision": page.Revision, "source_page_id": in.SourcePageID, "source_revision": in.SourceRevision})
	flashSuccess(w, r, h.renderer, fmt.Sprintf("/admin/pages/%d", id), i18n.T(lang, "review.translation_saved"))
}
