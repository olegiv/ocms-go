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
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdmin, "Cache system not initialized")
		return
	}

	data := CacheStatsData{
		Caches:     h.cacheManager.AllStats(),
		TotalStats: h.cacheManager.TotalStats(),
		Info:       h.cacheManager.Info(),
		IsRedis:    h.cacheManager.IsRedis(),
	}

	// Perform health check
	if err := h.cacheManager.HealthCheck(r.Context()); err != nil {
		data.HealthError = err.Error()
	}

	h.renderer.RenderPage(w, r, "admin/cache_stats", render.TemplateData{
		Title: i18n.T(lang, "nav.cache"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.cache"), URL: redirectAdminCache, Active: true},
		},
	})
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
	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.ClearAll,
		"cache cleared", "All caches cleared", "All caches cleared successfully")
}

// ClearConfig handles POST /admin/cache/clear/config - clears config cache.
func (h *CacheHandler) ClearConfig(w http.ResponseWriter, r *http.Request) {
	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.InvalidateConfig,
		"config cache cleared", "Configuration cache cleared", "Configuration cache cleared")
}

// ClearSitemap handles POST /admin/cache/clear/sitemap - clears sitemap cache.
func (h *CacheHandler) ClearSitemap(w http.ResponseWriter, r *http.Request) {
	if h.cacheManager == nil {
		flashError(w, r, h.renderer, redirectAdminCache, "Cache system not initialized")
		return
	}
	h.clearCacheHelper(w, r, h.cacheManager.InvalidateSitemap,
		"sitemap cache cleared", "Sitemap cache cleared", "Sitemap cache cleared")
}
