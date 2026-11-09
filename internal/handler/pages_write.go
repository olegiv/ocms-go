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
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

func submittedPageRevision(r *http.Request) *int64 {
	raw := r.FormValue("base_revision")
	// This button is shown only on the conflict form. It confirms a new CAS
	// against exactly the revision displayed there, never an unconditional save.
	if confirmed := r.FormValue("overwrite_revision"); confirmed != "" {
		raw = confirmed
	}
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision < 1 {
		return nil
	}
	return &revision
}

func (h *PagesHandler) formWrite(r *http.Request, input pageFormInput, base *int64) pageFormWrite {
	userID := middleware.GetUserID(r)
	return pageFormWrite{UserID: userID, BaseRevision: base, Related: func(work *pagewrite.Tx, page store.Page) error {
		if err := work.RequireTracked(page.ID); err != nil {
			return err
		}
		ctx := r.Context()
		if err := work.Q.ClearPageTags(ctx, page.ID); err != nil {
			return err
		}
		for _, raw := range r.Form["tags[]"] {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return &pagewrite.Refusal{MessageKey: "pages.invalid_taxonomy_selection"}
			}
			if _, err = work.Q.GetTagByID(ctx, id); errors.Is(err, sql.ErrNoRows) {
				return &pagewrite.Refusal{MessageKey: "pages.conflict_missing_tag", MessageArgs: []any{id}}
			} else if err != nil {
				return err
			}
			if err = work.Q.AddTagToPage(ctx, store.AddTagToPageParams{PageID: page.ID, TagID: id}); err != nil {
				return err
			}
		}
		if err := work.Q.ClearPageCategories(ctx, page.ID); err != nil {
			return err
		}
		for _, raw := range r.Form["categories[]"] {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return &pagewrite.Refusal{MessageKey: "pages.invalid_taxonomy_selection"}
			}
			if _, err = work.Q.GetCategoryByID(ctx, id); errors.Is(err, sql.ErrNoRows) {
				return &pagewrite.Refusal{MessageKey: "pages.save_missing_category", MessageArgs: []any{id}}
			} else if err != nil {
				return err
			}
			if err = work.Q.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: page.ID, CategoryID: id}); err != nil {
				return err
			}
		}
		if err := checkPageAliases(ctx, work.Q, r.Form["aliases[]"], page.ID); err != nil {
			return err
		}
		if err := work.Q.ClearPageAliases(ctx, page.ID); err != nil {
			return err
		}
		for _, raw := range r.Form["aliases[]"] {
			alias := strings.TrimSpace(raw)
			if alias == "" {
				continue
			}
			if _, err := work.Q.CreatePageAlias(ctx, store.CreatePageAliasParams{PageID: page.ID, Alias: alias, CreatedAt: time.Now().UTC()}); err != nil {
				return err
			}
		}
		if !input.CreatedAt.IsZero() && !input.CreatedAt.Equal(page.CreatedAt) {
			if err := work.Q.UpdatePageCreatedAt(ctx, store.UpdatePageCreatedAtParams{ID: page.ID, CreatedAt: input.CreatedAt}); err != nil {
				return err
			}
		}
		if r.FormValue("ai_access_present") != "" {
			user, err := work.Q.GetUserByID(ctx, userID)
			if err != nil {
				return err
			}
			if page.AuthorID != user.ID && user.Role != model.RoleAdmin {
				return &pagewrite.Refusal{MessageKey: "pages.error_manage_ai_access"}
			}
			enabled := r.FormValue("allow_ai_edits") == "on"
			// A save that leaves AI access as it was must not re-attribute it:
			// set_by names the user who last changed it, and deleting that user
			// is refused while the row exists.
			current, err := work.Q.GetPageAIAccess(ctx, page.ID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if current.AiEditable != enabled {
				if err = work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: enabled, SetBy: userID, SetAt: time.Now().UTC()}); err != nil {
					return err
				}
			}
		}
		if page.Status == "published" || page.ScheduledAt.Valid {
			return review.ValidatePublication(ctx, work.Q, page)
		}
		return nil
	}}
}

