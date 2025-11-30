// Package handler implements HTTP handlers for the admin interface,
// including user management, page editing, configuration, and authentication.
package handler

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
)

// DashboardStats holds the statistics displayed on the dashboard.
type DashboardStats struct {
	TotalPages        int64
	PublishedPages    int64
	DraftPages        int64
	TotalUsers        int64
	TotalMedia        int64
	TotalForms        int64
	UnreadSubmissions int64
}

// RecentSubmission represents a recent form submission for dashboard display.
type RecentSubmission struct {
	ID        int64
	FormID    int64
	FormName  string
	FormSlug  string
	IsRead    bool
	CreatedAt string
}

// DashboardData holds all dashboard data including stats and recent items.
type DashboardData struct {
	Stats             DashboardStats
	RecentSubmissions []RecentSubmission
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
	ctx := r.Context()

	// Fetch stats
	stats := DashboardStats{}

	// Get user count
	if userCount, err := h.queries.CountUsers(ctx); err != nil {
		slog.Error("failed to count users", "error", err)
	} else {
		stats.TotalUsers = userCount
	}

	// Get page counts
	if totalPages, err := h.queries.CountPages(ctx); err != nil {
		slog.Error("failed to count pages", "error", err)
	} else {
		stats.TotalPages = totalPages
	}

	if publishedPages, err := h.queries.CountPagesByStatus(ctx, "published"); err != nil {
		slog.Error("failed to count published pages", "error", err)
	} else {
		stats.PublishedPages = publishedPages
	}

	if draftPages, err := h.queries.CountPagesByStatus(ctx, "draft"); err != nil {
		slog.Error("failed to count draft pages", "error", err)
	} else {
		stats.DraftPages = draftPages
	}

	// Get media count
	if mediaCount, err := h.queries.CountMedia(ctx); err != nil {
		slog.Error("failed to count media", "error", err)
	} else {
		stats.TotalMedia = mediaCount
	}

	// Get forms count
	if formsCount, err := h.queries.CountForms(ctx); err != nil {
		slog.Error("failed to count forms", "error", err)
	} else {
		stats.TotalForms = formsCount
	}

	// Get unread submissions count
	if unreadCount, err := h.queries.CountAllUnreadSubmissions(ctx); err != nil {
		slog.Error("failed to count unread submissions", "error", err)
	} else {
		stats.UnreadSubmissions = unreadCount
	}

	// Get recent form submissions
	var recentSubmissions []RecentSubmission
	if submissions, err := h.queries.GetRecentSubmissionsWithForm(ctx, 5); err != nil {
		slog.Error("failed to get recent submissions", "error", err)
	} else {
		for _, s := range submissions {
			recentSubmissions = append(recentSubmissions, RecentSubmission{
				ID:        s.ID,
				FormID:    s.FormID,
				FormName:  s.FormName,
				FormSlug:  s.FormSlug,
				IsRead:    s.IsRead,
				CreatedAt: s.CreatedAt.Format("Jan 2, 2006 3:04 PM"),
			})
		}
	}

	// Build dashboard data
	dashboardData := DashboardData{
		Stats:             stats,
		RecentSubmissions: recentSubmissions,
	}

	if err := h.renderer.Render(w, r, "admin/dashboard", render.TemplateData{
		Title: "Dashboard",
		User:  user,
		Data:  dashboardData,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// SetLanguage changes the admin UI language preference.
// POST /admin/language
func (h *AdminHandler) SetLanguage(w http.ResponseWriter, r *http.Request) {
	lang := r.FormValue("lang")
	if lang == "" {
		lang = "en"
	}

	// Validate the language code
	if !i18n.IsSupported(lang) {
		lang = "en"
	}

	// Set the language preference in session
	h.renderer.SetAdminLang(r, lang)

	// Redirect back to the referring page, or dashboard if not available
	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = "/admin"
	}

	http.Redirect(w, r, referer, http.StatusSeeOther)
}
