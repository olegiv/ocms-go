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

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
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
