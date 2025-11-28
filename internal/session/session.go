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

	return sm
}
