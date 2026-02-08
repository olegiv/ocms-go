// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// demoGuard checks if the application is in demo mode and blocks the action if so.
// Returns true if the action was blocked (caller should return immediately).
// Returns false if the action is allowed to proceed.
//
// Usage:
//
//	func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
//	    if demoGuard(w, r, h.renderer, middleware.RestrictionCreateUser, "/admin/users") {
//	        return
//	    }
//	    // ... normal handler code
//	}
func demoGuard(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, restriction middleware.DemoRestriction, redirectURL string) bool {
	if !middleware.IsDemoMode() {
		return false
	}

	message := middleware.DemoModeMessageDetailed(restriction)
	flashError(w, r, renderer, redirectURL, message)
	return true
}

// demoGuardAPI checks if the application is in demo mode for API requests.
// Returns true if the action was blocked (caller should return immediately).
// Returns false if the action is allowed to proceed.
//
// Usage:
//
//	func (h *APIHandler) CreatePage(w http.ResponseWriter, r *http.Request) {
//	    if demoGuardAPI(w) {
//	        return
//	    }
//	    // ... normal handler code
//	}
func demoGuardAPI(w http.ResponseWriter) bool {
	if !middleware.IsDemoMode() {
		return false
	}

	message := middleware.DemoModeMessageDetailed(middleware.RestrictionContentReadOnly)
	http.Error(w, message, http.StatusForbidden)
	return true
}

// IsDemoMode returns true if the application is running in demo mode.
// This is a convenience wrapper around middleware.IsDemoMode().
func IsDemoMode() bool {
	return middleware.IsDemoMode()
}
