# Changelog

All notable changes to oCMS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.20.0] - 2026-04-22

### Added

#### Agent-Ready Discovery
- Publish RFC 8288 `Link` header on `/` pointing to API Catalog,
  OpenAPI service-desc, and Swagger UI service-doc relations so agent
  scanners can find the REST API from a single homepage hit
- `GET /.well-known/api-catalog` (RFC 9727 linkset) declares stable
  API surfaces with media types and doc links
- `GET /.well-known/agent-skills/index.json` (v0.2.0) advertises the
  REST API capability with a real OpenAPI SHA-256 digest for client
  validation
- `GET /.well-known/mcp/server-card.json` (draft SEP-1649) exposes MCP
  server metadata; `transport` is `null` pending a future MCP transport
  implementation, with REST fallback via `capabilities.rest.openapi`
- `Content-Signal:` directive in `/robots.txt` declares training-data
  preferences (default `search=yes, ai-train=no, ai-input=yes`,
  configurable via `robots_content_signal` config key)
- New config keys: `robots_content_signal` and `mcp_server_version`

#### Markdown for Agents
- Content negotiation on `/` and `/{slug}`: callers sending
  `Accept: text/markdown` receive a plain-text Markdown representation
  instead of HTML theme output
- Response headers include `Content-Type: text/markdown; charset=utf-8`,
  `Vary: Accept` (for CDN cache separation), and `X-Markdown-Tokens`
  for agent context-budget estimation
- Conversion from stored TinyMCE HTML via
  `JohannesKaufmann/html-to-markdown/v2`; `<script>` and `<iframe>`
  stripped by construction
- 2 MB input cap to prevent CPU DoS on oversized pages; falls back to
  HTML on overflow
- Auth parity with the HTML path: draft pages remain admin/editor-only
  in both representations

#### Configuration
- `OCMS_HSTS_PRELOAD` environment variable (default `false`) enables
  the `; preload` HSTS directive in production without recompiling;
  opt-in for operators submitting domains to hstspreload.org

### Changed

#### Deployment & Operations
- `ocmsctl start|stop|restart` auto-delegates to `systemctl` when a
  site is enabled as `ocms@<site-id>.service`, eliminating manual
  `sudo systemctl` invocations. PID-file sites continue to work
  unchanged
- `ocmsctl start --foreground` still errors on systemd-enabled sites
  (unsupported)
- Delegation respects management intent: `start` defers to systemd
  when the unit is enabled; `stop`/`restart` only act when the unit
  is active, avoiding races with a PID-file process

#### Documentation
- `scripts/deploy/README.md` — new Troubleshooting sections for
  missing systemd drop-in files and "sites don't come back after
  reboot", plus clarification that the health-check cron only
  restarts `active` sites
- `docs/agent-ready.md` (new) describes discovery surfaces, Markdown
  negotiation, config keys, and local verification
- `docs/login-security.md` — Production Deployment Checklist calls
  out rotating the seeded `admin@example.com / changeme1234`
  credentials on first login
- `docs/reverse-proxy.md` — HSTS Preload section with the
  `OCMS_HSTS_PRELOAD` env var and hstspreload.org submission process

#### Dependencies
- Update htmx.org to 2.0.10
- Update Tailwind CSS to 4.2.4

### Fixed

#### Agent-Ready Content Negotiation
- `Accept: text/markdown;q=0` now correctly yields HTML
- Wildcard ranges (`*/*`, `text/*`) contribute to HTML quality without
  forcing markdown when HTML is listed with higher specificity
- Specificity tiers (explicit media type > wildcard) defer to explicit
  entries, so `Accept: text/html;q=0.2, text/markdown;q=0.8, */*`
  correctly prefers markdown

#### Login Protection
- Serialize `RecordFailedAttempt` and `RecordSuccessfulLogin` under a
  per-process mutex so concurrent requests cannot drop attempts or let
  an upsert re-lock a just-cleared account

### Security

#### Markdown Rendering (FIND-001, FIND-002)
- Markdown canonical URL requires configured `site_url`; no `r.Host`
  fallback, blocking attacker-supplied Host headers from landing in
  `[Source]` links (FIND-001)
- `sanitizeHeading` collapses newlines in H1 titles before writing
  markdown, blocking heading break-out (FIND-002)

