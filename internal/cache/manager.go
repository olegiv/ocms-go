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
	CacheTypeConfig  CacheType = "config"
	CacheTypeSitemap CacheType = "sitemap"
	CacheTypeGeneral CacheType = "general"
)

// CacheStats holds statistics for a specific cache.
type CacheStats struct {
	Name     string
	Type     CacheType
	Stats    Stats
	CachedAt *time.Time // when the cache was last populated (for sitemap)
	Size     int        // size in bytes (for sitemap)
}

// Manager manages all cache instances and provides a unified interface.
type Manager struct {
	Config  *ConfigCache
	Sitemap *SitemapCache
	General *Cache // for misc cached data

	// Theme settings cache key prefix
	ThemeSettingsPrefix string
}

// NewManager creates a new cache manager.
func NewManager(queries *store.Queries) *Manager {
	return &Manager{
		Config:              NewConfigCache(queries),
		Sitemap:             NewSitemapCache(queries, time.Hour),
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
	m.General.Clear()

	// Reset statistics for all caches
	m.Config.ResetStats()
	m.Sitemap.ResetStats()
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

// AllStats returns statistics for all caches.
func (m *Manager) AllStats() []CacheStats {
	stats := []CacheStats{
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
	generalStats := m.General.Stats()

	total := Stats{
		Hits:   configStats.Hits + sitemapStats.Hits + generalStats.Hits,
		Misses: configStats.Misses + sitemapStats.Misses + generalStats.Misses,
		Sets:   configStats.Sets + sitemapStats.Sets + generalStats.Sets,
		Items:  configStats.Items + sitemapStats.Items + generalStats.Items,
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

	// Preload sitemap (optional, can be slow for large sites)
	if siteURL != "" {
		_, err := m.Sitemap.Get(ctx, siteURL)
		if err != nil {
			// Non-fatal, sitemap will be generated on first request
			return nil
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
