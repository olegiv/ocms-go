// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// buildPageContext creates a PageContext for templ views from the request state.
func buildPageContext(
	r *http.Request,
	sm *scs.SessionManager,
	renderer *render.Renderer,
	title string,
	breadcrumbs []render.Breadcrumb,
) *adminviews.PageContext {
	user := middleware.GetUser(r)
	adminLang := renderer.GetAdminLang(r)

	var userInfo adminviews.UserInfo
	if user != nil {
		userInfo = adminviews.UserInfo{
			ID:    user.ID,
			Name:  user.Name,
			Email: user.Email,
			Role:  user.Role,
		}
	}

	pc := &adminviews.PageContext{
		Title:       title,
		User:        userInfo,
		SiteName:    middleware.GetSiteName(r),
		CurrentPath: r.URL.Path,
		AdminLang:   adminLang,
	}

	// Convert breadcrumbs
	for _, b := range breadcrumbs {
		pc.Breadcrumbs = append(pc.Breadcrumbs, adminviews.Breadcrumb{
			Label:  b.Label,
			URL:    b.URL,
			Active: b.Active,
		})
	}

	// Get sidebar modules
	for _, m := range renderer.ListSidebarModules() {
		pc.SidebarModules = append(pc.SidebarModules, adminviews.SidebarModule{
			Name:     m.Name,
			Label:    m.Label,
			AdminURL: m.AdminURL,
		})
	}

	// Get flash message from session
	if sm != nil {
		if flash := sm.PopString(r.Context(), "flash"); flash != "" {
			pc.Flash = flash
			pc.FlashType = sm.PopString(r.Context(), "flash_type")
			if pc.FlashType == "" {
				pc.FlashType = "info"
			}
		}
	}

	return pc
}

// renderTempl renders a templ component as an HTTP response.
func renderTempl(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set(HeaderContentType, "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// convertPagination converts handler AdminPagination to view PaginationData.
func convertPagination(p AdminPagination) adminviews.PaginationData {
	var pages []adminviews.PaginationPage
	for _, pg := range p.Pages {
		pages = append(pages, adminviews.PaginationPage{
			Number:     pg.Number,
			URL:        pg.URL,
			IsCurrent:  pg.IsCurrent,
			IsEllipsis: pg.IsEllipsis,
		})
	}
	return adminviews.PaginationData{
		CurrentPage: p.CurrentPage,
		TotalPages:  p.TotalPages,
		TotalItems:  p.TotalItems,
		HasFirst:    p.HasFirst,
		HasPrev:     p.HasPrev,
		HasNext:     p.HasNext,
		HasLast:     p.HasLast,
		FirstURL:    p.FirstURL(),
		PrevURL:     p.PrevURL(),
		NextURL:     p.NextURL(),
		LastURL:     p.LastURL(),
		Pages:       pages,
	}
}

// dashboardBreadcrumbs returns breadcrumbs for the dashboard page.
func dashboardBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin, Active: true},
	}
}

// tagsBreadcrumbs returns breadcrumbs for the tags list page.
func tagsBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "tags.title"), URL: redirectAdminTags, Active: true},
	}
}

// tagFormBreadcrumbs returns breadcrumbs for the tag form page.
func tagFormBreadcrumbs(lang string, isEdit bool) []render.Breadcrumb {
	label := i18n.T(lang, "tags.new")
	if isEdit {
		label = i18n.T(lang, "tags.edit")
	}
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "tags.title"), URL: redirectAdminTags},
		{Label: label, Active: true},
	}
}

// categoriesBreadcrumbs returns breadcrumbs for the categories list page.
func categoriesBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "categories.title"), URL: redirectAdminCategories, Active: true},
	}
}

// categoryFormBreadcrumbs returns breadcrumbs for the category form page.
func categoryFormBreadcrumbs(lang string, isEdit bool) []render.Breadcrumb {
	label := i18n.T(lang, "categories.new")
	if isEdit {
		label = i18n.T(lang, "categories.edit")
	}
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "categories.title"), URL: redirectAdminCategories},
		{Label: label, Active: true},
	}
}

// tagEditBreadcrumbs returns breadcrumbs for the tag edit form with entity name.
func tagEditBreadcrumbs(lang string, tagName string, tagID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.tags"), URL: redirectAdminTags},
		{Label: tagName, URL: fmt.Sprintf(redirectAdminTagsID, tagID), Active: true},
	}
}

// categoryEditBreadcrumbs returns breadcrumbs for the category edit form with entity name.
func categoryEditBreadcrumbs(lang string, categoryName string, categoryID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.categories"), URL: redirectAdminCategories},
		{Label: categoryName, URL: fmt.Sprintf(redirectAdminCategoriesID, categoryID), Active: true},
	}
}

