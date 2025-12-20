package cache

import (
	"context"
	"log/slog"
	"time"

	"ocms-go/internal/store"
)

// Kind identifies a specific cache.
type Kind string

// Kind constants for different cache types.
const (
	KindConfig      Kind = "config"
	KindSitemap     Kind = "sitemap"
	KindGeneral     Kind = "general"
	KindMenu        Kind = "menu"
	KindLanguage    Kind = "language"
	KindTranslation Kind = "translation"
)

// ManagerCacheStats holds statistics for a specific cache in the manager.
type ManagerCacheStats struct {
	Name     string
	Kind     Kind
	Stats    Stats
	CachedAt *time.Time // when the cache was last populated (for sitemap)
	Size     int        // size in bytes (for sitemap)
}

// ManagerInfo holds information about the cache manager configuration.
type ManagerInfo struct {
	BackendType BackendType // The distributed cache backend type
	IsFallback  bool        // True if fell back to memory due to Redis failure
	RedisURL    string      // Redis URL if using Redis (masked for security)
}

// Manager manages all cache instances and provides a unified interface.
type Manager struct {
	Config      *ConfigCache
	Sitemap     *SitemapCache
	Menus       *MenuCache
	Language    *LanguageCache
	Translation *TranslationCache
	General     *Cache // for misc cached data

	// Distributed cache (optional, nil if only using memory cache)
	Distributed Cacher

	// Theme settings cache key prefix
	ThemeSettingsPrefix string

	// Info about the cache configuration
	info ManagerInfo
}

// NewManager creates a new cache manager with default memory cache.
func NewManager(queries *store.Queries) *Manager {
	return &Manager{
		Config:              NewConfigCache(queries),
		Sitemap:             NewSitemapCache(queries, time.Hour),
		Menus:               NewMenuCache(queries),
		Language:            NewLanguageCache(queries),
		Translation:         NewTranslationCache(queries),
		General:             New(5 * time.Minute),
		ThemeSettingsPrefix: "theme_settings_",
		info: ManagerInfo{
			BackendType: BackendMemory,
			IsFallback:  false,
		},
	}
}

// NewManagerWithConfig creates a new cache manager with optional distributed cache.
func NewManagerWithConfig(queries *store.Queries, cfg Config) *Manager {
	m := NewManager(queries)

	// Try to create distributed cache if Redis is configured
	if cfg.Type == "redis" && cfg.RedisURL != "" {
		result, err := NewCacheWithInfo(cfg)
		if err == nil {
			m.Distributed = result.Cache
			m.info.BackendType = result.BackendType
			m.info.IsFallback = result.IsFallback
			m.info.RedisURL = maskRedisURL(cfg.RedisURL)
		} else {
			slog.Warn("failed to create distributed cache", "error", err)
			m.info.BackendType = BackendMemory
			m.info.IsFallback = true
		}
	}

	return m
}

// maskRedisURL masks the password in a Redis URL for security.
func maskRedisURL(url string) string {
	// Simple masking - hide password if present
	// Format: redis://[:password@]host:port/db
	if len(url) > 10 {
		// Find @ symbol which indicates credentials
		for i := 8; i < len(url); i++ {
			if url[i] == '@' {
				// Found credentials, mask everything between :// and @
				return url[:8] + "***@" + url[i+1:]
			}
		}
	}
	return url
}

// Start starts background cleanup tasks.
func (m *Manager) Start() {
	// Start cleanup for general cache
	m.General.StartCleanup(time.Minute)
}

// Stop stops all background tasks and closes distributed cache.
func (m *Manager) Stop() {
	m.General.Stop()
	if m.Distributed != nil {
		if err := m.Distributed.Close(); err != nil {
			slog.Warn("failed to close distributed cache", "error", err)
		}
	}
}

// Info returns information about the cache manager configuration.
func (m *Manager) Info() ManagerInfo {
	return m.info
}

