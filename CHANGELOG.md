# Changelog

All notable changes to oCMS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- Update Go to 1.26.0

## [0.7.0] - 2026-02-09

### Added

#### Informer Module
- Dismissible notification bar with flow layout and HTML content support
- Cookie-based dismissal tracking with reset capability
- Daily auto-reset for demo deployments
- Klaro cookie consent integration (essential purpose and cookie policy)
- Full i18n support (English and Russian translations)

#### Demo Mode Security
- Read-only content protection (blocks create/edit/delete/unpublish)
- Module and theme settings protection
- Form submission CSV export blocking
- SQL execution blocking in DB Manager module
- IP address masking in event logs
- Theme switching still permitted for demo testing
- Comprehensive test coverage for all restrictions

#### Theme Enhancements
- Hero section settings for developer theme
- Hero image placed inside terminal body
- Terminal frame visible when hero text is off

### Changed
- Update Go to 1.25.7
- Update `golang.org/x/sys` to v0.41.0
- Replace `GOGC=50` with `GOMEMLIMIT=200MiB` in Fly.io config

### Fixed
- Fix OOM crash on login for 256MB Fly.io VMs (Argon2id memory params)
- Fix sentinel middleware not intercepting requests
- Fix duplicate sentinel event log entries
- Restore sentinel ban UI in events template
- Fix template crash on forms submissions page
- Fix swapped `writeJSONError` arguments in modules handler
- Remove dead demo upload size limit code