// eventsBreadcrumbs returns breadcrumbs for the events list page.
func eventsBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.event_log"), URL: redirectAdminEvents, Active: true},
	}
}

// convertEventItems converts handler EventWithUser slice to view EventItem slice.
// It masks IPs for display and pre-computes sentinel ban/whitelist state.
func convertEventItems(events []EventWithUser, renderer *render.Renderer, sentinelActive bool) []adminviews.EventItem {
	items := make([]adminviews.EventItem, len(events))
	for i, e := range events {
		items[i] = adminviews.EventItem{
			ID:           e.ID,
			Level:        e.Level,
			Category:     e.Category,
			Message:      e.Message,
			Details:      e.Details,
			DetailsLong:  e.DetailsLong,
			IPAddress:    maskIP(e.IPAddress),
			RawIPAddress: e.IPAddress,
			IsOwnIP:      e.IsOwnIP,
			RequestURL:   e.RequestURL,
			CreatedAt:    e.CreatedAt,
			UserName:     e.UserName,
		}
		if sentinelActive && e.IPAddress != "" {
			items[i].IsBanned = renderer.SentinelIsIPBanned(e.IPAddress)
			items[i].IsWhitelisted = renderer.SentinelIsIPWhitelisted(e.IPAddress)
		}
	}
	return items
}

// =============================================================================
// TYPE CONVERSION HELPERS (store → view types)
// =============================================================================

// convertLanguageOption converts a store.Language to a view LanguageOption.
func convertLanguageOption(lang store.Language) adminviews.LanguageOption {
	return adminviews.LanguageOption{
		Code:       lang.Code,
		Name:       lang.Name,
		NativeName: lang.NativeName,
	}
}

// convertLanguageOptions converts store languages to view language options.
func convertLanguageOptions(langs []store.Language) []adminviews.LanguageOption {
	var opts []adminviews.LanguageOption
	for _, lang := range langs {
		opts = append(opts, convertLanguageOption(lang))
	}
	return opts
}

// convertLanguageOptionPtr converts a *store.Language to a *adminviews.LanguageOption.
func convertLanguageOptionPtr(lang *store.Language) *adminviews.LanguageOption {
	if lang == nil {
		return nil
	}
	opt := convertLanguageOption(*lang)
	return &opt
}

// convertTagItem converts a store.Tag to a view TagItem.
func convertTagItem(tag store.Tag) adminviews.TagItem {
	return adminviews.TagItem{
		ID:           tag.ID,
		Name:         tag.Name,
		Slug:         tag.Slug,
		LanguageCode: tag.LanguageCode,
	}
}

// convertTagTranslations converts handler TagTranslationInfo slice to view TagTranslation slice.
func convertTagTranslations(translations []TagTranslationInfo) []adminviews.TagTranslation {
	var result []adminviews.TagTranslation
	for _, tr := range translations {
		result = append(result, adminviews.TagTranslation{
			Tag:      convertTagItem(tr.Tag),
			Language: convertLanguageOption(tr.Language),
		})
	}
	return result
}

// convertCategoryItem converts a store.Category to a view CategoryItem.
func convertCategoryItem(cat store.Category) adminviews.CategoryItem {
	return adminviews.CategoryItem{
		ID:           cat.ID,
		Name:         cat.Name,
		Slug:         cat.Slug,
		Description:  cat.Description.String,
		LanguageCode: cat.LanguageCode,
		ParentID:     cat.ParentID.Int64,
		HasParent:    cat.ParentID.Valid,
		Position:     int(cat.Position),
	}
}

// convertCategoryListItems converts handler CategoryTreeNode slice to view CategoryListItem slice.
func convertCategoryListItems(nodes []CategoryTreeNode) []adminviews.CategoryListItem {
	var items []adminviews.CategoryListItem
	for _, node := range nodes {
		desc := ""
		if node.Category.Description.Valid {
			desc = node.Category.Description.String
		}
		items = append(items, adminviews.CategoryListItem{
			ID:           node.Category.ID,
			Name:         node.Category.Name,
			Slug:         node.Category.Slug,
			Description:  desc,
			LanguageCode: node.Category.LanguageCode,
			UsageCount:   node.UsageCount,
			Depth:        node.Depth,
			Children:     len(node.Children) > 0,
		})
	}
	return items
}

// convertCategoryTranslations converts handler CategoryTranslationInfo slice to view CategoryTranslation slice.
func convertCategoryTranslations(translations []CategoryTranslationInfo) []adminviews.CategoryTranslation {
	var result []adminviews.CategoryTranslation
	for _, tr := range translations {
		result = append(result, adminviews.CategoryTranslation{
			Category: convertCategoryItem(tr.Category),
			Language: convertLanguageOption(tr.Language),
		})
	}
	return result
}
