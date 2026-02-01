# Changelog

All notable changes to oCMS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/olegiv/ocms-go/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/olegiv/ocms-go/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/olegiv/ocms-go/compare/v0.0.0...v0.1.0
[0.0.0]: https://github.com/olegiv/ocms-go/releases/tag/v0.0.0
