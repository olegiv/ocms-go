// Package handler implements HTTP handlers for the admin interface,
// including user management, page editing, configuration, and authentication.
package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/cache"
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
	// Webhook stats
	TotalWebhooks       int64
	ActiveWebhooks      int64
	FailedDeliveries24h int64
	// Phase 4 additions
	TotalLanguages   int64
	ActiveLanguages  int64
	CacheHitRate     float64
	CacheHits        int64
	CacheMisses      int64
	CacheItems       int
	CacheBackendType string // "memory" or "redis"
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

// WebhookHealthItem represents a webhook's health for dashboard display.
type WebhookHealthItem struct {
	ID           int64
	Name         string
	IsActive     bool
	HealthStatus string // "green", "yellow", "red", "unknown"
	SuccessRate  float64
}

// RecentFailedDelivery represents a recent failed webhook delivery.
type RecentFailedDelivery struct {
	ID          int64
	WebhookID   int64
	WebhookName string
	Event       string
	Status      string
	CreatedAt   string
}

// TranslationCoverage represents translation coverage for a specific language.
type TranslationCoverage struct {
	LanguageID   int64
	LanguageCode string
	LanguageName string
	TotalPages   int64
	IsDefault    bool
}

// DashboardData holds all dashboard data including stats and recent items.
type DashboardData struct {
	Stats                  DashboardStats
	RecentSubmissions      []RecentSubmission
	WebhookHealth          []WebhookHealthItem
	RecentFailedDeliveries []RecentFailedDelivery
	TranslationCoverage    []TranslationCoverage
}

// AdminHandler handles admin routes.
type AdminHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	cacheManager   *cache.Manager
}

// NewAdminHandler creates a new AdminHandler.
func NewAdminHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, cacheManager *cache.Manager) *AdminHandler {
	return &AdminHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		cacheManager:   cacheManager,
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

	// Get webhook stats
	if webhookCount, err := h.queries.CountWebhooks(ctx); err != nil {
		slog.Error("failed to count webhooks", "error", err)
	} else {
		stats.TotalWebhooks = webhookCount
	}

	if activeWebhooks, err := h.queries.CountActiveWebhooks(ctx); err != nil {
		slog.Error("failed to count active webhooks", "error", err)
	} else {
		stats.ActiveWebhooks = activeWebhooks
	}

	// Get webhook health summary
	var webhookHealth []WebhookHealthItem
	last24h := time.Now().Add(-24 * time.Hour)
	if healthSummary, err := h.queries.GetWebhookHealthSummary(ctx, last24h); err != nil {
		slog.Error("failed to get webhook health summary", "error", err)
	} else {
		for _, wh := range healthSummary {
			delivered := int64(0)
			if wh.DeliveredCount.Valid {
				delivered = int64(wh.DeliveredCount.Float64)
			}
			dead := int64(0)
			if wh.DeadCount.Valid {
				dead = int64(wh.DeadCount.Float64)
			}

			// Calculate success rate
			var successRate float64
			if wh.TotalDeliveries > 0 {
				successRate = float64(delivered) / float64(wh.TotalDeliveries) * 100
			}

			// Calculate health status
			healthStatus := "unknown"
			if wh.TotalDeliveries > 0 {
				if successRate >= 95 {
					healthStatus = "green"
				} else if successRate >= 80 {
					healthStatus = "yellow"
				} else {
					healthStatus = "red"
				}
			}

			webhookHealth = append(webhookHealth, WebhookHealthItem{
				ID:           wh.ID,
				Name:         wh.Name,
				IsActive:     wh.IsActive,
				HealthStatus: healthStatus,
				SuccessRate:  successRate,
			})

			// Count failed deliveries
			stats.FailedDeliveries24h += dead
		}
	}

	// Get recent failed deliveries
	var recentFailedDeliveries []RecentFailedDelivery
	if failedDeliveries, err := h.queries.GetRecentFailedDeliveriesWithWebhook(ctx, 5); err != nil {
		slog.Error("failed to get recent failed deliveries", "error", err)
	} else {
		for _, d := range failedDeliveries {
			recentFailedDeliveries = append(recentFailedDeliveries, RecentFailedDelivery{
				ID:          d.ID,
				WebhookID:   d.WebhookID,
				WebhookName: d.WebhookName,
				Event:       d.Event,
				Status:      d.Status,
				CreatedAt:   d.CreatedAt.Format("Jan 2, 2006 3:04 PM"),
			})
		}
	}

	// Get language counts
	if langCount, err := h.queries.CountLanguages(ctx); err != nil {
		slog.Error("failed to count languages", "error", err)
	} else {
		stats.TotalLanguages = langCount
	}

	if activeLangCount, err := h.queries.CountActiveLanguages(ctx); err != nil {
		slog.Error("failed to count active languages", "error", err)
	} else {
		stats.ActiveLanguages = activeLangCount
	}

	// Get cache stats
	if h.cacheManager != nil {
		cacheStats := h.cacheManager.TotalStats()
		stats.CacheHitRate = cacheStats.HitRate
		stats.CacheHits = cacheStats.Hits
		stats.CacheMisses = cacheStats.Misses
		stats.CacheItems = cacheStats.Items
		if h.cacheManager.IsRedis() {
			stats.CacheBackendType = "redis"
		} else {
			stats.CacheBackendType = "memory"
		}
	}

	// Get translation coverage
	var translationCoverage []TranslationCoverage
	if coverage, err := h.queries.GetTranslationCoverage(ctx); err != nil {
		slog.Error("failed to get translation coverage", "error", err)
	} else {
		for _, c := range coverage {
			translationCoverage = append(translationCoverage, TranslationCoverage{
				LanguageID:   c.LanguageID,
				LanguageCode: c.LanguageCode,
				LanguageName: c.LanguageName,
				TotalPages:   c.PageCount,
				IsDefault:    c.IsDefault,
			})
		}
	}

	// Build dashboard data
	dashboardData := DashboardData{
		Stats:                  stats,
		RecentSubmissions:      recentSubmissions,
		WebhookHealth:          webhookHealth,
		RecentFailedDeliveries: recentFailedDeliveries,
		TranslationCoverage:    translationCoverage,
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
