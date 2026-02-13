// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
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

// usersBreadcrumbs returns breadcrumbs for the users list page.
func usersBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.users"), URL: redirectAdminUsers, Active: true},
	}
}

// userFormBreadcrumbs returns breadcrumbs for the user form page.
func userFormBreadcrumbs(lang string, isEdit bool) []render.Breadcrumb {
	label := i18n.T(lang, "users.new")
	if isEdit {
		label = i18n.T(lang, "users.edit")
	}
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.users"), URL: redirectAdminUsers},
		{Label: label, Active: true},
	}
}

// userEditBreadcrumbs returns breadcrumbs for the user edit form with entity name.
func userEditBreadcrumbs(lang string, userName string, userID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.users"), URL: redirectAdminUsers},
		{Label: userName, URL: fmt.Sprintf(redirectAdminUsersID, userID), Active: true},
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

// =============================================================================
// REDIRECTS HELPERS
// =============================================================================

// redirectsBreadcrumbs returns breadcrumbs for the redirects list page.
func redirectsBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.redirects"), URL: redirectAdminRedirects, Active: true},
	}
}

// redirectFormBreadcrumbs returns breadcrumbs for the redirect form page.
func redirectFormBreadcrumbs(lang string, isEdit bool) []render.Breadcrumb {
	label := i18n.T(lang, "redirects.new")
	if isEdit {
		label = i18n.T(lang, "redirects.edit")
	}
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.redirects"), URL: redirectAdminRedirects},
		{Label: label, Active: true},
	}
}

// redirectEditBreadcrumbs returns breadcrumbs for the redirect edit form with entity name.
func redirectEditBreadcrumbs(lang string, sourcePath string, redirectID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.redirects"), URL: redirectAdminRedirects},
		{Label: sourcePath, URL: fmt.Sprintf(redirectAdminRedirectsID, redirectID), Active: true},
	}
}

// convertRedirectListItems converts store redirects to view RedirectListItem slice.
func convertRedirectListItems(redirects []store.Redirect) []adminviews.RedirectListItem {
	items := make([]adminviews.RedirectListItem, len(redirects))
	for i, r := range redirects {
		items[i] = adminviews.RedirectListItem{
			ID:         r.ID,
			SourcePath: r.SourcePath,
			TargetURL:  r.TargetUrl,
			StatusCode: r.StatusCode,
			IsWildcard: r.IsWildcard,
			TargetType: r.TargetType,
			Enabled:    r.Enabled,
		}
	}
	return items
}

// convertRedirectInfo converts a store.Redirect pointer to a view RedirectInfo pointer.
func convertRedirectInfo(rd *store.Redirect) *adminviews.RedirectInfo {
	if rd == nil {
		return nil
	}
	return &adminviews.RedirectInfo{
		ID:         rd.ID,
		SourcePath: rd.SourcePath,
		TargetURL:  rd.TargetUrl,
		StatusCode: rd.StatusCode,
		IsWildcard: rd.IsWildcard,
		TargetType: rd.TargetType,
		Enabled:    rd.Enabled,
		CreatedAt:  rd.CreatedAt,
		UpdatedAt:  rd.UpdatedAt,
	}
}

// convertStatusCodes converts handler StatusCodeOption slice to view RedirectStatusCodeOption slice.
func convertStatusCodes(codes []StatusCodeOption) []adminviews.RedirectStatusCodeOption {
	items := make([]adminviews.RedirectStatusCodeOption, len(codes))
	for i, c := range codes {
		items[i] = adminviews.RedirectStatusCodeOption{
			Code:  c.Code,
			Label: c.Label,
		}
	}
	return items
}

// =============================================================================
// MENUS HELPERS
// =============================================================================

// menusBreadcrumbs returns breadcrumbs for the menus list page.
func menusBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.menus"), URL: redirectAdminMenus, Active: true},
	}
}

// menuFormBreadcrumbs returns breadcrumbs for the menu new form page.
func menuFormBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.menus"), URL: redirectAdminMenus},
		{Label: i18n.T(lang, "menus.new"), Active: true},
	}
}

// menuEditBreadcrumbs returns breadcrumbs for the menu edit form with entity name.
func menuEditBreadcrumbs(lang string, menuName string, menuID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.menus"), URL: redirectAdminMenus},
		{Label: menuName, URL: fmt.Sprintf(redirectAdminMenusID, menuID), Active: true},
	}
}

// convertMenuListItems converts store menus to view MenuListItem slice.
func convertMenuListItems(menus []store.Menu) []adminviews.MenuListItem {
	items := make([]adminviews.MenuListItem, len(menus))
	for i, m := range menus {
		items[i] = adminviews.MenuListItem{
			ID:           m.ID,
			Name:         m.Name,
			Slug:         m.Slug,
			LanguageCode: m.LanguageCode,
			UpdatedAt:    m.UpdatedAt,
			IsProtected:  m.Slug == "main" || m.Slug == "footer",
		}
	}
	return items
}

// convertMenuPages converts store pages to view MenuPageItem slice.
func convertMenuPages(pages []store.Page) []adminviews.MenuPageItem {
	items := make([]adminviews.MenuPageItem, len(pages))
	for i, p := range pages {
		items[i] = adminviews.MenuPageItem{
			ID:    p.ID,
			Title: p.Title,
			Slug:  p.Slug,
		}
	}
	return items
}

// convertMenuInfo converts a store.Menu pointer to a view MenuInfo pointer.
func convertMenuInfo(menu *store.Menu) *adminviews.MenuInfo {
	if menu == nil {
		return nil
	}
	return &adminviews.MenuInfo{
		ID:           menu.ID,
		Name:         menu.Name,
		Slug:         menu.Slug,
		LanguageCode: menu.LanguageCode,
	}
}

// =============================================================================
// LANGUAGES HELPERS
// =============================================================================

// languagesBreadcrumbs returns breadcrumbs for the languages list page.
func languagesBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.languages"), URL: redirectAdminLanguages, Active: true},
	}
}

// languageFormBreadcrumbs returns breadcrumbs for the language form page.
func languageFormBreadcrumbs(lang string, isEdit bool) []render.Breadcrumb {
	label := i18n.T(lang, "languages.new")
	if isEdit {
		label = i18n.T(lang, "languages.edit")
	}
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.languages"), URL: redirectAdminLanguages},
		{Label: label, Active: true},
	}
}

