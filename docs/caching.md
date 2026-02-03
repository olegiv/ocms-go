# Caching

oCMS uses a multi-layer caching system to improve performance while ensuring data freshness.

## Cache Types

| Cache | Purpose | Invalidation |
|-------|---------|--------------|
| **Config** | Site settings (site_name, etc.) | On config update |
| **Sitemap** | Generated sitemap.xml | On page/category/tag changes |
| **Pages** | Published page content | On page create/update/delete |
| **Menus** | Menu structure and items | On menu/item changes |
| **Languages** | Active languages list | On language changes |
| **Translations** | Entity translations | On translation changes |

## Context-Aware Page Caching

Pages are cached using context-aware keys that include language and user role:

```
{language}:{role}:{slug}
```

Examples:
- `en:anonymous:about-us` - English, anonymous visitor
- `en:admin:about-us` - English, admin user
- `ru:anonymous:about-us` - Russian, anonymous visitor

This ensures users see content appropriate for their language and access level.

### Role Levels

| Role | Level | Description |
|------|-------|-------------|
| `anonymous` | 0 | Not logged in |
| `public` | 0 | Logged in, no special permissions |
| `editor` | 1 | Can edit content |
| `admin` | 2 | Full system access |

## Admin Panel

**Admin routes (`/admin/*`) bypass the cache** for site configuration lookups to ensure administrators always see fresh data. This is configured in `cmd/ocms/main.go`:

```go
// Admin routes: no cache for site config
r.Use(middleware.LoadSiteConfig(db, nil))

// Frontend routes: use cache for performance
r.Use(middleware.LoadSiteConfig(db, cacheManager))
```

## Cache Backends

### In-Memory (Default)

- Single-instance caching
- No external dependencies
- Lost on restart

### Redis (Distributed)

- Multi-instance caching
- Shared across servers
- Persists across restarts

Configure with environment variables:

```bash
OCMS_REDIS_URL=redis://localhost:6379/0
OCMS_CACHE_PREFIX=ocms:
OCMS_CACHE_TTL=3600
```

## Cache Statistics

View cache statistics at `/admin/cache`:

- Hit rate per cache type
- Number of cached items
- Cache backend status (Redis/Memory)

## Manual Cache Clearing

Clear caches from the admin panel (`/admin/cache`) or programmatically:

```go
cacheManager.ClearAll()           // Clear everything
cacheManager.InvalidateConfig()   // Clear config
cacheManager.InvalidateSitemap()  // Clear sitemap
cacheManager.InvalidatePages()    // Clear all pages
cacheManager.InvalidatePage(id)   // Clear specific page
cacheManager.InvalidateMenus()    // Clear menus
cacheManager.InvalidateLanguages() // Clear languages
cacheManager.InvalidateTranslations() // Clear translations
```

## Automatic Invalidation

Caches are automatically invalidated when content changes:

- **Page created/updated/deleted** → Page cache + Sitemap cache
- **Menu/item changed** → Menu cache
- **Config changed** → Config cache
- **Language changed** → Language cache
- **Translation changed** → Translation cache for that entity

## Cache Preloading

On startup, oCMS preloads frequently accessed data:

```go
cacheManager.Preload(ctx, siteURL)
```

This loads:
- Site configuration
- All menus
- Active languages
- Sitemap (if siteURL provided)
