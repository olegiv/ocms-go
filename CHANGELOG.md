# Changelog

All notable changes to oCMS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2024-XX-XX

### Added

#### Theme System
- Theme loading and switching infrastructure
- Theme configuration via `theme.json`
- Template inheritance with layouts, partials, and pages
- Theme-specific static asset serving at `/themes/{name}/static/*`
- Admin UI for theme management and activation
- Theme settings with color, text, and select options
- Default theme with clean, minimal design
- Developer theme with dark mode for technical blogs

#### Module System
- Module registration and lifecycle management
- Module interface with Init, Shutdown, Routes, AdminRoutes, and TemplateFuncs
- Module migration support with version tracking
- Hook system for extensibility (page.before_save, page.after_save, etc.)
- Example module demonstrating the module system
- Admin UI for viewing registered modules

#### REST API
- Complete REST API for pages, media, tags, and categories
- API key authentication with Bearer tokens
- Permission-based access control (read/write per resource)
- Per-key and global rate limiting
- Standard JSON response format with pagination metadata
- API documentation page at `/api/v1/docs`
- API key management in admin panel

#### SEO Features
- Page-level SEO fields (meta title, description, keywords)
- Open Graph and Twitter Card meta tags
- Auto-generated sitemap.xml at `/sitemap.xml`
- Configurable robots.txt at `/robots.txt`
- Canonical URL support
- NoIndex/NoFollow controls per page
- JSON-LD structured data

#### Scheduled Publishing
- Schedule pages to publish at a future date/time
- Cron-based scheduler using robfig/cron/v3
- Automatic status change from draft to published
- Scheduler status in admin dashboard

#### Full-Text Search
- SQLite FTS5 search index for pages
- Auto-sync with triggers on insert, update, delete
- Frontend search page with pagination
- Admin search for filtering pages
- Search input sanitization

#### Caching
- In-memory cache with TTL support
- Automatic cache invalidation on content changes
- Cache statistics in admin panel
- Manual cache clear functionality

#### Performance & Stability
- Gzip compression middleware for responses
- Request timeout middleware
- Health check endpoint at `/health`
- Graceful shutdown with signal handling
- ETag headers for static content

### Changed
- Updated project structure with new packages (cache, module, scheduler, seo, theme)
- Enhanced middleware chain with API authentication
- Improved error handling with structured JSON errors

### Security
- API key hashing with SHA-256
- Rate limiting to prevent abuse
- Permission validation on all API write operations

## [0.2.0] - 2024-XX-XX

### Added

#### Content Management
- Page versioning and revision history
- Featured images for pages
- Rich text editor with TinyMCE
- Page templates (home, page, list, 404)

#### Media Library
- Multi-file upload support
- Automatic thumbnail generation
- Image variants (small, medium, large)
- Folder organization
- Media picker component
- libvips integration for fast image processing

#### Menu System
- Menu builder with drag-and-drop ordering
- Hierarchical menu structures
- Multiple menu locations
- Link to pages or external URLs

#### Forms
- Form builder with multiple field types
- Form submissions management
- Read/unread status tracking
- Email notifications

#### Admin Improvements
- Dashboard with statistics
- Recent submissions widget
- Improved navigation

## [0.1.0] - 2024-XX-XX

### Added
- Initial release
- Page management (CRUD)
- Category and tag taxonomy
- User management with roles
- Session-based authentication
- SQLite database with migrations
- Embedded templates and static files
- SCSS styling framework
- HTMX and Alpine.js frontend

[Unreleased]: https://github.com/yourusername/ocms-go/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/yourusername/ocms-go/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/yourusername/ocms-go/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/yourusername/ocms-go/releases/tag/v0.1.0
