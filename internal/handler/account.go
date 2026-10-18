// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	accountviews "github.com/olegiv/ocms-go/internal/views/account"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// Account renders a read-only overview of the authenticated user's account.
// Auth and LoadUser middleware must run before this handler.
func (h *AuthHandler) Account(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	user := middleware.GetUser(r)
	if user == nil {
		http.Redirect(w, r, redirectLogin, http.StatusSeeOther)
		return
	}
	// Unknown roles share the public permission level, so require a known role.
	if !isValidRole(user.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	lang := middleware.GetAdminLang(r)
	pc := &adminviews.PageContext{
		Title:         i18n.T(lang, "account.title"),
		AccountLayout: true,
		AdminLang:     lang,
		SiteName:      middleware.GetSiteName(r),
		CurrentPath:   r.URL.Path,
		User:          adminviews.UserInfo{Name: user.Name, Email: user.Email},
		Flash:         h.sessionManager.PopString(r.Context(), "flash"),
		FlashType:     h.sessionManager.PopString(r.Context(), "flash_type"),
	}
	if pc.FlashType == "" {
		pc.FlashType = "info"
	}
	data := accountviews.Data{
		CreatedAt:   h.renderer.FormatDateTimeLocale(user.CreatedAt, lang),
		LastLoginAt: i18n.T(lang, "account.no_login"),
	}
	if user.LastLoginAt.Valid {
		data.LastLoginAt = h.renderer.FormatDateTimeLocale(user.LastLoginAt.Time, lang)
	}
	pc.LangOptions = adminLangOptions(h.renderer)
	renderTempl(w, r, accountviews.Page(pc, data))
}