// languageEditBreadcrumbs returns breadcrumbs for the language edit form with entity name.
func languageEditBreadcrumbs(lang string, langName string, langID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.languages"), URL: redirectAdminLanguages},
		{Label: langName, URL: fmt.Sprintf(redirectAdminLanguagesID, langID), Active: true},
	}
}

// convertLanguageListItems converts store languages to view LanguageListItem slice.
func convertLanguageListItems(languages []store.Language) []adminviews.LanguageListItem {
	items := make([]adminviews.LanguageListItem, len(languages))
	for i, l := range languages {
		items[i] = adminviews.LanguageListItem{
			ID:         l.ID,
			Code:       l.Code,
			Name:       l.Name,
			NativeName: l.NativeName,
			Direction:  l.Direction,
			IsDefault:  l.IsDefault,
			IsActive:   l.IsActive,
			Position:   l.Position,
		}
	}
	return items
}

// convertLanguageInfo converts a store.Language pointer to a view LanguageInfo pointer.
func convertLanguageInfo(lang *store.Language) *adminviews.LanguageInfo {
	if lang == nil {
		return nil
	}
	return &adminviews.LanguageInfo{
		ID:         lang.ID,
		Code:       lang.Code,
		Name:       lang.Name,
		NativeName: lang.NativeName,
		Direction:  lang.Direction,
		IsDefault:  lang.IsDefault,
		IsActive:   lang.IsActive,
		Position:   lang.Position,
		CreatedAt:  lang.CreatedAt,
		UpdatedAt:  lang.UpdatedAt,
	}
}

// convertCommonLanguages converts model CommonLanguages to view CommonLanguageOption slice.
func convertCommonLanguages() []adminviews.CommonLanguageOption {
	opts := make([]adminviews.CommonLanguageOption, len(model.CommonLanguages))
	for i, cl := range model.CommonLanguages {
		opts[i] = adminviews.CommonLanguageOption{
			Code:       cl.Code,
			Name:       cl.Name,
			NativeName: cl.NativeName,
			Direction:  cl.Direction,
		}
	}
	return opts
}

// =============================================================================
// API KEYS HELPERS
// =============================================================================

// apiKeysBreadcrumbs returns breadcrumbs for the API keys list page.
func apiKeysBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.api_keys"), URL: redirectAdminAPIKeys, Active: true},
	}
}

// apiKeyFormBreadcrumbs returns breadcrumbs for the API key form page.
func apiKeyFormBreadcrumbs(lang string, isEdit bool) []render.Breadcrumb {
	label := i18n.T(lang, "api_keys.new_key")
	if isEdit {
		label = i18n.T(lang, "api_keys.edit_key")
	}
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.api_keys"), URL: redirectAdminAPIKeys},
		{Label: label, Active: true},
	}
}

// apiKeyEditBreadcrumbs returns breadcrumbs for the API key edit form with entity name.
func apiKeyEditBreadcrumbs(lang string, keyName string, keyID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.api_keys"), URL: redirectAdminAPIKeys},
		{Label: keyName, URL: fmt.Sprintf("%s/%d", redirectAdminAPIKeys, keyID), Active: true},
	}
}

// convertAPIKeyListItems converts store API keys to view APIKeyListItem slice.
func convertAPIKeyListItems(apiKeys []store.ApiKey) []adminviews.APIKeyListItem {
	items := make([]adminviews.APIKeyListItem, len(apiKeys))
	for i, k := range apiKeys {
		items[i] = adminviews.APIKeyListItem{
			ID:        k.ID,
			Name:      k.Name,
			KeyPrefix: k.KeyPrefix,
			IsActive:  k.IsActive,
		}

		// Parse permissions JSON
		items[i].Permissions = parsePermissionsJSON(k.Permissions)

		// Check if expired (active but past expiration)
		if k.IsActive && k.ExpiresAt.Valid && k.ExpiresAt.Time.Before(time.Now()) {
			items[i].IsExpired = true
		}

		// Format dates
		if k.LastUsedAt.Valid {
			items[i].LastUsedAt = k.LastUsedAt.Time.Format("Jan 2, 2006 3:04 PM")
		}
		if k.ExpiresAt.Valid {
			items[i].ExpiresAt = k.ExpiresAt.Time.Format("Jan 2, 2006")
		}
	}
	return items
}

// convertAPIKeyInfo converts a store.ApiKey pointer to a view APIKeyInfo pointer.
func convertAPIKeyInfo(apiKey *store.ApiKey) *adminviews.APIKeyInfo {
	if apiKey == nil {
		return nil
	}
	info := &adminviews.APIKeyInfo{
		ID:        apiKey.ID,
		Name:      apiKey.Name,
		KeyPrefix: apiKey.KeyPrefix,
		IsActive:  apiKey.IsActive,
		CreatedAt: apiKey.CreatedAt.Format("Jan 2, 2006 3:04 PM"),
	}

	// Permissions - raw JSON for form checking
	info.Permissions = apiKey.Permissions

	if apiKey.LastUsedAt.Valid {
		info.HasLastUsed = true
		info.LastUsedAt = apiKey.LastUsedAt.Time.Format("Jan 2, 2006 3:04 PM")
	}
	if apiKey.ExpiresAt.Valid {
		info.HasExpires = true
		info.ExpiresAt = apiKey.ExpiresAt.Time.Format("2006-01-02")
	}

	return info
}

// buildPermissionGroups creates the permission groups for the API key form.
// existingPerms is the raw JSON permissions string (for edit mode checking).
func buildPermissionGroups(existingPerms string) []adminviews.PermissionGroup {
	return []adminviews.PermissionGroup{
		{
			TitleKey: "api_keys.perm_pages",
			Permissions: []adminviews.PermissionOption{
				{Value: "pages:read", DescKey: "api_keys.perm_pages_read", Checked: strings.Contains(existingPerms, "pages:read")},
				{Value: "pages:write", DescKey: "api_keys.perm_pages_write", Checked: strings.Contains(existingPerms, "pages:write")},
			},
		},
		{
			TitleKey: "api_keys.perm_media",
			Permissions: []adminviews.PermissionOption{
				{Value: "media:read", DescKey: "api_keys.perm_media_read", Checked: strings.Contains(existingPerms, "media:read")},
				{Value: "media:write", DescKey: "api_keys.perm_media_write", Checked: strings.Contains(existingPerms, "media:write")},
			},
		},
		{
			TitleKey: "api_keys.perm_taxonomy",
			Permissions: []adminviews.PermissionOption{
				{Value: "taxonomy:read", DescKey: "api_keys.perm_taxonomy_read", Checked: strings.Contains(existingPerms, "taxonomy:read")},
				{Value: "taxonomy:write", DescKey: "api_keys.perm_taxonomy_write", Checked: strings.Contains(existingPerms, "taxonomy:write")},
			},
		},
	}
}

