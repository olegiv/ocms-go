// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"errors"
	"net/http"

	"github.com/olegiv/ocms-go/internal/connectedapps"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	accountviews "github.com/olegiv/ocms-go/internal/views/account"
)

// ConnectedApps lists only the authenticated account's connection metadata.
func (h *AuthHandler) ConnectedApps(w http.ResponseWriter, r *http.Request) {
	// The language form returns via a validated same-origin Referer.
	w.Header().Set("Referrer-Policy", "same-origin")
	u := accountUser(w, r)
	if u == nil {
		return
	}
	pages, err := connectedapps.ParsePagination(r.URL.Query())
	if err != nil {
		http.Error(w, "Invalid page", http.StatusBadRequest)
		return
	}
	page, err := h.connections.List(r.Context(), connectedapps.Actor{UserID: u.ID, SessionVersion: u.SessionVersion}, false, pages, h.oauth != nil)
	if err != nil {
		connectedAppsError(w, err)
		return
	}
	renderTempl(w, r, accountviews.ConnectionsPage(h.accountPageContext(r, u, "connected.title"),
		accountviews.ConnectionsData{Page: page, BasePath: RouteAccountConnectedApps}))
}

// RevokeConnectedApp removes only the authenticated account's selected access.
func (h *AuthHandler) RevokeConnectedApp(w http.ResponseWriter, r *http.Request) {
	u := accountUser(w, r)
	if u == nil {
		return
	}
	if demoGuard(w, r, h.renderer, middleware.RestrictionAPIKeys, RouteAccountConnectedApps) {
		return
	}
	kind, id, err := connectedapps.ParseRevoke(w, r)
	if err == nil {
		err = h.connections.Revoke(r.Context(), connectedapps.Actor{UserID: u.ID, SessionVersion: u.SessionVersion}, false, kind, id)
	}
	if err != nil {
		connectedAppsError(w, err)
		return
	}
	logSecurityEvent(r, h.eventService, model.EventLevelInfo, "Account revoked connected app access",
		map[string]any{"kind": kind, "connection_id": id})
	http.Redirect(w, r, RouteAccountConnectedApps, http.StatusSeeOther)
}

func connectedAppsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, connectedapps.ErrInvalid):
		http.Error(w, "Invalid connection request", http.StatusBadRequest)
	case errors.Is(err, connectedapps.ErrForbidden):
		http.Error(w, "Forbidden", http.StatusForbidden)
	case errors.Is(err, connectedapps.ErrNotFound):
		http.Error(w, "Not found", http.StatusNotFound)
	default:
		logAndInternalError(w, "connected app operation failed", "error", err)
	}
}