// IsRedis returns true if using Redis as the distributed cache backend.
func (m *Manager) IsRedis() bool {
	return m.info.BackendType == BackendRedis
}

// HealthCheck performs a health check on the cache system.
// Returns nil if healthy, error otherwise.
func (m *Manager) HealthCheck(ctx context.Context) error {
	if m.Distributed == nil {
		return nil // Memory-only cache is always "healthy"
	}

	// For Redis, ping the server
	if redisCache, ok := m.Distributed.(*RedisCache); ok {
		return redisCache.Ping(ctx)
	}

	return nil
}

// ClearAll clears all caches and resets statistics.
func (m *Manager) ClearAll() {
	m.Config.Invalidate()
	m.Sitemap.Invalidate()
	m.Menus.Invalidate()
	m.Language.Invalidate()
	m.Translation.Invalidate()
	m.General.Clear()

	// Clear distributed cache if present
	if m.Distributed != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.Distributed.Clear(ctx); err != nil {
			slog.Warn("failed to clear distributed cache", "error", err)
		}
		if sp, ok := m.Distributed.(StatsProvider); ok {
			sp.ResetStats()
		}
	}

	// Reset statistics for all caches
	m.Config.ResetStats()
	m.Sitemap.ResetStats()
	m.Menus.ResetStats()
	m.Language.ResetStats()
	m.Translation.ResetStats()
	m.General.ResetStats()

	slog.Info("cache stats reset")
}

// InvalidateConfig invalidates the config cache.
func (m *Manager) InvalidateConfig() {
	m.Config.Invalidate()
}

// InvalidateSitemap invalidates the sitemap cache.
func (m *Manager) InvalidateSitemap() {
	m.Sitemap.Invalidate()
}

// InvalidateThemeSettings invalidates theme settings in the config cache.
// Since theme settings are stored as config, we invalidate the entire config cache.
func (m *Manager) InvalidateThemeSettings() {
	m.Config.Invalidate()
}

// InvalidateContent invalidates content-related caches (sitemap, etc.).
// Call this when pages, categories, or tags are created/updated/deleted.
func (m *Manager) InvalidateContent() {
	m.Sitemap.Invalidate()
}

// InvalidateMenus invalidates the menus cache.
// Call this when menus or menu items are created/updated/deleted.
func (m *Manager) InvalidateMenus() {
	m.Menus.Invalidate()
}

// InvalidateLanguages invalidates the language cache.
// Call this when languages are created/updated/deleted.
func (m *Manager) InvalidateLanguages() {
	m.Language.Invalidate()
}

// InvalidateTranslations invalidates the entire translation cache.
// Call this when translations are created/updated/deleted.
func (m *Manager) InvalidateTranslations() {
	m.Translation.Invalidate()
}

// InvalidateTranslation invalidates translation cache for a specific entity.
func (m *Manager) InvalidateTranslation(entityType string, entityID int64) {
	m.Translation.InvalidateEntity(entityType, entityID)
}

// AllStats returns statistics for all caches.
func (m *Manager) AllStats() []ManagerCacheStats {
	stats := []ManagerCacheStats{
		{
			Name:  "Site Configuration",
			Kind:  KindConfig,
			Stats: m.Config.Stats(),
		},
		{
			Name:  "Sitemap",
			Kind:  KindSitemap,
			Stats: m.Sitemap.Stats(),
		},
		{
			Name:  "Menus",
			Kind:  KindMenu,
			Stats: m.Menus.Stats(),
		},
		{
			Name:  "Languages",
			Kind:  KindLanguage,
			Stats: m.Language.Stats(),
		},
		{
			Name:  "Translations",
			Kind:  KindTranslation,
			Stats: m.Translation.Stats(),
		},
		{
			Name:  "General Cache",
			Kind:  KindGeneral,
			Stats: m.General.Stats(),
		},
	}

	// Add sitemap-specific info
	if m.Sitemap.IsCached() {
		cachedAt := m.Sitemap.CachedAt()
		stats[1].CachedAt = &cachedAt
		stats[1].Size = m.Sitemap.Size()
	}

	return stats
}

