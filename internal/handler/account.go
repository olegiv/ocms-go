// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"

	"github.com/olegiv/ocms-go/internal/i18n"
	accountviews "github.com/olegiv/ocms-go/internal/views/account"
)

// Account renders a read-only overview of the authenticated user's account.
// Auth and LoadUser middleware must run before this handler.
func (h *AuthHandler) Account(w http.ResponseWriter, r *http.Request) {
	user := accountUser(w, r)
	if user == nil {
		return
	}

	pc := h.accountPageContext(r, user, "account.title")
	data := accountviews.Data{
		CreatedAt:   h.renderer.FormatDateTimeLocale(user.CreatedAt, pc.AdminLang),
		LastLoginAt: i18n.T(pc.AdminLang, "account.no_login"),
	}
	if user.LastLoginAt.Valid {
		data.LastLoginAt = h.renderer.FormatDateTimeLocale(user.LastLoginAt.Time, pc.AdminLang)
	}
	renderTempl(w, r, accountviews.Page(pc, data))
}