#### Authentication (FIND-003, FIND-005)
- `auth.VerifyDummyPassword` runs constant-cost Argon2id verification
  on the unknown-email path, eliminating timing-based email
  enumeration (FIND-003)
- New `login_protection` SQLite table replaces the in-memory lockout
  map, so brute-force windows survive deploys, crashes, and OOM kills
  (FIND-005)

#### Session & CSP (FIND-006, FIND-007, FIND-008)
- Session cookie upgraded to `SameSite=Strict` (FIND-006)
- Remove unused `unpkg.com` and `esm.sh` from `script-src` and
  `connect-src` in both dev and prod CSPs (FIND-007)
- Add `upgrade-insecure-requests` to the production CSP so
  admin-authored HTML with `http://` subresources auto-upgrades
  (FIND-008)

#### Transport & Secrets (FIND-009, FIND-010, FIND-011)
- `OCMS_HSTS_PRELOAD` env var enables HSTS preload without
  recompiling; docs cover the hstspreload.org submission process
  (FIND-009)
- Broaden `.gitignore` to `.env*` and `*.api_keys` so secret-carrying
  files cannot be committed by accident (FIND-010)
- Production Deployment Checklist requires operators to rotate seeded
  admin credentials on first login (FIND-011)

Each security fix ships with a drift test that fails on the bug
state. FIND-004 (`unsafe-eval` for Alpine.js) remains open as a
structural dependency.

## [0.19.0] - 2026-04-20

### ⚠️ Breaking Changes

#### REST API
- `/api/v1` removed. Clients must migrate to `/api/v2`. The error envelope
  shape (`{error: {code, message, details}}`) is preserved, so for most
  callers only the base URL changes.

### Added

