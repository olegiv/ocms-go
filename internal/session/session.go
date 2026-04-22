// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package session provides HTTP session management using SCS with SQLite storage.
// Sessions are used for user authentication and flash messages.
package session

import (
	"database/sql"
	"encoding/gob"
	"net/http"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
)

func init() {
	// Register types that may be stored in sessions.
	// This is required by gob encoding used by the session store.
	gob.Register(map[string]string{})
}

// New creates a new session manager configured with SQLite store.
func New(db *sql.DB, isDev bool) *scs.SessionManager {
	sm := scs.New()

	// Use SQLite store
	sm.Store = sqlite3store.New(db)

	// Configure session.
	//
	// SameSite=Strict refuses to send the session cookie on any cross-site
	// top-level navigation, so a crafted link in an email or chat message
	// cannot piggyback on an authenticated admin session. The trade-off is
	// that arriving at /admin/... via an external link lands the user
	// logged-out; that is the desired behavior for an admin panel, and
	// public routes do not rely on cross-site session continuity.
	// All mutating admin actions use POST (verified in main.go route
	// registrations), so idempotent GETs see no functional change.
	sm.Lifetime = 24 * time.Hour
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteStrictMode
	sm.Cookie.Secure = !isDev // Secure cookies in production only

	// Production hardening: Use __Host- prefix
	// This requires Secure=true, Path=/, and no Domain attribute
	// Prevents subdomain and path-based cookie attacks
	if !isDev {
		sm.Cookie.Name = "__Host-session"
		sm.Cookie.Path = "/"
	}

	return sm
}
