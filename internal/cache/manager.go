package cache

import (
	"context"
	"log/slog"
	"time"

	"ocms-go/internal/store"
)

// CacheType identifies a specific cache.
type CacheType string

// Cache types.
const (
	CacheTypeConfig   CacheType = "config"
	CacheTypeSitemap  CacheType = "sitemap"
	CacheTypeGeneral  CacheType = "general"
	CacheTypeMenu     CacheType = "menu"
	CacheTypeLanguage CacheType = "language"
)

// ManagerCacheStats holds statistics for a specific cache in the manager.
// This is different from CacheStats in interface.go which is for the Cacher interface.
type ManagerCacheStats struct {
	Name     string
	Type     CacheType
	Stats    Stats
	CachedAt *time.Time // when the cache was last populated (for sitemap)
	Size     int        // size in bytes (for sitemap)
}

// Manager manages all cache instances and provides a unified interface.
type Manager struct {
	Config   *ConfigCache
	Sitemap  *SitemapCache
	Menus    *MenuCache
	Language *LanguageCache
	General  *Cache // for misc cached data

	// Theme settings cache key prefix
	ThemeSettingsPrefix string
}

// NewManager creates a new cache manager.
func NewManager(queries *store.Queries) *Manager {
	return &Manager{
		Config:              NewConfigCache(queries),
		Sitemap:             NewSitemapCache(queries, time.Hour),
		Menus:               NewMenuCache(queries),
		Language:            NewLanguageCache(queries),
		General:             New(5 * time.Minute),
		ThemeSettingsPrefix: "theme_settings_",
	}
}

// Start starts background cleanup tasks.
func (m *Manager) Start() {
	// Start cleanup for general cache
	m.General.StartCleanup(time.Minute)
}

// Stop stops all background tasks.
func (m *Manager) Stop() {
	m.General.Stop()
}

// ClearAll clears all caches and resets statistics.
func (m *Manager) ClearAll() {
	m.Config.Invalidate()
	m.Sitemap.Invalidate()
	m.Menus.Invalidate()
	m.Language.Invalidate()
	m.General.Clear()

	// Reset statistics for all caches
	m.Config.ResetStats()
	m.Sitemap.ResetStats()
	m.Menus.ResetStats()
	m.Language.ResetStats()
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

// AllStats returns statistics for all caches.
func (m *Manager) AllStats() []ManagerCacheStats {
	stats := []ManagerCacheStats{
		{
			Name:  "Site Configuration",
			Type:  CacheTypeConfig,
			Stats: m.Config.Stats(),
		},
		{
			Name:  "Sitemap",
			Type:  CacheTypeSitemap,
			Stats: m.Sitemap.Stats(),
		},
		{
			Name:  "Menus",
			Type:  CacheTypeMenu,
			Stats: m.Menus.Stats(),
		},
		{
			Name:  "Languages",
			Type:  CacheTypeLanguage,
			Stats: m.Language.Stats(),
		},
		{
			Name:  "General Cache",
			Type:  CacheTypeGeneral,
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
	generalStats := m.General.Stats()

	total := Stats{
		Hits:   configStats.Hits + sitemapStats.Hits + menuStats.Hits + languageStats.Hits + generalStats.Hits,
		Misses: configStats.Misses + sitemapStats.Misses + menuStats.Misses + languageStats.Misses + generalStats.Misses,
		Sets:   configStats.Sets + sitemapStats.Sets + menuStats.Sets + languageStats.Sets + generalStats.Sets,
		Items:  configStats.Items + sitemapStats.Items + menuStats.Items + languageStats.Items + generalStats.Items,
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