// TotalStats returns aggregated statistics across all caches.
func (m *Manager) TotalStats() Stats {
	configStats := m.Config.Stats()
	sitemapStats := m.Sitemap.Stats()
	menuStats := m.Menus.Stats()
	languageStats := m.Language.Stats()
	translationStats := m.Translation.Stats()
	generalStats := m.General.Stats()

	total := Stats{
		Hits:   configStats.Hits + sitemapStats.Hits + menuStats.Hits + languageStats.Hits + translationStats.Hits + generalStats.Hits,
		Misses: configStats.Misses + sitemapStats.Misses + menuStats.Misses + languageStats.Misses + translationStats.Misses + generalStats.Misses,
		Sets:   configStats.Sets + sitemapStats.Sets + menuStats.Sets + languageStats.Sets + translationStats.Sets + generalStats.Sets,
		Items:  configStats.Items + sitemapStats.Items + menuStats.Items + languageStats.Items + translationStats.Items + generalStats.Items,
	}

	totalRequests := total.Hits + total.Misses
	if totalRequests > 0 {
		total.HitRate = float64(total.Hits) / float64(totalRequests) * 100
	}

	// Use the most recent reset time from any cache
	if configStats.ResetAt != nil {
		total.ResetAt = configStats.ResetAt
	}

	return total
}

// Preload preloads caches with data.
func (m *Manager) Preload(ctx context.Context, siteURL string) error {
	// Preload config
	if err := m.Config.Preload(ctx); err != nil {
		return err
	}

	// Preload menus
	if err := m.Menus.Preload(ctx); err != nil {
		slog.Warn("failed to preload menus cache", "error", err)
	}

	// Preload languages
	if err := m.Language.Preload(ctx); err != nil {
		slog.Warn("failed to preload language cache", "error", err)
	}

	// Preload sitemap (optional, can be slow for large sites)
	if siteURL != "" {
		_, err := m.Sitemap.Get(ctx, siteURL)
		if err != nil {
			// Non-fatal, sitemap will be generated on first request
			slog.Warn("failed to preload sitemap cache", "error", err)
		}
	}

	return nil
}

// GetConfig is a convenience method to get a config value.
func (m *Manager) GetConfig(ctx context.Context, key string) (string, error) {
	return m.Config.Get(ctx, key)
}

// GetSitemap is a convenience method to get the sitemap XML.
func (m *Manager) GetSitemap(ctx context.Context, siteURL string) ([]byte, error) {
	return m.Sitemap.Get(ctx, siteURL)
}

// GetMenu is a convenience method to get a menu by slug.
func (m *Manager) GetMenu(ctx context.Context, slug string) (*MenuWithItems, error) {
	return m.Menus.Get(ctx, slug)
}

// GetActiveLanguages is a convenience method to get all active languages.
func (m *Manager) GetActiveLanguages(ctx context.Context) ([]store.Language, error) {
	return m.Language.GetActive(ctx)
}

// GetDefaultLanguage is a convenience method to get the default language.
func (m *Manager) GetDefaultLanguage(ctx context.Context) (*store.Language, error) {
	return m.Language.GetDefault(ctx)
}

// GetLanguageByCode is a convenience method to get a language by its code.
func (m *Manager) GetLanguageByCode(ctx context.Context, code string) (*store.Language, error) {
	return m.Language.GetByCode(ctx, code)
}

// GetTranslations is a convenience method to get translations for an entity.
func (m *Manager) GetTranslations(ctx context.Context, entityType string, entityID int64) (TranslationMap, error) {
	return m.Translation.Get(ctx, entityType, entityID)
}

// GetTranslationsBatch is a convenience method to get translations for multiple entities.
func (m *Manager) GetTranslationsBatch(ctx context.Context, entityType string, entityIDs []int64) (map[int64]TranslationMap, error) {
	return m.Translation.GetBatch(ctx, entityType, entityIDs)
}