// =============================================================================
// CACHE HELPERS
// =============================================================================

// cacheBreadcrumbs returns breadcrumbs for the cache stats page.
func cacheBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.cache"), URL: redirectAdminCache, Active: true},
	}
}

// convertCacheItems converts cache.ManagerCacheStats slice to view CacheItemView slice.
func convertCacheItems(caches []cache.ManagerCacheStats) []adminviews.CacheItemView {
	items := make([]adminviews.CacheItemView, len(caches))
	for idx, c := range caches {
		items[idx] = adminviews.CacheItemView{
			Name:    c.Name,
			Kind:    string(c.Kind),
			Items:   c.Stats.Items,
			Hits:    c.Stats.Hits,
			Misses:  c.Stats.Misses,
			HitRate: c.Stats.HitRate,
			Size:    c.Size,
		}
		if c.CachedAt != nil {
			items[idx].CachedAt = c.CachedAt.Format("Jan 2, 15:04")
		}
	}
	return items
}

// parsePermissionsJSON parses a JSON array string into a string slice.
func parsePermissionsJSON(jsonStr string) []string {
	var perms []string
	if jsonStr == "" || jsonStr == "[]" {
		return perms
	}
	if err := json.Unmarshal([]byte(jsonStr), &perms); err != nil {
		return nil
	}
	return perms
}

// =============================================================================
// DOCS HELPERS
// =============================================================================

// docsBreadcrumbs returns breadcrumbs for the docs overview page.
func docsBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "docs.title"), URL: redirectAdminDocs, Active: true},
	}
}

// docsGuideBreadcrumbs returns breadcrumbs for a single guide page.
func docsGuideBreadcrumbs(lang string, title string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "docs.title"), URL: redirectAdminDocs},
		{Label: title, Active: true},
	}
}

// convertDocsViewData converts handler types to the view data struct.
func convertDocsViewData(sys DocsSystemInfo, groups []DocsEndpointGroup, guides []DocsGuide) adminviews.DocsViewData {
	viewSys := adminviews.DocsSystemInfoView{
		Version:        sys.Version,
		GitCommit:      sys.GitCommit,
		BuildTime:      sys.BuildTime,
		GoVersion:      sys.GoVersion,
		Environment:    sys.Environment,
		ServerPort:     sys.ServerPort,
		DBPath:         sys.DBPath,
		ActiveTheme:    sys.ActiveTheme,
		CacheType:      sys.CacheType,
		EnabledModules: sys.EnabledModules,
		TotalModules:   sys.TotalModules,
		Uptime:         sys.Uptime,
	}

	var viewGroups []adminviews.DocsEndpointGroupView
	for _, g := range groups {
		vg := adminviews.DocsEndpointGroupView{Name: g.Name}
		for _, ep := range g.Endpoints {
			vg.Endpoints = append(vg.Endpoints, adminviews.DocsEndpointView{
				Method:      ep.Method,
				Path:        ep.Path,
				Description: ep.Description,
				Auth:        ep.Auth,
			})
		}
		viewGroups = append(viewGroups, vg)
	}

	var viewGuides []adminviews.DocsGuideView
	for _, g := range guides {
		viewGuides = append(viewGuides, adminviews.DocsGuideView{
			Slug:  g.Slug,
			Title: g.Title,
		})
	}

	return adminviews.DocsViewData{
		System:    viewSys,
		Endpoints: viewGroups,
		Guides:    viewGuides,
	}
}

// =============================================================================
// MODULES HELPERS
// =============================================================================

// modulesBreadcrumbs returns breadcrumbs for the modules list page.
func modulesBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules", Active: true},
	}
}

// convertModuleViewItems converts module.Info slice to view ModuleViewItem slice.
func convertModuleViewItems(modules []module.Info) []adminviews.ModuleViewItem {
	items := make([]adminviews.ModuleViewItem, len(modules))
	for i, m := range modules {
		items[i] = adminviews.ModuleViewItem{
			Name:              m.Name,
			Version:           m.Version,
			Description:       m.Description,
			AdminURL:          m.AdminURL,
			Active:            m.Active,
			ShowInSidebar:     m.ShowInSidebar,
			HasMigrations:     m.HasMigrations,
			MigrationsApplied: m.MigrationsApplied,
			MigrationCount:    m.MigrationCount,
			MigrationsPending: m.MigrationsPending,
		}
	}
	return items
}

// convertHookViewItems converts module.HookInfo slice to view HookViewItem slice.
func convertHookViewItems(hooks []module.HookInfo) []adminviews.HookViewItem {
	items := make([]adminviews.HookViewItem, len(hooks))
	for i, h := range hooks {
		items[i] = adminviews.HookViewItem{Name: h.Name}
		for _, hh := range h.Handlers {
			items[i].Handlers = append(items[i].Handlers, adminviews.HookHandlerViewItem{
				Name:     hh.Name,
				Module:   hh.Module,
				Priority: hh.Priority,
			})
		}
	}
	return items
}

// =============================================================================
// THEMES HELPERS
// =============================================================================

// themesBreadcrumbs returns breadcrumbs for the themes list page.
func themesBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.themes"), URL: redirectAdminThemes, Active: true},
	}
}

// themeSettingsBreadcrumbs returns breadcrumbs for the theme settings page.
func themeSettingsBreadcrumbs(lang string, configName string, themeName string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.themes"), URL: redirectAdminThemes},
		{Label: configName + " Settings", URL: redirectAdminThemesSlash + themeName + pathSettings, Active: true},
	}
}

