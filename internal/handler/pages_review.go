// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

func (h *PagesHandler) SetPreviewTTL(ttl time.Duration) { h.previewTTL = ttl }

func (h *PagesHandler) reviewPanel(r *http.Request, page store.Page, currentRevision int64) (*adminviews.ReviewPanelData, error) {
	details, err := review.Load(r.Context(), h.queries, page, middleware.GetUserID(r))
	if err != nil {
		return nil, err
	}
	access, err := h.queries.GetPageAIAccess(r.Context(), page.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	current, err := h.queries.GetPageByID(r.Context(), page.ID)
	if err != nil {
		return nil, err
	}
	return &adminviews.ReviewPanelData{PageID: page.ID, Revision: page.Revision, BaseRevision: currentRevision, Current: page.Revision == currentRevision, Details: details, PreviewURL: sessionPreviewPath(page),
		CanShare: access.AiEditable && current.AuthorID == middleware.GetUserID(r) && current.Status == "draft" && !current.ScheduledAt.Valid}, nil
}

func (h *PagesHandler) CheckClaim(w http.ResponseWriter, r *http.Request) {
	if middleware.IsDemoMode() {
		http.Error(w, "Read only", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	index, err := strconv.ParseInt(chi.URLParam(r, "index"), 10, 64)
	if err != nil || index < 0 {
		http.NotFound(w, r)
		return
	}
	base := submittedPageRevision(r)
	if base == nil {
		http.Error(w, i18n.T(h.renderer.GetAdminLang(r), "pages.revision_conflict"), http.StatusConflict)
		return
	}
	err = review.SetClaim(r.Context(), h.queries, middleware.GetUserID(r), id, *base, index, r.FormValue("checked") == "on")
	if err != nil {
		lang := h.renderer.GetAdminLang(r)
		var conflict *pagewrite.Conflict
		switch {
		case errors.Is(err, pagewrite.ErrEditorRequired): // also matches sql.ErrNoRows for a deleted account
			http.Error(w, i18n.T(lang, "pages.error_editor_required"), http.StatusForbidden)
		case errors.As(err, &conflict), errors.Is(err, review.ErrClaimUnavailable), errors.Is(err, sql.ErrNoRows):
			http.Error(w, i18n.T(lang, "review.check_conflict"), http.StatusConflict)
		default:
			slog.Error("failed to record claim check", "error", err, "page_id", id, "claim_index", index)
			http.Error(w, i18n.T(lang, "review.check_failed"), http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
}

func (h *PagesHandler) CreatePreviewLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if middleware.IsDemoMode() || r.FormValue("acknowledge") != "on" {
		http.Error(w, "Explicit share acknowledgment required", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	revision, _ := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	base := submittedPageRevision(r)
	if base == nil {
		http.Error(w, "Reload the reviewed revision", http.StatusConflict)
		return
	}
	cfg, err := h.queries.GetConfigByKey(r.Context(), model.ConfigKeySiteURL)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, i18n.T(h.renderer.GetAdminLang(r), "preview.site_url_required"), http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		slog.Error("failed to load site URL for a preview link", "error", err, "page_id", id)
		http.Error(w, i18n.T(h.renderer.GetAdminLang(r), "preview.temporarily_unavailable"), http.StatusServiceUnavailable)
		return
	}
	userID := middleware.GetUserID(r)
	link, err := (preview.Service{Q: h.queries, TTL: h.previewTTL}).Issue(r.Context(), provenance.Principal{UserID: userID, Source: "human", Transport: "admin"}, id, revision, cfg.Value, func(ctx context.Context, q *store.Queries) error {
		page, loadErr := q.GetPageByID(ctx, id)
		if loadErr != nil {
			return loadErr
		}
		if page.Revision != *base {
			return &pagewrite.Conflict{CurrentRevision: page.Revision}
		}
		return nil
	})
	if err != nil {
		lang := h.renderer.GetAdminLang(r)
		var conflict *pagewrite.Conflict
		switch {
		case errors.Is(err, preview.ErrLimit):
			http.Error(w, i18n.T(lang, "preview.limit", preview.MaxActiveLinks, preview.MaxIssuedPerHour), http.StatusTooManyRequests)
		case errors.As(err, &conflict):
			http.Error(w, i18n.T(lang, "pages.error_changed_meanwhile"), http.StatusConflict)
		case errors.Is(err, preview.ErrUnavailable), errors.Is(err, sql.ErrNoRows):
			http.Error(w, i18n.T(lang, "preview.unavailable"), http.StatusConflict)
		default:
			slog.Error("failed to create preview link", "error", err, "page_id", id, "revision", revision)
			http.Error(w, i18n.T(lang, "preview.temporarily_unavailable"), http.StatusServiceUnavailable)
		}
		return
	}
	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(h.renderer.GetAdminLang(r), "preview.created"), nil)
	renderTempl(w, r, adminviews.PreviewLinkCreated(pc, link))
}

func (h *PagesHandler) RevokePreviewLink(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	linkID, _ := strconv.ParseInt(chi.URLParam(r, "linkID"), 10, 64)
	user, err := h.queries.GetUserByID(r.Context(), middleware.GetUserID(r))
	if err != nil || (user.Role != model.RoleAdmin && user.Role != model.RoleEditor) || middleware.IsDemoMode() {
		http.NotFound(w, r)
		return
	}
	page, err := h.queries.GetPageByID(r.Context(), id)
	if err != nil || (page.AuthorID != user.ID && user.Role != model.RoleAdmin) {
		http.NotFound(w, r)
		return
	}
	revoked, err := h.queries.RevokePagePreviewLink(r.Context(), store.RevokePagePreviewLinkParams{RevokedAt: time.Now().Unix(), ID: linkID, PageID: id})
	if err != nil {
		slog.Error("failed to revoke preview link", "error", err, "page_id", id, "link_id", linkID)
		http.Error(w, "Cannot revoke preview", http.StatusInternalServerError)
		return
	}
	lang := h.renderer.GetAdminLang(r)
	target := fmt.Sprintf("/admin/pages/%d", id)
	if revoked == 0 {
		flashError(w, r, h.renderer, target, i18n.T(lang, "preview.revoke_missing"))
		return
	}
	_ = h.eventService.LogPageEvent(r.Context(), model.EventLevelInfo, "Preview share link revoked", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"page_id": id, "link_id": linkID})
	flashSuccess(w, r, h.renderer, target, i18n.T(lang, "preview.revoked"))
}

// RevisionReview compares only recorded fields with the preceding recorded version.
func (h *PagesHandler) RevisionReview(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	versionID, _ := strconv.ParseInt(chi.URLParam(r, "versionId"), 10, 64)
	page, err := h.queries.GetPageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			logAndHTTPError(w, "Cannot load page", http.StatusInternalServerError, "failed to load page for revision review", "error", err, "page_id", id, "version_id", versionID)
		}
		return
	}
	version, err := h.queries.GetPageVersion(r.Context(), versionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			logAndHTTPError(w, "Cannot load revision", http.StatusInternalServerError, "failed to load recorded revision", "error", err, "page_id", id, "version_id", versionID)
		}
		return
	}
	if version.PageID != id {
		http.NotFound(w, r)
		return
	}
	previous, err := h.queries.GetPreviousPageVersion(r.Context(), store.GetPreviousPageVersionParams{PageID: id, ID: version.ID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		logAndHTTPError(w, "Cannot load revision", http.StatusInternalServerError, "failed to load previous revision", "error", err, "page_id", id, "version_id", versionID)
		return
	}
	differences, err := review.Compare(previous, version)
	if err != nil {
		logAndHTTPError(w, "Cannot compare recorded revision", http.StatusInternalServerError, "failed to compare recorded revision", "error", err, "page_id", id, "version_id", versionID)
		return
	}
	data := adminviews.RevisionReviewData{PageID: id, VersionID: version.ID, Revision: version.Revision.Int64, BaseRevision: page.Revision, Title: version.Title, Legacy: version.Legacy, Differences: differences}
	if !version.Legacy && version.Revision.Valid {
		selected := page
		selected.Revision = version.Revision.Int64
		data.Panel, err = h.reviewPanel(r, selected, page.Revision)
		if err != nil {
			logAndHTTPError(w, "Cannot load revision evidence", http.StatusInternalServerError, "failed to load revision evidence", "error", err, "page_id", id, "version_id", versionID, "revision", version.Revision.Int64)
			return
		}
	}
	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(h.renderer.GetAdminLang(r), "review.compare"), nil)
	renderTempl(w, r, adminviews.RevisionReviewPage(pc, data))
}