// pageWriteMessage explains a refused page write the editor can act on, in
// lang. ok is false for an unexpected failure, which the caller logs and
// reports generically instead of showing its text.
func pageWriteMessage(lang string, err error) (message string, ok bool) {
	var conflict *pagewrite.Conflict
	var refusal *pagewrite.Refusal
	var size *pagewrite.FeaturedImageSizeError
	switch {
	case errors.Is(err, pagewrite.ErrEditorRequired):
		return i18n.T(lang, "pages.error_editor_required"), true
	case errors.As(err, &conflict), errors.Is(err, pagewrite.ErrRevisionRequired):
		return i18n.T(lang, "pages.error_changed_meanwhile"), true
	case errors.Is(err, review.ErrHardValidation):
		return i18n.T(lang, "pages.error_publication_checks"), true
	case errors.Is(err, review.ErrSiteURLRequired):
		return i18n.T(lang, "pages.error_site_url_required"), true
	case errors.Is(err, pagewrite.ErrTranslationExists):
		return i18n.T(lang, "pages.error_translation_exists"), true
	case errors.As(err, &size):
		return i18n.T(lang, "pages.error.featured_image_too_small", model.MinFeaturedImageWidth, model.MinFeaturedImageHeight, size.Width, size.Height), true
	case errors.As(err, &refusal):
		if refusal.MessageKey != "" {
			return i18n.T(lang, refusal.MessageKey, refusal.MessageArgs...), true
		}
		// Fixed text written for people (pagewrite.Refusal), never storage text.
		return refusal.Reason, true
	}
	return "", false
}

func pageWriteFormErrors(lang string, err error) map[string]string {
	if errors.Is(err, errPageRouteTaken) {
		return map[string]string{"slug": "Slug conflicts with the URL prefix of an active language"}
	}
	if message, ok := pageWriteMessage(lang, err); ok {
		return map[string]string{"body": message}
	}
	return nil
}

func (h *PagesHandler) submittedPageTags(ctx context.Context, values []string, lang string) ([]store.Tag, error) {
	var tags []store.Tag
	for _, raw := range values {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		tag, err := h.queries.GetTagByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			tag = store.Tag{ID: id, Name: i18n.T(lang, "pages.conflict_missing_tag", id)}
		} else if err != nil {
			return nil, fmt.Errorf("load submitted tag %d: %w", id, err)
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

func (h *PagesHandler) pageConflict(w http.ResponseWriter, r *http.Request, err error, page store.Page, input pageFormInput) bool {
	var conflict *pagewrite.Conflict
	if !errors.As(err, &conflict) && !errors.Is(err, pagewrite.ErrRevisionRequired) {
		return false
	}
	latest, loadErr := h.queries.GetPageByID(r.Context(), page.ID)
	if loadErr != nil {
		h.preservePageEdits(w, r, page.ID, fmt.Errorf("load current page for conflict form: %w", loadErr))
		return true
	}
	allCategories, loadErr := h.queries.ListCategories(r.Context())
	if loadErr != nil {
		h.preservePageEdits(w, r, page.ID, fmt.Errorf("load categories for conflict form: %w", loadErr))
		return true
	}
	lang := h.renderer.GetAdminLang(r)
	tags, loadErr := h.submittedPageTags(r.Context(), r.Form["tags[]"], lang)
	if loadErr != nil {
		h.preservePageEdits(w, r, page.ID, loadErr)
		return true
	}
	user, loadErr := h.queries.GetUserByID(r.Context(), middleware.GetUserID(r))
	if errors.Is(loadErr, sql.ErrNoRows) {
		http.Error(w, "Account unavailable", http.StatusUnauthorized)
		return true
	}
	if loadErr != nil {
		h.preservePageEdits(w, r, page.ID, fmt.Errorf("load editor for conflict form: %w", loadErr))
		return true
	}
	featured, loadErr := h.queryImageData(r.Context(), input.FeaturedImageID)
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		h.preservePageEdits(w, r, page.ID, fmt.Errorf("load featured image %d for conflict form: %w", input.FeaturedImageID.Int64, loadErr))
		return true
	}
	ogImage, loadErr := h.queryImageData(r.Context(), input.OgImageID)
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		h.preservePageEdits(w, r, page.ID, fmt.Errorf("load Open Graph image %d for conflict form: %w", input.OgImageID.Int64, loadErr))
		return true
	}
	info := h.loadPageLanguageInfo(r.Context(), latest)
	data := PageFormData{Page: &latest, BaseRevision: page.Revision, ConflictRevision: latest.Revision, IsEdit: true,
		Tags: tags, Categories: selectedPageCategoriesFromForm(r.Form["categories[]"]), AllCategories: buildPageCategoryTree(allCategories, nil, 0),
		Aliases: pageAliasesFromForm(r.Form["aliases[]"]), AllLanguages: info.AllLanguages, Language: info.EntityLanguage,
		Translations: info.Translations, MissingLanguages: info.MissingLanguages, Statuses: ValidPageStatuses, PageTypes: ValidPageTypes,
		FormValues: input.FormValues, FeaturedImage: featured, OgImage: ogImage,
		AIEditable: r.FormValue("allow_ai_edits") == "on", CanManageAI: latest.AuthorID == user.ID || user.Role == model.RoleAdmin}
	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "pages.revision_conflict_title"), pagesEditBreadcrumbs(lang, latest.Title, latest.ID))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	renderTempl(w, r, adminviews.PageFormPage(pc, convertPageFormViewData(data, h.renderer, lang, h.sanitizePageHTML)))
	return true
}