// convertThemeViewItems converts theme.Info slice to view ThemeViewItem slice.
func convertThemeViewItems(themes []theme.Info) []adminviews.ThemeViewItem {
	items := make([]adminviews.ThemeViewItem, len(themes))
	for i, t := range themes {
		items[i] = adminviews.ThemeViewItem{
			Name:              t.Name,
			ConfigName:        t.Config.Name,
			ConfigVersion:     t.Config.Version,
			ConfigAuthor:      t.Config.Author,
			ConfigDescription: t.Config.Description,
			ConfigScreenshot:  t.Config.Screenshot,
			IsActive:          t.IsActive,
			IsEmbedded:        t.IsEmbedded,
			HasSettings:       len(t.Config.Settings) > 0,
			SettingsURL:       fmt.Sprintf("/admin/themes/%s/settings", t.Name),
		}
	}
	return items
}

// convertThemeSettingsViewData converts theme data to the settings view data.
func convertThemeSettingsViewData(thm *theme.Theme, themeName string, isActive bool, settings map[string]string, errors map[string]string) adminviews.ThemeSettingsViewData {
	var viewSettings []adminviews.ThemeSettingView
	for _, s := range thm.Config.Settings {
		vs := adminviews.ThemeSettingView{
			Key:     s.Key,
			Label:   s.Label,
			Type:    s.Type,
			Default: s.Default,
			Options: s.Options,
			Value:   settings[s.Key],
			Error:   errors[s.Key],
		}
		viewSettings = append(viewSettings, vs)
	}

	return adminviews.ThemeSettingsViewData{
		ThemeName:       themeName,
		ThemeConfigName: thm.Config.Name,
		IsActive:        isActive,
		Settings:        viewSettings,
		IsDemoMode:      middleware.IsDemoMode(),
	}
}

// =============================================================================
// EXPORT/IMPORT HELPERS
// =============================================================================

// exportBreadcrumbs returns breadcrumbs for the export page.
func exportBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.export"), URL: "/admin/export", Active: true},
	}
}

// convertImportViewData converts handler ImportFormData to view ImportViewData.
func convertImportViewData(data ImportFormData) adminviews.ImportViewData {
	viewData := adminviews.ImportViewData{
		IsZipFile:     data.IsZipFile,
		HasMediaFiles: data.HasMediaFiles,
	}

	// Convert conflict strategies
	for _, cs := range data.ConflictStrategies {
		viewData.ConflictStrategies = append(viewData.ConflictStrategies, adminviews.ConflictStrategyView{
			Value:       cs.Value,
			Label:       cs.Label,
			Description: cs.Description,
		})
	}

	// Convert validation result
	if data.ValidationResult != nil {
		vr := &adminviews.ValidationResultView{
			Valid:     data.ValidationResult.Valid,
			Entities:  data.ValidationResult.Entities,
			Conflicts: data.ValidationResult.Conflicts,
		}
		for _, e := range data.ValidationResult.Errors {
			vr.Errors = append(vr.Errors, adminviews.ValidationErrorView{
				Entity:  e.Entity,
				Message: e.Message,
			})
		}
		viewData.ValidationResult = vr
	}

	// Convert import result
	if data.ImportResult != nil {
		ir := &adminviews.ImportResultView{
			Success:      data.ImportResult.Success,
			DryRun:       data.ImportResult.DryRun,
			TotalCreated: data.ImportResult.TotalCreated(),
			TotalUpdated: data.ImportResult.TotalUpdated(),
			TotalSkipped: data.ImportResult.TotalSkipped(),
			Created:      data.ImportResult.Created,
			Updated:      data.ImportResult.Updated,
			Skipped:      data.ImportResult.Skipped,
		}
		for _, e := range data.ImportResult.Errors {
			ir.Errors = append(ir.Errors, adminviews.ImportErrorView{
				Entity:  e.Entity,
				ID:      e.ID,
				Message: e.Message,
			})
		}
		viewData.ImportResult = ir
	}

	// Convert uploaded data flags
	if data.UploadedData != nil {
		viewData.UploadedData = &adminviews.ImportUploadedDataView{
			HasPages:      len(data.UploadedData.Pages) > 0,
			HasCategories: len(data.UploadedData.Categories) > 0,
			HasTags:       len(data.UploadedData.Tags) > 0,
			HasMedia:      len(data.UploadedData.Media) > 0,
			HasMenus:      len(data.UploadedData.Menus) > 0,
			HasForms:      len(data.UploadedData.Forms) > 0,
			HasUsers:      len(data.UploadedData.Users) > 0,
			HasLanguages:  len(data.UploadedData.Languages) > 0,
			HasConfig:     len(data.UploadedData.Config) > 0,
		}
	}

	return viewData
}

// =============================================================================
// WIDGETS HELPERS
// =============================================================================

// widgetsBreadcrumbs returns breadcrumbs for the widgets list page.
func widgetsBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.widgets"), URL: "/admin/widgets", Active: true},
	}
}

// convertWidgetsViewData converts handler WidgetsListData to view WidgetsViewData.
func convertWidgetsViewData(data WidgetsListData) adminviews.WidgetsViewData {
	var areas []adminviews.WidgetAreaView
	for _, wa := range data.WidgetAreas {
		area := adminviews.WidgetAreaView{
			AreaID:          wa.Area.ID,
			AreaName:        wa.Area.Name,
			AreaDescription: wa.Area.Description,
		}
		for _, w := range wa.Widgets {
			area.Widgets = append(area.Widgets, adminviews.WidgetItemView{
				ID:         w.ID,
				WidgetType: w.WidgetType,
				Title:      w.Title.String,
				HasTitle:   w.Title.Valid && w.Title.String != "",
			})
		}
		areas = append(areas, area)
	}

	var types []adminviews.WidgetTypeView
	for _, wt := range data.WidgetTypes {
		types = append(types, adminviews.WidgetTypeView{
			ID:          wt.ID,
			Name:        wt.Name,
			Description: wt.Description,
		})
	}

	themeName := ""
	if data.Theme != nil {
		themeName = data.Theme.Name
	}

	return adminviews.WidgetsViewData{
		ThemeName:   themeName,
		WidgetAreas: areas,
		WidgetTypes: types,
	}
}

// =============================================================================
// CONFIG HELPERS
// =============================================================================

