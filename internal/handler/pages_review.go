// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
		http.Error(w, i18n.T(h.renderer.GetAdminLang(r), "review.check_conflict"), http.StatusConflict)
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
	if err != nil {
		http.Error(w, "Configure the site URL", http.StatusServiceUnavailable)
		return
	}
	userID := middleware.GetUserID(r)
	link, err := (preview.Service{Q: h.queries, TTL: h.previewTTL}).Issue(r.Context(), provenance.Principal{UserID: userID, Source: "human", Transport: "admin"}, id, revision, cfg.Value, func(ctx context.Context, q *store.Queries) error {
		page, err := q.GetPageByID(ctx, id)
		if err != nil {
			return err
		}
		if page.Revision != *base {
			return &pagewrite.Conflict{CurrentRevision: page.Revision}
		}
		return nil
	})
	if err != nil {
		http.Error(w, i18n.T(h.renderer.GetAdminLang(r), "preview.unavailable"), http.StatusConflict)
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
	if _, err = h.queries.RevokePagePreviewLink(r.Context(), store.RevokePagePreviewLinkParams{RevokedAt: time.Now().Unix(), ID: linkID, PageID: id}); err != nil {
		http.Error(w, "Cannot revoke preview", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
}

// RevisionReview compares only recorded fields with the preceding recorded version.
func (h *PagesHandler) RevisionReview(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	versionID, _ := strconv.ParseInt(chi.URLParam(r, "versionId"), 10, 64)
	page, err := h.queries.GetPageByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	version, err := h.queries.GetPageVersion(r.Context(), versionID)
	if err != nil || version.PageID != id {
		http.NotFound(w, r)
		return
	}
	previous, err := h.queries.GetPreviousPageVersion(r.Context(), store.GetPreviousPageVersionParams{PageID: id, ID: version.ID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Cannot load revision", 500)
		return
	}
	differences, err := review.Compare(previous, version)
	if err != nil {
		http.Error(w, "Cannot compare recorded revision", 500)
		return
	}
	data := adminviews.RevisionReviewData{PageID: id, VersionID: version.ID, Revision: version.Revision.Int64, BaseRevision: page.Revision, Title: version.Title, Legacy: version.Legacy, Differences: differences}
	if !version.Legacy && version.Revision.Valid {
		selected := page
		selected.Revision = version.Revision.Int64
		data.Panel, err = h.reviewPanel(r, selected, page.Revision)
		if err != nil {
			http.Error(w, "Cannot load revision evidence", 500)
			return
		}
	}
	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(h.renderer.GetAdminLang(r), "review.compare"), nil)
	renderTempl(w, r, adminviews.RevisionReviewPage(pc, data))
}
