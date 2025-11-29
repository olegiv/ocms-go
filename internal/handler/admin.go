// Package handler implements HTTP handlers for the admin interface,
// including user management, page editing, configuration, and authentication.
package handler

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
)

// DashboardStats holds the statistics displayed on the dashboard.
type DashboardStats struct {
	TotalPages     int64
	PublishedPages int64
	DraftPages     int64
	TotalUsers     int64
}

// AdminHandler handles admin routes.
type AdminHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewAdminHandler creates a new AdminHandler.
func NewAdminHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *AdminHandler {
	return &AdminHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// Dashboard renders the admin dashboard with stats and recent activity.
func (h *AdminHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Fetch stats
	stats := DashboardStats{}

	// Get user count
	userCount, err := h.queries.CountUsers(r.Context())
	if err != nil {
		slog.Error("failed to count users", "error", err)
	} else {
		stats.TotalUsers = userCount
	}

	// Page stats will be 0 until pages are implemented in Iteration 11
	// TODO: Add page stats once pages are implemented
	stats.TotalPages = 0
	stats.PublishedPages = 0
	stats.DraftPages = 0

	if err := h.renderer.Render(w, r, "admin/dashboard", render.TemplateData{
		Title: "Dashboard",
		User:  user,
		Data:  stats,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