// convertConfigViewData converts handler ConfigFormData to view ConfigViewData.
func convertConfigViewData(data ConfigFormData) adminviews.ConfigViewData {
	var items []adminviews.ConfigItemView
	for _, item := range data.Items {
		items = append(items, adminviews.ConfigItemView{
			Key:         item.Key,
			Value:       item.Value,
			Type:        item.Type,
			Description: item.Description,
			Label:       item.Label,
		})
	}

	var transItems []adminviews.TranslatableConfigItemView
	for _, item := range data.TranslatableItems {
		ti := adminviews.TranslatableConfigItemView{
			Key:         item.Key,
			Label:       item.Label,
			Description: item.Description,
			Type:        item.Type,
		}
		for _, tr := range item.Translations {
			ti.Translations = append(ti.Translations, adminviews.ConfigTranslationValueView{
				LanguageCode: tr.LanguageCode,
				LanguageName: tr.LanguageName,
				Value:        tr.Value,
			})
		}
		transItems = append(transItems, ti)
	}

	return adminviews.ConfigViewData{
		Items:                items,
		TranslatableItems:    transItems,
		Errors:               data.Errors,
		HasMultipleLanguages: data.HasMultipleLanguages,
	}
}

// =============================================================================
// WEBHOOKS HELPERS
// =============================================================================

// webhooksBreadcrumbs returns breadcrumbs for the webhooks list page.
func webhooksBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.webhooks"), URL: redirectAdminWebhooks, Active: true},
	}
}

// webhookNewBreadcrumbs returns breadcrumbs for the new webhook form.
func webhookNewBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.webhooks"), URL: redirectAdminWebhooks},
		{Label: i18n.T(lang, "webhooks.new"), URL: redirectAdminWebhooksNew, Active: true},
	}
}

// webhookEditBreadcrumbs returns breadcrumbs for the edit webhook form.
func webhookEditBreadcrumbs(lang string, webhookName string, webhookID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.webhooks"), URL: redirectAdminWebhooks},
		{Label: webhookName, URL: fmt.Sprintf(redirectAdminWebhooksID, webhookID), Active: true},
	}
}

// webhookDeliveriesBreadcrumbs returns breadcrumbs for the deliveries page.
func webhookDeliveriesBreadcrumbs(lang string, webhook store.Webhook) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.webhooks"), URL: redirectAdminWebhooks},
		{Label: webhook.Name, URL: fmt.Sprintf(redirectAdminWebhooksID, webhook.ID)},
		{Label: i18n.T(lang, "webhooks.deliveries_title"), URL: fmt.Sprintf(redirectAdminWebhooksIDDeliveries, webhook.ID), Active: true},
	}
}

// convertWebhooksListViewData converts handler WebhooksListData to view WebhooksListViewData.
func convertWebhooksListViewData(data WebhooksListData) adminviews.WebhooksListViewData {
	var items []adminviews.WebhookListItemView
	for _, wh := range data.Webhooks {
		item := adminviews.WebhookListItemView{
			ID:             wh.ID,
			Name:           wh.Name,
			URL:            wh.Url,
			Events:         wh.Events,
			IsActive:       wh.IsActive,
			TotalDelivered: wh.TotalDelivered,
			TotalPending:   wh.TotalPending,
			TotalDead:      wh.TotalDead,
			SuccessRate:    wh.SuccessRate,
			HealthStatus:   wh.HealthStatus,
		}
		if wh.LastSuccessfulAt != nil {
			item.HasLastSuccessfulAt = true
			item.LastSuccessfulAt = wh.LastSuccessfulAt.Format("Jan 2, 15:04")
		}
		items = append(items, item)
	}

	return adminviews.WebhooksListViewData{
		Webhooks:      items,
		TotalWebhooks: data.TotalWebhooks,
	}
}

// convertWebhookFormViewData converts handler WebhookFormData to view WebhookFormViewData.
func convertWebhookFormViewData(data WebhookFormData) adminviews.WebhookFormViewData {
	var events []adminviews.WebhookEventInfoView
	for _, ev := range data.Events {
		events = append(events, adminviews.WebhookEventInfoView{
			Type:        ev.Type,
			Description: ev.Description,
		})
	}

	viewData := adminviews.WebhookFormViewData{
		IsEdit:      data.IsEdit,
		Events:      events,
		Errors:      data.Errors,
		FormValues:  data.FormValues,
		FormEvents:  data.FormEvents,
		FormHeaders: data.FormHeaders,
	}

	if data.Webhook != nil {
		viewData.WebhookID = data.Webhook.ID
		viewData.CreatedAt = data.Webhook.CreatedAt.Format("Jan 2, 2006 3:04 PM")
		viewData.UpdatedAt = data.Webhook.UpdatedAt.Format("Jan 2, 2006 3:04 PM")
	}

	return viewData
}

// convertWebhookDeliveriesViewData converts handler WebhookDeliveriesData to view.
func convertWebhookDeliveriesViewData(data WebhookDeliveriesData) adminviews.WebhookDeliveriesViewData {
	var deliveries []adminviews.WebhookDeliveryView
	for _, d := range data.Deliveries {
		dv := adminviews.WebhookDeliveryView{
			ID:        d.ID,
			Event:     d.Event,
			Status:    d.Status,
			Attempts:  d.Attempts,
			Payload:   d.Payload,
			CreatedAt: d.CreatedAt.Format("Jan 2, 15:04:05"),
			UpdatedAt: d.UpdatedAt.Format("Jan 2, 15:04:05"),
			CanRetry:  d.Status == "dead" || d.Status == "failed",
		}
		if d.ResponseCode.Valid {
			dv.HasResponseCode = true
			dv.ResponseCode = d.ResponseCode.Int64
		}
		if d.ErrorMessage.Valid && d.ErrorMessage.String != "" {
			dv.HasErrorMessage = true
			dv.ErrorMessage = d.ErrorMessage.String
		}
		if d.ResponseBody.Valid && d.ResponseBody.String != "" {
			dv.HasResponseBody = true
			dv.ResponseBody = d.ResponseBody.String
		}
		if d.DeliveredAt.Valid {
			dv.HasDeliveredAt = true
			dv.DeliveredAt = d.DeliveredAt.Time.Format("Jan 2, 15:04:05")
		}
		if d.NextRetryAt.Valid {
			dv.HasNextRetryAt = true
			dv.NextRetryAt = d.NextRetryAt.Time.Format("Jan 2, 15:04:05")
		}
		deliveries = append(deliveries, dv)
	}

	return adminviews.WebhookDeliveriesViewData{
		WebhookID:   data.Webhook.ID,
		WebhookName: data.Webhook.Name,
		Deliveries:  deliveries,
		TotalCount:  data.TotalCount,
		Pagination:  convertPagination(data.Pagination),
	}
}

