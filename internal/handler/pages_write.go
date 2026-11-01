// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
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

func (h *PagesHandler) formWrite(r *http.Request, input pageFormInput, base *int64, previous ...store.Page) pageFormWrite {
	userID := middleware.GetUserID(r)
	return pageFormWrite{UserID: userID, BaseRevision: base, Related: func(work *pagewrite.Tx, page store.Page) error {
		ctx := r.Context()
		if len(previous) == 0 || previous[0].FeaturedImageID != page.FeaturedImageID {
			if err := pagewrite.FeaturedImageSize(ctx, work.Q, page.FeaturedImageID); err != nil {
				return err
			}
		}
		if err := work.Q.ClearPageTags(ctx, page.ID); err != nil {
			return err
		}
		for _, raw := range r.Form["tags[]"] {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return errors.New("invalid tag ID")
			}
			if _, err = work.Q.GetTagByID(ctx, id); err != nil {
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
				return errors.New("invalid category ID")
			}
			if _, err = work.Q.GetCategoryByID(ctx, id); err != nil {
				return err
			}
			if err = work.Q.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: page.ID, CategoryID: id}); err != nil {
				return err
			}
		}
		transactional := *h
		transactional.queries = work.Q
		if message := transactional.validatePageAliases(ctx, r.Form["aliases[]"], page.ID); message != "" {
			return fmt.Errorf("%s", message)
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
				return errors.New("only the owner or an administrator may change AI access")
			}
			enabled := r.FormValue("allow_ai_edits") == "on"
			if err = work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: enabled, SetBy: userID, SetAt: time.Now().UTC()}); err != nil {
				return err
			}
		}
		return nil
	}}
}

func (h *PagesHandler) pageConflict(w http.ResponseWriter, r *http.Request, err error, page store.Page, input pageFormInput) bool {
	var conflict *pagewrite.Conflict
	if !errors.As(err, &conflict) && !errors.Is(err, pagewrite.ErrRevisionRequired) {
		return false
	}
	latest, loadErr := h.queries.GetPageByID(r.Context(), page.ID)
	if loadErr != nil {
		flashError(w, r, h.renderer, redirectAdminPages, "Page unavailable")
		return true
	}
	allCategories, loadErr := h.queries.ListCategories(r.Context())
	if loadErr != nil {
		http.Error(w, "Cannot load conflict form", http.StatusInternalServerError)
		return true
	}
	var tags []store.Tag
	for _, raw := range r.Form["tags[]"] {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			continue
		}
		tag, tagErr := h.queries.GetTagByID(r.Context(), id)
		if tagErr == nil {
			tags = append(tags, tag)
		}
	}
	user, loadErr := h.queries.GetUserByID(r.Context(), middleware.GetUserID(r))
	if loadErr != nil {
		http.Error(w, "Account unavailable", http.StatusUnauthorized)
		return true
	}
	info := h.loadPageLanguageInfo(r.Context(), latest)
	data := PageFormData{Page: &latest, BaseRevision: page.Revision, ConflictRevision: latest.Revision, IsEdit: true,
		Tags: tags, Categories: selectedPageCategoriesFromForm(r.Form["categories[]"]), AllCategories: buildPageCategoryTree(allCategories, nil, 0),
		Aliases: pageAliasesFromForm(r.Form["aliases[]"]), AllLanguages: info.AllLanguages, Language: info.EntityLanguage,
		Translations: info.Translations, MissingLanguages: info.MissingLanguages, Statuses: ValidPageStatuses, PageTypes: ValidPageTypes,
		FormValues: input.FormValues, FeaturedImage: h.loadImageData(r.Context(), input.FeaturedImageID), OgImage: h.loadImageData(r.Context(), input.OgImageID),
		AIEditable: r.FormValue("allow_ai_edits") == "on", CanManageAI: latest.AuthorID == user.ID || user.Role == model.RoleAdmin}
	lang := h.renderer.GetAdminLang(r)
	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "pages.revision_conflict_title"), pagesEditBreadcrumbs(lang, latest.Title, latest.ID))
	w.WriteHeader(http.StatusConflict)
	renderTempl(w, r, adminviews.PageFormPage(pc, convertPageFormViewData(data, h.renderer, lang)))
	return true
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
