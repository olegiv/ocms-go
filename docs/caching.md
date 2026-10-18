# Caching

oCMS uses a multi-layer caching system to improve performance while ensuring data freshness.

## RSS and Atom feeds

[Feeds](feeds.md) are generated from current database data on every request.
They use `Cache-Control: public, no-cache` and deterministic weak ETags;
matching `If-None-Match` validators return a bodyless 304. Content changes are
visible on the next request without clearing the page cache. HEAD retains GET's
representation headers, including negotiated compression, and omits the body.
Error responses use `no-store`.

## Cache Types

| Cache | Purpose | Invalidation |
|-------|---------|--------------|
| **Config** | Site settings (site_name, etc.) | On config update |
| **Sitemap** | Generated sitemap.xml | On page and language changes, theme activation and settings, and Migrator imports; also expires after 1 hour |
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
```

Public frontend handlers read site configuration through the cache manager (`cacheManager.GetConfig`) for performance.

## Cache Backends

### In-Memory (Default)

- Single-instance caching
- No external dependencies
- Lost on restart

### Redis (Optional)

- Connected at startup when `OCMS_REDIS_URL` is set; if Redis is unreachable, oCMS logs a warning and falls back to memory
- The backend type and health are shown at `/admin/cache`, and "clear all" also removes the Redis keys under `OCMS_CACHE_PREFIX`
- The config, sitemap, page, menu, language and translation caches are held in each instance's own memory. They are not stored in Redis and are not shared between instances, so each instance invalidates only its own copies

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
- **Translation changed** → Translation cache for that entity type

## Cache Preloading

On startup, oCMS preloads frequently accessed data:

```go
cacheManager.Preload(ctx, "")
```

This loads:
- Site configuration
- All menus
- Active languages
- Sitemap (only when `siteURL` is non-empty; startup passes an empty value, so the sitemap is built on the first `/sitemap.xml` request)