// =============================================================================
// PAGES HELPERS
// =============================================================================

// pagesBreadcrumbs returns breadcrumbs for the pages list page.
func pagesBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "pages.title"), URL: redirectAdminPages, Active: true},
	}
}

// pagesNewBreadcrumbs returns breadcrumbs for the new page form.
func pagesNewBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "pages.title"), URL: redirectAdminPages},
		{Label: i18n.T(lang, "pages.new"), URL: redirectAdminPagesNew, Active: true},
	}
}

// pagesEditBreadcrumbs returns breadcrumbs for the edit page form.
func pagesEditBreadcrumbs(lang string, pageTitle string, pageID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "pages.title"), URL: redirectAdminPages},
		{Label: pageTitle, URL: fmt.Sprintf(redirectAdminPagesID, pageID), Active: true},
	}
}

// pagesVersionsBreadcrumbs returns breadcrumbs for the page versions page.
func pagesVersionsBreadcrumbs(lang string, pageTitle string, pageID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "pages.title"), URL: redirectAdminPages},
		{Label: pageTitle, URL: fmt.Sprintf(redirectAdminPagesID, pageID)},
		{Label: i18n.T(lang, "versions.title"), URL: fmt.Sprintf(redirectAdminPagesIDVersions, pageID), Active: true},
	}
}

// convertPagesListViewData converts handler PagesListData to view PagesListViewData.
func convertPagesListViewData(data PagesListData, renderer *render.Renderer, lang string) adminviews.PagesListViewData {
	var pages []adminviews.PageListItemView
	for _, p := range data.Pages {
		item := adminviews.PageListItemView{
			ID:        p.ID,
			Title:     p.Title,
			Slug:      p.Slug,
			Status:    p.Status,
			UpdatedAt: renderer.FormatDateTimeLocale(p.UpdatedAt, lang),
		}

		// Scheduled check
		if p.Status == "draft" && p.ScheduledAt.Valid {
			item.IsScheduled = true
			item.ScheduledAtTitle = i18n.T(lang, "status.scheduled") + " " + renderer.FormatDateTimeLocale(p.ScheduledAt.Time, lang)
		}

		// Featured image
		if img, ok := data.PageFeaturedImages[p.ID]; ok && img != nil {
			item.FeaturedImage = &adminviews.PageFeaturedImageView{
				Thumbnail: img.Thumbnail,
			}
		}

		// Tags
		if tags, ok := data.PageTags[p.ID]; ok {
			for _, t := range tags {
				item.Tags = append(item.Tags, adminviews.PageTagView{Name: t.Name})
			}
		}

		// Categories
		if cats, ok := data.PageCategories[p.ID]; ok {
			for _, c := range cats {
				item.Categories = append(item.Categories, adminviews.PageCategoryView{Name: c.Name})
			}
		}

		// Language
		if l, ok := data.PageLanguages[p.ID]; ok && l != nil {
			item.Language = &adminviews.PageLanguageView{Code: l.Code, Name: l.Name}
		}

		// Demo mode check
		item.IsDemoPublished = middleware.IsDemoMode() && p.Status == "published"

		pages = append(pages, item)
	}

	return adminviews.PagesListViewData{
		Pages:          pages,
		TotalCount:     data.TotalCount,
		StatusFilter:   data.StatusFilter,
		CategoryFilter: data.CategoryFilter,
		LanguageFilter: data.LanguageFilter,
		SearchFilter:   data.SearchFilter,
		AllCategories:  convertPageCategoryNodes(data.AllCategories),
		AllLanguages:   convertLanguageOptions(data.AllLanguages),
		Statuses:       data.Statuses,
		Pagination:     convertPagination(data.Pagination),
		IsDemoMode:     middleware.IsDemoMode(),
	}
}

// convertPageCategoryNodes converts handler PageCategoryNode slice to view.
func convertPageCategoryNodes(nodes []PageCategoryNode) []adminviews.PageCategoryNodeView {
	var result []adminviews.PageCategoryNodeView
	for _, n := range nodes {
		desc := ""
		if n.Category.Description.Valid {
			desc = n.Category.Description.String
		}
		result = append(result, adminviews.PageCategoryNodeView{
			ID:          n.Category.ID,
			Name:        n.Category.Name,
			Description: desc,
			Depth:       n.Depth,
		})
	}
	return result
}

// convertPageFormViewData converts handler PageFormData to view PageFormViewData.
func convertPageFormViewData(data PageFormData, renderer *render.Renderer, lang string) adminviews.PageFormViewData {
	viewData := adminviews.PageFormViewData{
		IsEdit:               data.IsEdit,
		Statuses:             data.Statuses,
		PageTypes:            data.PageTypes,
		AllCategories:        convertPageCategoryNodes(data.AllCategories),
		Errors:               data.Errors,
		FormValues:           data.FormValues,
		HasMultipleLanguages: len(data.AllLanguages) > 1,
		AllLanguages:         convertLanguageOptions(data.AllLanguages),
		Language:             convertLanguageOptionPtr(data.Language),
		IsDemoMode:           middleware.IsDemoMode(),
	}

	if data.Page != nil {
		viewData.PageID = data.Page.ID
		viewData.PageTitle = data.Page.Title
		viewData.PageSlug = data.Page.Slug
		viewData.PageBody = data.Page.Body
		viewData.PageStatus = data.Page.Status
		viewData.PageType = data.Page.PageType
		viewData.MetaTitle = data.Page.MetaTitle
		viewData.MetaDescription = data.Page.MetaDescription
		viewData.MetaKeywords = data.Page.MetaKeywords
		viewData.CanonicalURL = data.Page.CanonicalUrl
		viewData.NoIndex = data.Page.NoIndex == 1
		viewData.NoFollow = data.Page.NoFollow == 1
		viewData.HideFeaturedImage = data.Page.HideFeaturedImage == 1
		viewData.ExcludeFromLists = data.Page.ExcludeFromLists == 1

		if data.Page.OgImageID.Valid {
			viewData.OgImageID = fmt.Sprintf("%d", data.Page.OgImageID.Int64)
		}

		if data.Page.ScheduledAt.Valid {
			viewData.HasScheduledAt = true
			viewData.ScheduledAt = data.Page.ScheduledAt.Time.Format("2006-01-02T15:04")
			viewData.ScheduledAtFmt = renderer.FormatDateTimeLocale(data.Page.ScheduledAt.Time, lang)
		}
	}

	// Featured image
	if data.FeaturedImage != nil {
		viewData.FeaturedImage = &adminviews.PageFormFeaturedImageView{
			ID:        data.FeaturedImage.ID,
			Filename:  data.FeaturedImage.Filename,
			Filepath:  data.FeaturedImage.Filepath,
			Thumbnail: data.FeaturedImage.Thumbnail,
			Mimetype:  data.FeaturedImage.Mimetype,
		}
	}

	// Tags
	for _, t := range data.Tags {
		viewData.Tags = append(viewData.Tags, adminviews.PageFormTagView{
			ID:   t.ID,
			Name: t.Name,
			Slug: t.Slug,
		})
	}

	// Categories (selected)
	for _, c := range data.Categories {
		viewData.Categories = append(viewData.Categories, adminviews.PageFormCategoryView{
			ID: c.ID,
		})
	}

	// Aliases
	for _, a := range data.Aliases {
		viewData.Aliases = append(viewData.Aliases, adminviews.PageFormAliasView{
			ID:    a.ID,
			Alias: a.Alias,
		})
	}

	// Translations
	for _, tr := range data.Translations {
		viewData.Translations = append(viewData.Translations, adminviews.PageTranslationView{
			Language: convertLanguageOption(tr.Language),
			PageID:   tr.Page.ID,
			Title:    tr.Page.Title,
			Status:   tr.Page.Status,
		})
	}

	// Missing languages
	for _, l := range data.MissingLanguages {
		viewData.MissingLanguages = append(viewData.MissingLanguages, convertLanguageOption(l))
	}

	return viewData
}