func (h *PagesHandler) preservePageEdits(w http.ResponseWriter, r *http.Request, pageID int64, cause error) {
	lang := h.renderer.GetAdminLang(r)
	retryable := !errors.Is(cause, sql.ErrNoRows)
	status, messageKey := http.StatusNotFound, "pages.edit_recovery_missing"
	if retryable {
		status, messageKey = http.StatusServiceUnavailable, "pages.edit_recovery_retry"
		slog.ErrorContext(r.Context(), "page save interrupted", "error", cause, "page_id", pageID, "user_id", middleware.GetUserID(r))
	}
	title := i18n.T(lang, "pages.edit_recovery_title")
	action, breadcrumbs := redirectAdminPages, pagesNewBreadcrumbs(lang)
	if pageID > 0 {
		action = fmt.Sprintf(redirectAdminPagesID, pageID)
		breadcrumbs = pagesEditBreadcrumbs(lang, r.FormValue("title"), pageID)
	}
	pc := buildPageContext(r, h.sessionManager, h.renderer, title, breadcrumbs)
	w.Header().Set(HeaderContentType, "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	renderTempl(w, r, adminviews.PageEditRecovery(pc, action, r.Form, i18n.T(lang, messageKey), retryable))
}

// revisionForAction rejects malformed/missing revisions before an editor action.
func revisionForAction(r *http.Request) (*int64, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	base := submittedPageRevision(r)
	if base == nil {
		return nil, pagewrite.ErrRevisionRequired
	}
	return base, nil
}

func (h *PagesHandler) deletePageAtomic(r *http.Request, id int64) error {
	work, err := pagewrite.InTx(r.Context(), h.db, provenance.Principal{UserID: middleware.GetUserID(r), Source: "human", Transport: "admin"}, func(work *pagewrite.Tx) error {
		if err := pagewrite.RequireEditor(r.Context(), work.Q, middleware.GetUserID(r)); err != nil {
			return err
		}
		return work.Delete(r.Context(), id)
	})
	if err != nil {
		return err
	}
	for changedID := range work.Pages {
		h.invalidatePageCache(changedID)
	}
	return nil
}