#### REST API v2
- New `/api/v2` surface generated from Go types via [huma v2](https://huma.rocks/)
  + humachi. OpenAPI 3.1 served live at `/api/v2/openapi.json` and
  `/api/v2/openapi.yaml`; Swagger UI self-hosted at `/api/v2/docs` (no CDN).
- `GET /api/v2/status` (public liveness) and `GET /api/v2/auth` (inspect the
  calling API key's prefix, name, and permissions).
- Single-file vs batch media upload: `POST /api/v2/media` accepts one file;
  `POST /api/v2/media/batch` accepts many and returns per-file errors.
- Per-permission OpenAPI Security scopes: every write op advertises its
  required permission (`pages:write`, `media:write`, `taxonomy:write`) in
  the spec, and the runtime middleware enforces the scope before huma
  parses the request body.

#### Developer Tooling
- `/pr-prepare` slash command that runs `pr-review-toolkit:code-reviewer`
  (and `silent-failure-hunter` when warranted) against the branch diff to
  catch blast-radius issues locally before opening a PR.

### Changed

#### Dependencies
- Update modernc.org/sqlite 1.48.2 → 1.49.1
- Promote gopkg.in/yaml.v3 from indirect to direct (used by
  `/api/v2/openapi.yaml` serving)

#### Admin UI
- Sidebar, API Keys page, and `/admin/docs` now link to `/api/v2` endpoints
  instead of `/api/v1`

### Removed

#### REST API
- Drop the entire `/api/v1` surface and the `internal/handler/api` package.
  The chronic spec drift that motivated the rewrite no longer has anywhere
  to hide.

### Security

#### API v2 Hardening
- Restore per-API-key rate limiter on write methods (10 rps / burst 20),
  matching v1; authenticated reads stay on the global IP-level limit only
- Mandatory API key authentication runs BEFORE huma parses the request
  body, so unauthenticated callers cannot force multipart / tempfile work
  on `POST /media`
- Permission check runs before body parse: a read-only key hitting a write
  op gets 403 without any body work
- Tiered request-body caps: 100 MiB for multipart file uploads, 1 MiB for
  JSON and form-urlencoded (v1-parity; v2's scaffold had no cap initially)
- Surface specific auth rejection reasons on writes ("API key expired",
  "access not allowed from this IP", "Too many authentication attempts"
  for Argon2 slot saturation, "Invalid Authorization header format") —
  v2's initial `OptionalAPIKeyAuth` collapsed them into a generic 401
- Declare `ApiKeyAuth` in `components.securitySchemes`; every write
  operation and `GET /auth` carry `Security` in the spec so Swagger UI
  shows the padlock and generated clients attach the Authorization header

#### Input Validation
- Validate `canonical_url` and `video_url` on pages: scheme allowlist
  (http/https only; `javascript:` and similar rejected) and 2048-char cap
- Sanitize media filename on `PUT /media/{id}` — strip path separators
  and HTML-dangerous characters (`<`, `>`, `&`, `#`, quotes, percent,
  backslash). Previously only the upload path sanitized
- Validate `language_code` for format (`util.IsValidLangCode`) and
  existence (`GetLanguageByCode`) on pages, tags, and categories before
  persisting
- Reject `folder_id` overflow: `strconv.ParseInt` replaces the hand-rolled
  `id * 10 + digit` scan that wrapped on 20-plus-digit inputs into bogus
  positive IDs
- Restrict `X-Forwarded-Proto` in the docs helper to `http` or `https`;
  other values ignored (previously trusted any value)

#### Information Disclosure
- Scrub non-domain errors in `UploadBatch` to a generic "Upload failed"
  message instead of passing raw filesystem / imaging library strings
  through to the API response

#### Observability
- Reinstate audit events on every successful v2 write (pages, media, tags,
  categories — 13 sites). v2's initial scaffold silently dropped v1's
  audit-log calls; `/admin/events` now sees API-driven changes again

#### Spec/Runtime Coherence
- Introduce `NullableInt64` JSON sentinel with a `SchemaProvider` override,
  so `PUT /api/v2/media/{id}` with `"folder_id": null` correctly clears
  the folder assignment (previously silently no-opped because `*int64`
  could not distinguish absent from null), and the OpenAPI schema
  advertises `integer | null` instead of the reflected struct shape

### Fixed

#### REST API v2
- Propagate `resolveLanguageCode` validation errors as 422 with field
  details instead of flattening to generic 500 in pages and taxonomy
  write paths
- Don't silently swallow `UpdateMedia` errors from the alt/caption step
  of `POST /media`: log via `slog.Warn` with media_id so operators can
  reconcile, and return the DTO that actually matches the persisted row

## [0.18.1] - 2026-04-14

### Changed

#### Themes
- Remove `{{if}}` guards on module template functions (always render)
- Update wiki embed hook syntax documentation

#### Analytics
- Add `article-content` selector to read tracker

#### Embed Proxy
- Strip default ports from render-time origin
- Honor `X-Forwarded-Host` from trusted proxies

#### Dependencies
- Update golang.org/x/crypto 0.49.0 → 0.50.0
- Update golang.org/x/image 0.38.0 → 0.39.0
- Update golang.org/x/net 0.52.0 → 0.53.0
- Update golang.org/x/text 0.35.0 → 0.36.0
- Update modernc.org/sqlite 1.48.1 → 1.48.2

### Fixed

#### Embed Proxy
- Fix token origin mismatch when site URL contains default port

#### Pages
- Clear `scheduled_at` on manual publish/unpublish
- Invalidate page cache on publish toggle
- Preserve page categories on form validation errors
- Skip featured image validation on unchanged media
- Filter unlisted posts from tag/category pages and search results
- Harden PageByID language code validation

#### API
- Preserve taxonomy language code in API updates

#### Admin UI
- Fix category translation redirect path
- Normalize root `parent_id` in menu AddItem
- Fix legacy-theme fallback and multi-proxy scheme parsing
- Fix Makefile `.PHONY` declaration

#### Modules
- Fix hCaptcha login lockout when module hooks inactive

#### Import
- Fix non-image import for double-dot filenames

#### Tests
- Skip DNS-dependent tests when network unavailable

#### Deployment
- Fix deploy symlink validation portability (Bash 3 compatibility)

### Security

#### Embed Proxy
- Mint embed proxy tokens at render time instead of page save
- Enforce embed proxy token verification in production
- Require static secret for embed token minting
- Tighten embed proxy rate limits

#### XSS Prevention
- Escape Alpine.js `x-data` values in page form slug, admin slug, and language edit views
- Escape page version preview body in admin modal
- Escape media folder names in HTMX create response
- Render custom CSS via sanitizing `safeCSS` helper
- Escape custom CSS in theme templates
- Validate Sentinel ban URLs before rendering
- Validate menu item URL schemes
- Harden javascript URI suspicious markup detection
- Consolidate XSS detection into `internal/security` package

#### API Authorization
- Enforce `pages:read` permission for draft page API access
- Enforce taxonomy permission for page tag auto-creation

#### Sitemap
- Key sitemap cache by configured `site_url` instead of Host header
- Require configured `site_url` for sitemap generation

#### hCaptcha
- Remove insecure default hCaptcha test keys
- Scrub persisted hCaptcha test keys on upgrade

#### Demo Mode
- Prevent IP address leak in events ban UI
- Rotate demo admin password on startup
- Remove demo admin password from startup log
- Disallow production seeding in demo mode
- Drop stale demo credentials from seeded homepage

#### Deployment
- Validate deploy script inputs before remote commands
- Restrict deploy symlink targets to custom directory
- Reject leading hyphens in validated CLI values
- Restrict `setup-site --path` to domain vhost directory

#### Data Protection
- Mitigate CSV formula injection in form submissions export
- Prevent symlink traversal in Elefant media import
- Use `crypto/rand` for imported user passwords
- Validate stored media filename during variant regeneration
- Stop exposing session token in public forms
- Prevent migrator password default disclosure

#### Infrastructure Hardening
- Prevent scheme-relative open redirects in trailing slash middleware
- Harden database directory and file permissions
- Harden `chmod` for SQLite file URI DSNs
- Image dimension guards before decode (DoS prevention)
- Bound rate limiter cache growth
- Bound webhook debouncer pending events
- Throttle concurrent API key hash verification (Argon2 DoS)
- Rate limit WARN event log persistence
- Limit 404 event logging to authenticated users
- Restrict developer module admin routes to admins
- Move Docker entrypoint out of writable app directory
- Add `noopener` to admin View Site and page preview links
- Harden theme image preview URLs
- Restore production default-credential startup guard

## [0.18.0] - 2026-04-09

### Added

#### Analytics
- Add views/reads tracking with Medium-style engagement metrics (read ratio, estimated read time)
- Add analytics dashboard with page-level view/read stats and retention reports
- Add view/read counters to page templates across all themes
- Add per-IP rate limiting to read beacon endpoint

#### Media
- Add OG image variant (1200×630) for social sharing across all themes

#### Deployment
- Follow symlinks in deploy custom content sync (`rsync -aL`)
- Add broken symlink validation before deploy to prevent failed deployments
- Pass git version, commit hash, and build time to Fly.io Docker builds

### Changed

#### Dependencies
- Update go-sqlite3 1.14.41 → 1.14.42

### Fixed

#### Analytics
- Fix multilingual stats and retention-bound reads
- Fix security and code quality issues from audit
- Extract shared extractIdentity helper to eliminate duplicate code

#### Admin UI
- Fix theme settings demo mode alert rendering one word per line (grid layout issue)

### Security
- Harden analytics auth and salt generation
- Fix security audit findings in analytics module (input validation, error handling)

## [0.17.0] - 2026-04-08

### Added

#### API
- Add structured event logging to all REST API handlers (pages, media, tags, categories) with category-scoped `apiLogger`
- Add `OCMS_ERROR_LOG_PATH` env var for separate error log file (5xx/ERROR messages)

#### Sentinel
- Add honeypot auto-ban: IPs that trigger form honeypot fields are automatically banned via `HookSecurityHoneypotTriggered` hook
- Add honeypot auto-ban toggle in Sentinel admin settings
- Add admin/editor exemption from honeypot auto-ban to prevent self-lockout

### Changed

#### Dependencies
- Update go-sqlite3 1.14.40 → 1.14.41

#### i18n
- Replace Unicode escape sequences with UTF-8 Cyrillic in locale files
- Add missing Klaro privacyPolicy translations

### Fixed

#### Database
- Fix SQLITE_BUSY errors by setting WAL journal mode and busy timeout via DSN parameters

### Security
- Replace chi's `RealIP` middleware with trusted-proxy-aware IP resolution; chi's middleware blindly trusts `True-Client-IP` / `X-Forwarded-For` headers from any source, allowing external attackers to spoof `127.0.0.1` and evade IP-based banning
- Gate author email on API key authentication to prevent account enumeration via `?include=author`
- Remove API key names from event log metadata to prevent infrastructure topology leakage
- Truncate Sentinel auto-ban notes/URL fields to 255 chars to prevent unbounded DB storage
- Restrict error log file permissions to 0600 (owner-only) with fchmod enforcement on pre-existing files
- Add session cookie pre-check in Sentinel middleware to avoid panic-recovery control flow
- Add `OCMS_TRUSTED_PROXIES` hint to docker-compose.yml for reverse proxy deployments

## [0.16.0] - 2026-04-06

### ⚠️ Breaking Changes

#### Deployment
- **sites.conf format changed**: column 2 is now the full instance directory instead of the vhost path; deploy scripts no longer append `/ocms` automatically
- Existing deployments must migrate sites.conf before updating scripts (see `scripts/deploy/README.md`)

### Added

#### Video Embedding
- Add video embedding widget for pages with YouTube, Vimeo, and Dailymotion support
- Add optional `video_title` field displayed above embedded video
- Add `video_url` and `video_title` to REST API page responses and import/export schema

#### Admin UI
- Add collapsible admin sidebar with persistent state
- Add page type filter to admin page list
- Add column sorting to admin page and tag lists
- Add editable created_at and published_at dates in page editor

#### Migrator
- Add webpage import source for importing pages from external URLs
- Add `OCMS_UPLOADS_DIR` support in migrator module

#### Deployment
- Add `--path` flag in setup-site.sh for configurable instance paths
- Add generate-logrotate.sh for dynamic logrotate config from sites.conf
- Add upgrade sequence with migration and rollback steps to deploy docs

#### Configuration
- Add `OCMS_HCAPTCHA_DISABLED` env var to force-disable hCaptcha
- `OCMS_ACTIVE_THEME` env var now overrides DB/admin setting

#### Theme
- Add hero image and style settings to starter theme
- Style category badges in starter theme

#### Developer Tooling
- Add `/create-page` CLI command for page creation via API or direct DB

### Changed

#### Theme
- Consolidate footer into single line with bullet separators (default, starter)

#### Code Quality
- Deduplicate form page template data (delegate to FrontendHandler)
- Fix 28 code quality issues across 20 files (import shadowing, unused params, cyclomatic complexity, if-else chains, unkeyed struct literals)

### Fixed

#### Pages
- Fix created_at mutation in generic UpdatePage query (now a separate dedicated query)
- Fix published_at not clearing when unpublishing via edit form
- Fix TinyMCE external link prompt for internal paths
- Fix embedBody placeholder

#### Configuration
- Fix .env loading and blog title translation
- Fix search snippet cleanup

### Security
- Sanitize Klaro cookie patterns against safe character allowlist (PRIV-001)
- Validate GCM consent types against known Google Consent Mode v2 allowlist (PRIV-002)
- Fix CSP and security audit findings since v0.14.0
- Filter unpublished content from Dify KB generation
- Skip menu items referencing unpublished pages in KB generation

## [0.15.0] - 2026-04-04

### Added

#### Dify Knowledge Base
- Add Dify knowledge base file downloads (site-content.md, user-guide.md) for AI chatbot training
- Add batch SQLC queries for page categories and tags to optimize KB generation

#### Wiki
- Add GitHub Wiki as git submodule at `wiki/` with `/update-wiki` command
- Add wiki sync requirement to CLAUDE.md

#### Theme
- Add shared image lightbox for all themes
- Show all page categories in post cards as pills
- Add module HTML injection support for templ-based frontend layouts

#### API
- Add API error logging for failed requests
- Add tag auto-creation on page save via API
- Return 422 for tag validation errors and pre-validate tag IDs

#### Testing
- Raise test coverage across all packages (~15,600 new test lines)
- Add make coverage and coverage-html targets

### Changed

#### Theme
- Use larger images in post and related cards
- Stack post card image above content on mobile

#### Build
- Move security reports to `.audit/` directory (gitignored)
- Update SECURITY.md with current versions
- Update shared Claude Code submodule

### Fixed

#### Security
- Fix email disclosure in Dify KB exports: author fallback no longer uses email, admin email replaced with contact-us link (KB-001, KB-002)
- Fix stale module template functions after toggle: funcs now fetched per-request via provider interface (INJ-001)

#### Performance
- Fix N+1 queries in KB generation with batch SQLC queries, reducing from O(2N+1) to 3 queries (KB-003)

#### Theme
- Fix mobile horizontal scrollbar on blog page

#### Build
- Fix wiki submodule URL from SSH to HTTPS for portability
- Fix macOS-only `open` command in coverage-html Makefile target

### Security
- Remove email address disclosure from Dify knowledge base exports (KB-001, KB-002)
- Fix deactivated modules continuing to inject HTML until server restart (INJ-001)

## [0.14.0] - 2026-04-04

### Added

#### Analytics
- Add global IP/CIDR exclusion list in Site Configuration for filtering internal traffic
- Add self-referral filtering using site_url config (www-aware, case-insensitive)
- Add one-time migration to purge historical self-referral data
- Add IP exclusion hint to external analytics settings page

#### Admin UI
- Add multi-line text config type with textarea rendering
- Add per-line IP/CIDR validation with localized error messages (EN, RU)

#### Editor
- Add code block language selector and Prism.js syntax highlighting
- Add Prism.js assets to default and starter theme layouts

### Changed

#### Dependencies
- Update Alpine.js 3.15.10 → 3.15.11
- Update go-sqlite3 1.14.38 → 1.14.40
- Update modernc.org/sqlite 1.48.0 → 1.48.1

### Fixed

#### Analytics
- Fix data race on cached analytics module fields (siteDomain, excludedIPs)
- Fix unhandled errors in analytics self-referral purge migration
- Fix stale config caches not refreshing after admin changes
- Fix EventService IP exclusion only working for shared instance

### Security
- Add frame-ancestors CSP directive
- Add 64 KB size limit for multi-line text config fields
- Add server-side IP/CIDR validation for excluded IPs config

## [0.12.0] - 2026-04-02

### Added

#### Users
- Add author profile fields: avatar, bio, website URL, LinkedIn, GitHub
- Render author info (avatar, bio, social links) on public page templates
- Add author profile section to admin user form with i18n (EN, RU)

### Changed

#### Dependencies
- Update Alpine.js packages 3.15.9 → 3.15.10
- Update TinyMCE 8.3.2 → 8.4.0
- Update go-sqlite3 1.14.37 → 1.14.38

### Fixed

#### Admin UI
- Fix preformatted text invisible in editor
- Fix Alpine.js SRI hash after dependency update

### Security
- Add URL scheme validation for profile URLs (reject javascript:, data:)
- Add domain validation for LinkedIn and GitHub profile URLs
- Add path traversal protection for relative avatar paths
- Add max-length enforcement for profile fields (bio: 500, URLs: 255)

## [0.11.0] - 2026-03-31

### Added

#### Pages
- Add summary field: optional textarea (max 500 chars) replaces auto-generated excerpt on frontend listings
- Add draft page preview for admin and editor users with noindex/nofollow protection

### Changed

#### Theme
- Move page hero text below featured image in developer theme

#### Media
- Relax variant skip threshold so medium-sized images get all variants

#### Dependencies
- Update Alpine.js packages 3.15.8 → 3.15.9
- Update goldmark 1.8.1 → 1.8.2
- Update modernc.org/sqlite 1.47.0 → 1.48.0

### Fixed

#### Media
- Fix OG image picker and featured image variant selection in admin
- Fix OG image falling back to missing variant
- Keep OG and featured image on form validation errors

#### Admin UI
- Fix Alpine.js SRI hash after dependency update
- Fix tag selector dropdown in Alpine.js 3.15
- Fix tag creation from page editor

## [0.10.2] - 2026-03-27

### Added

#### SEO
- Add OG article meta tags: `article:published_time`, `article:modified_time`, `article:author`, `article:section`, `article:tag`
- Add `<link rel="canonical">` to homepage

#### Deployment
- Add "Disabling a Site" section to deploy docs
- Add timestamps to deploy script log output
- Strip ANSI colors when logging to file

### Fixed

#### SEO
- Fix double slashes in sitemap.xml URLs when site URL has trailing slash

#### Modules
- Fix crash when enabling a module that was inactive at server startup

#### Deployment
- Fix backup script reporting false error exit code in Plesk scheduler
- Fix logrotate creating log files with wrong ownership (use copytruncate)
- Fix logrotate not matching nested vhost paths
- Add `su` directive for backup log rotation

### Security
- Fix brace-expansion DoS vulnerability CVE-2026-33750 (npm override to 5.0.5)
- Bump picomatch 4.0.3 → 4.0.4

### Changed
- Merge deployment docs into single `scripts/deploy/README.md`

## [0.10.1] - 2026-03-25

### Added

#### Deployment
- Add binary-only deploy script (`deploy-binary.sh`) for sites using embedded themes
- Support multiple instances in `deploy-binary.sh` to avoid version skew
- Add `deploy-binary` Makefile target
- Add `--skip-binary` flag to deploy script

#### Observability
- Log server start/stop events to admin event log (`/admin/events`)
- Add module no-op template placeholders (privacy, analytics-ext, embed)

#### Documentation
- Add missing make targets to README (build variants, sqlc, templ, deploy-binary)

### Changed
- Update goldmark 1.7.17 → 1.8.1
- Update golang.org/x/image 0.37.0 → 0.38.0
- Simplify logs gitignore (ignore entire directory)
- Disable form captcha requirement for demo

### Fixed
- Fix false positive in `javascript:` suspicious markup detection
- Fix sync-prod-to-dev.sh for macOS and path handling
- Fix custom content detection and remote path in deploy script
- Fix informerBar template error when module is inactive

## [0.10.0] - 2026-03-22

### Added

#### templUI Component Migration
- Convert admin forms to templUI components (input, textarea, selectbox, checkbox, dialog, dropdown)
- Add StatCard and StatusBanner reusable components
- Convert pages, media, and remaining admin search inputs to templUI
- Add grid image variant for admin media cards

#### Admin UI Improvements
- Add pagination to Sentinel banned IPs list
- Add search and language filters to tags list
- Add per-page selector to admin pagers
- Add bulk pager actions for admin lists
- Add 13 missing admin translation keys
- Style pagination footer with muted background
- Add reusable action buttons with left-aligned layout

#### Build Pipeline
- Add templUI JS files to build pipeline for reliable deployment
- Track templUI JS in source directory for fresh clone support

#### Documentation
- Document production security override flags in example configs
- Update module docs to use self-registration pattern
- Add Modules section and git tracking info to custom/README

### Changed
- Pin klaro to 0.7.21 stable release (0.7.22 not tagged as latest)
- Disable production security enforcement for Fly demo instance
- Reduce templUI input focus ring from 3px to 1px
- Polish card styling and dashboard header heights

### Fixed
- Fix form hint inline layout (add display:block globally)
- Fix checkbox alignment in SEO section (flex-start instead of center)
- Fix selectbox value reads for media and editor
- Fix selectbox and class collision issues
- Fix per-page selector for templUI selectbox
- Fix open redirect and CSP nonce fallback
- Fix CSP violation on events ban button
- Fix analytics date range off-by-one
- Fix menu builder Alpine.js title binding errors
- Fix language edit form and style readonly inputs
- Fix forms field builder JS error
- Fix media search input placeholder overlap

### Security
- Harden production defaults and webhook policy
- Allow IPv6 webhook host allowlist entries

## [0.9.0] - 2026-02-18

### Added

#### Admin UI Migration to templ + Tailwind CSS
- Migrate all admin pages from html/template to templ components
- Type-safe view models and store-to-view conversion layer (`internal/handler/templ.go`)
- Hybrid frontend render engine dispatch for gradual migration
- templUI component integration: buttons, badges, cards, alerts, tables, inputs, labels, pagination, page headers
- Tailwind CSS build step in Dockerfile
- `HcaptchaWidgetHTML()`, `AdminLangOptions()` renderer helpers for templ views

#### Embed Proxy (Dify Integration)
- Backend proxy for Dify API calls with plain text output rendering
- Signed embed proxy auth tokens with enforcement
- Embed proxy rate budget and abuse auditing
- HTTPS requirement for embed endpoints
- Embed upstream host allowlist and policy enforcement
- Admin-only restriction for embed settings

#### API Key Security Policies
- Source CIDR allowlists (global and per-key)
- Expiry policy and maximum lifetime enforcement
- Source IP anomaly detection and automatic revocation
- Default expiry for new API keys
- Legacy non-expiring API key warnings

#### Page Content Security
- HTML sanitization policy on write paths
- Block suspicious page markup in production
- Markup policy enforcement on imports, API writes, and version restores

#### Form Submission Hardening
- Public form submission payload size caps
- Form field value length caps
- Optional captcha policy for public forms
- Webhook field redaction and payload minimization

#### Production Startup Gates
- Block startup on default admin credentials
- Require embed origin, trusted proxy, API CIDR, and captcha configuration
- Security signal summary in health endpoint

#### CSP Nonce Support
- CSP nonce wiring across the entire render pipeline
- Nonce applied to all inline scripts and style tags

#### Developer Tooling
- AGENTS.md repo guidelines for AI agents
- Codex-compatible commit workflow scripts and commands
- templUI CLI commands and frontend-developer agent

### Changed
- Update Go to 1.26.0
- Remove unused legacy admin, auth, and public HTML templates
- Make logout POST-only across UI and routing
- Trust proxy headers only from configured trusted proxies
- Fail closed on malformed X-Forwarded-For chains

### Fixed
- Fix cross-platform WebM MIME type detection
- Fix admin language switcher type assertion
- Fix developer theme engine selection
- Sanitize informer text instead of escaping
- Harden admin fallback redirect URL handling

### Security
- Trusted proxy IP resolution with fail-closed behavior
- Webhook URL SSRF protection and destination auditing
- Upload MIME validation hardening and extension canonicalization
- JSON and multipart request body size caps
- Harden JSON decoding for admin, API, and Sentinel endpoints
- Embed proxy origin allowlist and rate limiting
- HTTPS policy enforcement for outbound URLs
- Security audit remediation (SEC-001 through SEC-008)

## [0.8.0] - 2026-02-12

### Added

#### Custom Modules
- Custom bookmarks module example in `custom/modules/`
- `init()` self-registration pattern for custom modules
- Custom module documentation, tests, and translations

#### Custom Themes
- Sample custom theme "Starter" with magazine-style card layout
- Starter theme tests, translations, and documentation

#### Scheduler Admin UI
- Scheduler admin interface with task management

#### Demo Mode
- Bookmarks module and theme settings read-only in demo mode

### Changed
- Update Go dependencies

### Fixed
- Fix scheduler SSRF and schedule override issues
- Fix bookmarks module role checks and 404 handling
- Fix taxonomy links losing language prefix
- Fix dockerignore excluding custom modules

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

[Unreleased]: https://github.com/olegiv/ocms-go/compare/v0.20.0...HEAD
[0.20.0]: https://github.com/olegiv/ocms-go/compare/v0.19.0...v0.20.0
[0.19.0]: https://github.com/olegiv/ocms-go/compare/v0.18.1...v0.19.0
[0.18.1]: https://github.com/olegiv/ocms-go/compare/v0.18.0...v0.18.1
[0.18.0]: https://github.com/olegiv/ocms-go/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/olegiv/ocms-go/compare/v0.16.0...v0.17.0
[0.16.0]: https://github.com/olegiv/ocms-go/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/olegiv/ocms-go/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/olegiv/ocms-go/compare/v0.12.0...v0.14.0
[0.12.0]: https://github.com/olegiv/ocms-go/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/olegiv/ocms-go/compare/v0.10.2...v0.11.0
[0.10.2]: https://github.com/olegiv/ocms-go/compare/v0.10.1...v0.10.2
[0.10.1]: https://github.com/olegiv/ocms-go/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/olegiv/ocms-go/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/olegiv/ocms-go/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/olegiv/ocms-go/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/olegiv/ocms-go/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/olegiv/ocms-go/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/olegiv/ocms-go/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/olegiv/ocms-go/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/olegiv/ocms-go/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/olegiv/ocms-go/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/olegiv/ocms-go/compare/v0.0.0...v0.1.0
[0.0.0]: https://github.com/olegiv/ocms-go/releases/tag/v0.0.0