// convertPageVersionsViewData converts handler PageVersionsData to view.
func convertPageVersionsViewData(data PageVersionsData, renderer *render.Renderer, lang string) adminviews.PageVersionsViewData {
	var versions []adminviews.PageVersionView
	for _, v := range data.Versions {
		versions = append(versions, adminviews.PageVersionView{
			ID:            v.ID,
			Title:         v.Title,
			Body:          v.Body,
			ChangedByName: v.ChangedByName,
			CreatedAt:     renderer.FormatDateTimeLocale(v.CreatedAt, lang),
		})
	}

	return adminviews.PageVersionsViewData{
		PageID:     data.Page.ID,
		PageTitle:  data.Page.Title,
		TotalCount: data.TotalCount,
		Versions:   versions,
		Pagination: convertPagination(data.Pagination),
	}
}

// =============================================================================
// SCHEDULER HELPERS
// =============================================================================

// schedulerBreadcrumbs returns breadcrumbs for the scheduler list page.
func schedulerBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "scheduler.title"), URL: redirectAdminScheduler, Active: true},
	}
}

// schedulerTaskFormBreadcrumbs returns breadcrumbs for the task form page.
func schedulerTaskFormBreadcrumbs(lang string, title string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "scheduler.title"), URL: redirectAdminScheduler},
		{Label: title, Active: true},
	}
}

// schedulerTaskRunsBreadcrumbs returns breadcrumbs for the task runs page.
func schedulerTaskRunsBreadcrumbs(lang string, taskName string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "scheduler.title"), URL: redirectAdminScheduler},
		{Label: taskName},
		{Label: i18n.T(lang, "scheduler.task_runs"), Active: true},
	}
}

// convertSchedulerListViewData converts handler data to view SchedulerListViewData.
func convertSchedulerListViewData(data SchedulerListData) adminviews.SchedulerListViewData {
	var jobs []adminviews.SchedulerJobViewItem
	for _, j := range data.Jobs {
		jobs = append(jobs, adminviews.SchedulerJobViewItem{
			Source:          j.Source,
			Name:            j.Name,
			Description:     j.Description,
			DefaultSchedule: j.DefaultSchedule,
			Schedule:        j.Schedule,
			IsOverridden:    j.IsOverridden,
			LastRun:         j.LastRun,
			NextRun:         j.NextRun,
			CanTrigger:      j.CanTrigger,
		})
	}

	var tasks []adminviews.SchedulerTaskViewItem
	for _, t := range data.Tasks {
		tasks = append(tasks, adminviews.SchedulerTaskViewItem{
			ID:       t.ID,
			Name:     t.Name,
			URL:      t.URL,
			Schedule: t.Schedule,
			IsActive: t.IsActive,
			LastRun:  t.LastRun,
		})
	}

	return adminviews.SchedulerListViewData{
		Jobs:       jobs,
		Tasks:      tasks,
		IsDemoMode: middleware.IsDemoMode(),
	}
}

// convertSchedulerTaskFormViewData converts store.ScheduledTask to view SchedulerTaskFormViewData.
func convertSchedulerTaskFormViewData(task store.ScheduledTask, isEdit bool) adminviews.SchedulerTaskFormViewData {
	timeout := task.TimeoutSeconds
	if timeout == 0 {
		timeout = 30
	}
	return adminviews.SchedulerTaskFormViewData{
		TaskID:         task.ID,
		Name:           task.Name,
		URL:            task.Url,
		Schedule:       task.Schedule,
		TimeoutSeconds: timeout,
		IsEdit:         isEdit,
		IsDemoMode:     middleware.IsDemoMode(),
	}
}

// convertSchedulerTaskRunsViewData converts handler data to view SchedulerTaskRunsViewData.
func convertSchedulerTaskRunsViewData(task store.ScheduledTask, runs []store.ScheduledTaskRun, totalCount int64, pagination AdminPagination) adminviews.SchedulerTaskRunsViewData {
	var viewRuns []adminviews.SchedulerTaskRunView
	for _, r := range runs {
		vr := adminviews.SchedulerTaskRunView{
			Status:    r.Status,
			StartedAt: r.StartedAt.Format("2006-01-02 15:04:05"),
		}
		if r.StatusCode.Valid {
			vr.HasStatusCode = true
			vr.StatusCode = r.StatusCode.Int64
		}
		if r.DurationMs.Valid {
			vr.HasDuration = true
			vr.DurationMs = r.DurationMs.Int64
		}
		if r.ErrorMessage.Valid && r.ErrorMessage.String != "" {
			vr.HasErrorMessage = true
			vr.ErrorMessage = r.ErrorMessage.String
		}
		viewRuns = append(viewRuns, vr)
	}

	return adminviews.SchedulerTaskRunsViewData{
		TaskID:       task.ID,
		TaskName:     task.Name,
		TaskURL:      task.Url,
		TaskSchedule: task.Schedule,
		TaskTimeout:  task.TimeoutSeconds,
		TaskIsActive: task.IsActive == 1,
		TotalCount:   totalCount,
		Runs:         viewRuns,
		Pagination:   convertPagination(pagination),
		IsDemoMode:   middleware.IsDemoMode(),
	}
}

