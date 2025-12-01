package handler

import (
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/cache"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/service"
)

// CacheHandler handles cache management routes.
type CacheHandler struct {
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	cacheManager   *cache.Manager
	eventService   *service.EventService
}

// NewCacheHandler creates a new CacheHandler.
func NewCacheHandler(renderer *render.Renderer, sm *scs.SessionManager, cm *cache.Manager, es *service.EventService) *CacheHandler {
	return &CacheHandler{
		renderer:       renderer,
		sessionManager: sm,
		cacheManager:   cm,
		eventService:   es,
	}
}

// CacheStatsData holds data for the cache stats template.
type CacheStatsData struct {
	Caches     []cache.CacheStats
	TotalStats cache.Stats
}

// Stats handles GET /admin/cache - displays cache statistics.
func (h *CacheHandler) Stats(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if h.cacheManager == nil {
		h.renderer.SetFlash(r, "Cache system not initialized", "error")
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	data := CacheStatsData{
		Caches:     h.cacheManager.AllStats(),
		TotalStats: h.cacheManager.TotalStats(),
	}

	if err := h.renderer.Render(w, r, "admin/cache_stats", render.TemplateData{
		Title: "Cache Statistics",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Cache", URL: "/admin/cache", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Clear handles POST /admin/cache/clear - clears all caches.
func (h *CacheHandler) Clear(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if h.cacheManager == nil {
		h.renderer.SetFlash(r, "Cache system not initialized", "error")
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	h.cacheManager.ClearAll()

	slog.Info("cache cleared", "cleared_by", user.ID)

	// Log to event log
	if h.eventService != nil {
		h.eventService.LogCacheEvent(r.Context(), model.EventLevelInfo, "All caches cleared", &user.ID, nil)
	}

	h.renderer.SetFlash(r, "All caches cleared successfully", "success")
	http.Redirect(w, r, "/admin/cache", http.StatusSeeOther)
}

// ClearConfig handles POST /admin/cache/clear/config - clears config cache.
func (h *CacheHandler) ClearConfig(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if h.cacheManager == nil {
		h.renderer.SetFlash(r, "Cache system not initialized", "error")
		http.Redirect(w, r, "/admin/cache", http.StatusSeeOther)
		return
	}

	h.cacheManager.InvalidateConfig()

	slog.Info("config cache cleared", "cleared_by", user.ID)

	// Log to event log
	if h.eventService != nil {
		h.eventService.LogCacheEvent(r.Context(), model.EventLevelInfo, "Configuration cache cleared", &user.ID, nil)
	}

	h.renderer.SetFlash(r, "Configuration cache cleared", "success")
	http.Redirect(w, r, "/admin/cache", http.StatusSeeOther)
}

// ClearSitemap handles POST /admin/cache/clear/sitemap - clears sitemap cache.
func (h *CacheHandler) ClearSitemap(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if h.cacheManager == nil {
		h.renderer.SetFlash(r, "Cache system not initialized", "error")
		http.Redirect(w, r, "/admin/cache", http.StatusSeeOther)
		return
	}

	h.cacheManager.InvalidateSitemap()

	slog.Info("sitemap cache cleared", "cleared_by", user.ID)

	// Log to event log
	if h.eventService != nil {
		h.eventService.LogCacheEvent(r.Context(), model.EventLevelInfo, "Sitemap cache cleared", &user.ID, nil)
	}

	h.renderer.SetFlash(r, "Sitemap cache cleared", "success")
	http.Redirect(w, r, "/admin/cache", http.StatusSeeOther)
}
