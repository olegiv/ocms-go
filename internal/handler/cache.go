// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
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
	Caches      []cache.ManagerCacheStats
	TotalStats  cache.Stats
	Info        cache.ManagerInfo
	IsRedis     bool
	HealthError string // Non-empty if health check failed
}

// Stats handles GET /admin/cache - displays cache statistics.
func (h *CacheHandler) Stats(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)

	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdmin, "Cache system not initialized")
		return
	}

	info := h.cacheManager.Info()
	totalStats := h.cacheManager.TotalStats()

	viewData := adminviews.CacheStatsViewData{
		Caches:     convertCacheItems(h.cacheManager.AllStats()),
		TotalStats: adminviews.CacheStatsView{Hits: totalStats.Hits, Misses: totalStats.Misses, Items: totalStats.Items, HitRate: totalStats.HitRate},
		IsRedis:    h.cacheManager.IsRedis(),
		IsFallback: info.IsFallback,
		RedisURL:   info.RedisURL,
	}

	if totalStats.ResetAt != nil {
		viewData.ResetAt = totalStats.ResetAt.Format("Jan 2, 2006 15:04:05")
	}

	// Perform health check
	if err := h.cacheManager.HealthCheck(r.Context()); err != nil {
		viewData.HealthError = err.Error()
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer,
		i18n.T(lang, "nav.cache"),
		cacheBreadcrumbs(lang))
	renderTempl(w, r, adminviews.CacheStatsPage(pc, viewData))
}

// clearCacheHelper performs the clear operation, logging, and flash message.
func (h *CacheHandler) clearCacheHelper(w http.ResponseWriter, r *http.Request, clearFn func(), logMsg, eventMsg, flashMsg string) {
	clearFn()
	slog.Info(logMsg, "cleared_by", middleware.GetUserID(r))

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogCacheEvent(r.Context(), model.EventLevelInfo, eventMsg, middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), nil)
	}

	flashSuccess(w, r, h.renderer, redirectAdminCache, flashMsg)
}

// Clear handles POST /admin/cache/clear - clears all caches.
func (h *CacheHandler) Clear(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionClearCache, redirectAdminCache) {
		return
	}

	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.ClearAll,
		"cache cleared", "All caches cleared", "All caches cleared successfully")
}

// ClearConfig handles POST /admin/cache/clear/config - clears config cache.
func (h *CacheHandler) ClearConfig(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionClearCache, redirectAdminCache) {
		return
	}

	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.InvalidateConfig,
		"config cache cleared", "Configuration cache cleared", "Configuration cache cleared")
}

// ClearSitemap handles POST /admin/cache/clear/sitemap - clears sitemap cache.
func (h *CacheHandler) ClearSitemap(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionClearCache, redirectAdminCache) {
		return
	}

	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.InvalidateSitemap,
		"sitemap cache cleared", "Sitemap cache cleared", "Sitemap cache cleared")
}

// ClearPages handles POST /admin/cache/clear/pages - clears page cache.
func (h *CacheHandler) ClearPages(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionClearCache, redirectAdminCache) {
		return
	}

	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.InvalidatePages,
		"pages cache cleared", "Pages cache cleared", "Pages cache cleared")
}

// ClearMenus handles POST /admin/cache/clear/menus - clears menu cache.
func (h *CacheHandler) ClearMenus(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionClearCache, redirectAdminCache) {
		return
	}

	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.InvalidateMenus,
		"menus cache cleared", "Menus cache cleared", "Menus cache cleared")
}

// ClearLanguages handles POST /admin/cache/clear/languages - clears language cache.
func (h *CacheHandler) ClearLanguages(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionClearCache, redirectAdminCache) {
		return
	}

	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.InvalidateLanguages,
		"languages cache cleared", "Languages cache cleared", "Languages cache cleared")
}