### Security
- Fix Cookie 'Secure' attribute not set to true (code scanning alerts #33, #34, #36)

## [0.6.0] - 2026-02-06

### Added

#### Fly.io Deployment
- Full deployment configuration for oCMS demo instances
- Demo media seeding with image variants and featured images
- Deploy script with tests and documentation
- `/fly-deploy` Claude Code command for streamlined deployment

#### Event Log Enhancements
- Ban IP button directly from event log entries
- Event URL included in ban context for audit trail

#### Admin UX
- `maxlength` attributes on all admin text inputs for input validation

### Changed
- Update go-chi to v5.2.5
- Gate demo seeding on `OCMS_DO_SEED` environment variable

### Fixed
- Form page header to match other admin pages
- Demo seeding: larger images and improved deploy script
- Deploy script whitespace trimming for machine ID

## [0.5.0] - 2026-02-05

### Added

#### Privacy Module
- Klaro consent library integration for GDPR compliance
- Google Consent Mode v2 (GCM v2) support
- Admin interface for configuring consent services

#### Sentinel Module
- IP banning with admin CRUD interface
- Country detection using GeoIP service
- Auto-ban paths configuration for automated blocking
- IP whitelist support for trusted addresses
- Self-ban prevention for administrators
- Settings to enable/disable IP ban check and auto-ban
- Skip auto-ban for authenticated admin/editor users

#### Global URL Redirects
- Admin CRUD interface for managing redirects
- Wildcard support with prefix matching
- Redirect caching for performance

#### Routing Enhancements
- Trailing slash redirect middleware
- Legacy blog tag redirect (`/blog/tag/{slug}` → `/tag/{slug}`)
- `countryName` template function for GeoIP lookups

### Changed
- SQLC regenerated with v1.26.0 format
- Page metadata (date/author) hidden for page type content

### Fixed
- Open URL redirect security vulnerability (CWE-601)
- Redirect cache and translation format issues
- Multi-wildcard IP pattern matching in Sentinel
- Flash message calls in Sentinel module handlers
- Alias validation to support path-like URLs
- Theme tests for privacy module functions
- Services JSON escaping in privacy admin template

### Security
- Fixed open URL redirect in legacy blog tag redirect (CWE-601)

## [0.4.0] - 2026-02-04

### Added

#### Docker Support
- Complete Docker deployment support with multi-stage build
- Entrypoint script for proper volume permissions
- Dockerfile supporting Go 1.25+ toolchain

#### Editor Upgrade
- Switch from TipTap to TinyMCE 8 editor
- GPL license key configuration for TinyMCE 8
- Security: Add `rel="noopener noreferrer"` to all links
- Disable automatic URL conversion in editor

#### Forms
- Integrate public forms with theme system for consistent styling
- Improved form 404 handling using FrontendHandler

#### Event Logging
- Track 403 and 404 HTTP errors in event log

#### Developer Module
- Add missing 'small' image variant in test data generator

### Changed
- Update Alpine.js dependencies to version 3.15.8
- Rename CacheContext to Context in cache package
- Show all config fields on newly created empty sites
- Add Google Analytics to development CSP

### Fixed
- Fix daily analytics aggregation logging and error handling
- Fix empty site homepage crash by setting PageType for generated pages
- Fix sidebar template using wrong field name for category count
- Fix translatable config to persist form values in base config row
- Fix theme tests by adding missing template functions
- Fix Docker volume permissions with entrypoint script

## [0.3.0] - 2026-02-03

### Added

#### DB Manager Module
- SQL query execution interface for administrators
- Read-only and write query support with confirmation dialogs
- Query history and result display
- Admin-only access enforcement

#### Page Caching
- Context-aware page caching with automatic admin bypass
- Cache invalidation on content updates

#### Page Types
- New page type field for content organization
- "Exclude from lists" option for utility pages

#### Analytics Enhancements
- On-demand full aggregation for analytics data
- Improved timezone handling in aggregation

#### Media Library
- Regenerate variants button on media edit page
- Unified media dropzone component across admin

#### Security & CI
- SECURITY.md vulnerability reporting guide
- GitHub security workflows and CI badges

### Changed
- Editor preserves HTML formatting in source mode
- Tag cloud ordered by usage count instead of alphabetically
- Refactored module handlers to eliminate duplicate code
- Switched to GitHub default CodeQL setup

### Fixed
- TestMakeUniqueSlug failing due to incomplete table schema
- Template float comparison error in analytics
- Timezone handling in RunFullAggregation
- Code quality warnings from golangci-lint

## [0.2.0] - 2026-02-01

### Added

#### Embedded Themes Architecture
- Core themes (default, developer) embedded directly in binary
- Custom themes supported in `custom/themes/` directory
- Dual-source theme loading (embedded + custom)
- Core/Custom badges in admin theme list
- Windows compatibility for embedded theme static files

#### Event Logging Enhancements
- Request URL column in events table tracks which admin page triggered actions
- Expanded event coverage for all admin operations:
  - Media: upload, delete
  - Tags/Categories: create, update
  - Menus: create, update, delete
  - API Keys: create, revoke
  - Webhooks: create, update, delete
- URL column displayed in admin events list

#### Analytics Dashboard
- View counts with percentages in Browser and Countries sections
- `formatNumber` template function for thousand separators

#### Module System Enhancements
- Module active status management with toggle on/off functionality
- Persist module active status to database across restarts
- Middleware to block routes for inactive modules (404 for public, redirect for admin)
- Skip inactive modules' template functions
- Admin UI toggle switch for enabling/disabling modules

#### Admin UI Localization (i18n)
- Full i18n support for admin interface using embedded JSON locale files
- English and Russian translations included
- Language detection from Accept-Language header with database fallback
- Translatable flash messages for authentication events
- Dynamic module translation loading from embedded filesystems

#### Module Translation Support
- Modules can embed their own locale files in `locales/` directory
- Automatic translation loading during module initialization
- Developer module fully localized (English + Russian)
- Example module with i18n demonstration

#### Developer Module
- Confirmation dialog before generating test data
- i18n support for all UI text and messages

#### Theme Settings
- Favicon upload option in theme settings
- ICO file upload support

#### Database
- Opt-in database seeding via `OCMS_DO_SEED` environment variable
- Production-to-development sync script for development workflow

### Changed
- Refactored user retrieval in handlers to use `middleware.GetUserID` helpers
- Improved error handling by explicitly handling encoding/writing errors
- Suppressed resource cleanup errors using anonymous functions for consistency
- Refactored `fetchPagesForEntity` to eliminate duplicate code
- Migrated golangci-lint config to v2 format
- Updated Alpine.js packages to 3.15.6

### Fixed
- Alpine.js SRI integrity hash mismatch
- Nil pointer panic in Favicon handler
- CodeQL alert #28: sanitize URL before img.src assignment
- Security and API issues in media picker

### Security
- URL sanitization before img.src assignment
- Media picker API security improvements

## [0.1.0] - 2026-02-01

### Security
- Fixed multiple XSS vulnerabilities (reflected and DOM-based)
- Fixed path injection vulnerabilities in theme and media handling
- Replaced SHA-256 with Argon2id for API key hashing
- Fixed cookie Secure attribute for production deployments
- Blocked TIFF format to mitigate CVE-2023-36308

### Added
- **GeoIP Analytics**: Country detection using MaxMind GeoLite2 database
- **Form Builder**: New captcha field type with hCaptcha support
- **Forms i18n**: Full translation support for public forms
- **Event Log**: IP address tracking for audit trail
- **Deployment**: Single-instance deployment script for Ubuntu/Plesk

### Changed
- Consolidated duplicate code for better maintainability
- Added revive linter for code quality
- Menu items now sorted by position consistently

## [0.0.0] - 2026-01-31

### Added
- Initial release with full CMS functionality
- **Content**: Pages with versioning, media library with image variants, categories, tags, form builder
- **Themes**: Multiple themes, hot-switching, RTL support
- **Modules**: Extensible plugin system with lifecycle hooks
- **REST API**: Full CRUD, API key auth, rate limiting, built-in docs
- **Security**: Argon2id, RBAC, CSRF, login protection, security headers
- **SEO**: Meta tags, Open Graph, sitemap.xml, robots.txt
- **i18n**: Multi-language content, URL prefixes, admin translations
- **Webhooks**: Event-driven with HMAC signatures and retry logic
- **Import/Export**: JSON/ZIP with conflict resolution
- **Caching**: In-memory + Redis support

[Unreleased]: https://github.com/olegiv/ocms-go/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/olegiv/ocms-go/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/olegiv/ocms-go/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/olegiv/ocms-go/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/olegiv/ocms-go/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/olegiv/ocms-go/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/olegiv/ocms-go/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/olegiv/ocms-go/compare/v0.0.0...v0.1.0
[0.0.0]: https://github.com/olegiv/ocms-go/releases/tag/v0.0.0