// =============================================================================
// Media helpers
// =============================================================================

// mediaBreadcrumbs returns breadcrumbs for the media library page.
func mediaBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.media"), URL: redirectAdminMedia, Active: true},
	}
}

// mediaUploadBreadcrumbs returns breadcrumbs for the media upload page.
func mediaUploadBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.media"), URL: redirectAdminMedia},
		{Label: i18n.T(lang, "media.upload"), URL: redirectAdminMediaUpload, Active: true},
	}
}

// mediaEditBreadcrumbs returns breadcrumbs for the media edit page.
func mediaEditBreadcrumbs(lang string, filename string, mediaID int64) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.media"), URL: redirectAdminMedia},
		{Label: filename, URL: fmt.Sprintf(redirectAdminMediaID, mediaID), Active: true},
	}
}

// convertMediaLibraryViewData converts handler data to view data for the media library.
func convertMediaLibraryViewData(data MediaLibraryData) adminviews.MediaLibraryViewData {
	viewMedia := make([]adminviews.MediaItemView, len(data.Media))
	for i, m := range data.Media {
		viewMedia[i] = adminviews.MediaItemView{
			ID:           m.ID,
			Filename:     m.Filename,
			Alt:          m.Alt.String,
			ThumbnailURL: m.ThumbnailURL,
			OriginalURL:  m.OriginalURL,
			IsImage:      m.IsImage,
			TypeIcon:     m.TypeIcon,
			Size:         render.FormatBytes(m.Medium.Size),
		}
	}

	viewFolders := make([]adminviews.MediaFolderView, len(data.Folders))
	for i, f := range data.Folders {
		viewFolders[i] = adminviews.MediaFolderView{
			ID:        f.ID,
			Name:      f.Name,
			HasParent: f.ParentID.Valid,
		}
	}

	return adminviews.MediaLibraryViewData{
		Media:      viewMedia,
		Folders:    viewFolders,
		TotalCount: data.TotalCount,
		Filter:     data.Filter,
		FolderID:   data.FolderID,
		Search:     data.Search,
		Pagination: convertPagination(data.Pagination),
	}
}

// convertMediaUploadViewData converts handler data to view data for the media upload page.
func convertMediaUploadViewData(data UploadFormData, lang string) adminviews.MediaUploadViewData {
	viewFolders := make([]adminviews.MediaFolderView, len(data.Folders))
	for i, f := range data.Folders {
		viewFolders[i] = adminviews.MediaFolderView{
			ID:   f.ID,
			Name: f.Name,
		}
	}

	return adminviews.MediaUploadViewData{
		Folders:          viewFolders,
		MaxSize:          data.MaxSize,
		MaxSizeFormatted: render.FormatBytes(data.MaxSize),
		AllowedExt:       data.AllowedExt,
		FormatsHint:      i18n.T(lang, "media.supported_formats"),
	}
}

// convertMediaEditViewData converts handler data to view data for the media edit page.
func convertMediaEditViewData(data MediaEditData, renderer *render.Renderer, lang string) adminviews.MediaEditViewData {
	media := adminviews.MediaItemView{
		ID:            data.Media.ID,
		Filename:      data.Media.Filename,
		Alt:           data.Media.Alt.String,
		ThumbnailURL:  data.Media.ThumbnailURL,
		OriginalURL:   data.Media.OriginalURL,
		IsImage:       data.Media.IsImage,
		TypeIcon:      data.Media.TypeIcon,
		Size:          render.FormatBytes(data.Media.Medium.Size),
		MimeType:      data.Media.MimeType,
		HasDimensions: data.Media.Width.Valid,
		Width:         data.Media.Width.Int64,
		Height:        data.Media.Height.Int64,
		CreatedAt:     render.FormatDateTime(data.Media.CreatedAt),
		UUID:          data.Media.Uuid,
		FolderID:      data.Media.FolderID.Int64,
		HasFolderID:   data.Media.FolderID.Valid,
	}

	viewVariants := make([]adminviews.MediaVariantView, len(data.Variants))
	for i, v := range data.Variants {
		typeLabel := v.Type
		switch v.Type {
		case "thumbnail":
			typeLabel = i18n.T(lang, "media.variant_thumbnail")
		case "medium":
			typeLabel = i18n.T(lang, "media.variant_medium")
		case "large":
			typeLabel = i18n.T(lang, "media.variant_large")
		}
		viewVariants[i] = adminviews.MediaVariantView{
			Type:      v.Type,
			TypeLabel: typeLabel,
			Width:     v.Width,
			Height:    v.Height,
			Size:      render.FormatBytes(v.Size),
		}
	}

	viewFolders := make([]adminviews.MediaFolderView, len(data.Folders))
	for i, f := range data.Folders {
		viewFolders[i] = adminviews.MediaFolderView{
			ID:   f.ID,
			Name: f.Name,
		}
	}

	viewLanguages := make([]adminviews.MediaLanguageView, len(data.Languages))
	for i, l := range data.Languages {
		viewLanguages[i] = adminviews.MediaLanguageView{
			Code:       l.Code,
			NativeName: l.NativeName,
		}
	}

	viewTranslations := make(map[string]adminviews.MediaTranslationView)
	for code, t := range data.Translations {
		viewTranslations[code] = adminviews.MediaTranslationView{
			Alt:     t.Alt,
			Caption: t.Caption,
		}
	}

	return adminviews.MediaEditViewData{
		Media:            media,
		Variants:         viewVariants,
		Folders:          viewFolders,
		CurrentFolderID:  data.Media.FolderID.Int64,
		HasCurrentFolder: data.Media.FolderID.Valid,
		Languages:        viewLanguages,
		Translations:     viewTranslations,
		Errors:           data.Errors,
		FormValues:       data.FormValues,
	}
}
