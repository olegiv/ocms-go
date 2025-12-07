// Package session provides HTTP session management using SCS with SQLite storage.
// Sessions are used for user authentication and flash messages.
package session

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
)

// New creates a new session manager configured with SQLite store.
func New(db *sql.DB, isDev bool) *scs.SessionManager {
	sm := scs.New()

	// Use SQLite store
	sm.Store = sqlite3store.New(db)

	// Configure session
	sm.Lifetime = 24 * time.Hour
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
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
